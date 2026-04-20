package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type App struct {
	app           *application.App // Updated to *application.App for Wails v3
	mainWindow    *application.WebviewWindow
	watcherCtx    context.Context
	watcherCancel context.CancelFunc
	mu            sync.Mutex // protects all LoadConfig/SaveConfig pairs
	processing    sync.Map
}

func NewApp(app *application.App, window *application.WebviewWindow) *App { // Updated here as well
	return &App{
		app:        app,
		mainWindow: window,
	}
}

// ServiceStartup is the v3 hook for running initialization logic once the app starts
func (a *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	conf := LoadConfig()
	if conf.BaseLibPath == "" {
		if a.mainWindow != nil {
			macActivate()
			a.mainWindow.Show()
		}
	}

	InitializeKiCadLibraries(conf) // Ensure the base library structure exists before starting the watcher
	a.StartWatcher()
	a.startSyncPoller()
	return nil
}

// startSyncPoller runs a background goroutine that checks remote sync status every 15 minutes.
func (a *App) startSyncPoller() {
	go func() {
		// Emit an initial status shortly after startup so the icon has a value
		time.Sleep(5 * time.Second)
		a.pollSyncStatus()

		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			a.pollSyncStatus()
		}
	}()
}

// pollSyncStatus fetches from all git remotes and emits a per-repo sync-status JSON map.
func (a *App) pollSyncStatus() {
	conf := LoadConfig()
	if conf.BaseLibPath == "" {
		return
	}

	statusMap := map[string]string{}
	for _, repo := range conf.Repositories {
		if repo.URL == "" {
			continue
		}
		repoPath := filepath.Join(conf.BaseLibPath, repo.Name)
		behind, err := GitFetchAndCheckStatus(repoPath)
		if err != nil || behind {
			statusMap[repo.Name] = "warning"
		} else {
			statusMap[repo.Name] = "synced"
		}
	}

	data, _ := json.Marshal(statusMap)
	a.app.Event.Emit("sync-status", string(data))
}

// SyncAllRepositories runs git pull --rebase on every git-backed repository.
func (a *App) SyncAllRepositories() error {
	a.app.Event.Emit("sync-status", "syncing")

	conf := LoadConfig()
	if conf.BaseLibPath == "" {
		return fmt.Errorf("base library path not configured")
	}

	var errs []string
	statusMap := map[string]string{}
	for _, repo := range conf.Repositories {
		if repo.URL == "" {
			continue
		}
		repoPath := filepath.Join(conf.BaseLibPath, repo.Name)
		if err := GitPull(repoPath); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", repo.Name, err))
			statusMap[repo.Name] = "warning"
		} else {
			statusMap[repo.Name] = "synced"
		}
	}

	data, _ := json.Marshal(statusMap)
	a.app.Event.Emit("sync-status", string(data))

	if len(errs) > 0 {
		return fmt.Errorf("some repositories failed to sync: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *App) StartWatcher() {
	if a.watcherCancel != nil {
		a.watcherCancel() // Stop existing watcher if running
	}
	a.watcherCtx, a.watcherCancel = context.WithCancel(context.Background())
	go a.watchFolder(a.watcherCtx)
}

// Helper function to safely wait for a file to finish downloading/copying
func waitForFileReady(path string) bool {
	maxRetries := 120 // Increased: Wait up to 60 seconds (120 * 500ms) for AV/SmartScreen
	var lastSize int64 = -1

	for i := 0; i < maxRetries; i++ {
		info, err := os.Stat(path) // Use Stat instead of OpenFile to avoid locking

		if err == nil {
			currentSize := info.Size()
			// 1. Ignore 0-byte browser placeholders entirely
			if currentSize > 0 {
				// 2. Ensure the file is completely finished growing
				if currentSize == lastSize {
					// 3. Do ONE final OpenFile check to ensure the browser has fully released its write-lock
					file, lockErr := os.OpenFile(path, os.O_RDONLY, 0666)
					if lockErr == nil {
						file.Close()
						return true
					}
				}
				lastSize = currentSize
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}
func (a *App) watchFolder(ctx context.Context) {
	conf := LoadConfig()
	watchPath := conf.WatchDir

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Println("Error creating watcher:", err)
		return
	}
	defer watcher.Close()

	if watchPath == "" {
		fmt.Println("--> Watch directory not configured, watcher not started")
		a.app.Event.Emit("watcher-error", "Watch directory is not configured.")
		return
	}

	err = watcher.Add(watchPath)
	if err != nil {
		fmt.Println("Error adding path to watcher:", err)
		a.app.Event.Emit("watcher-error", fmt.Sprintf("Cannot watch folder: %s", err.Error()))
		return
	}
	fmt.Println("--> Wails Backend Successfully watching:", watchPath)

	for {
		select {
		case <-ctx.Done():
			fmt.Println("--> Watcher stopped for directory:", watchPath)
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				ext := strings.ToLower(filepath.Ext(event.Name))
				if ext == ".epw" || ext == ".zip" {

					// --- Deduplication Check ---
					// If the file is already being processed, ignore this duplicate event
					if _, loaded := a.processing.LoadOrStore(event.Name, true); loaded {
						continue
					}

					go func(path string) {
						// Flag to track if we successfully processed the file
						success := false

						defer func() {
							if success {
								// Add a small 2-second buffer to swallow lingering browser events
								time.AfterFunc(2*time.Second, func() {
									a.processing.Delete(path)
								})
							} else {
								// If it failed/timed out, clear IMMEDIATELY so we don't accidentally ignore the real file
								a.processing.Delete(path)
							}
						}()

						if !waitForFileReady(path) {
							fmt.Println("--> Ignored file (timed out waiting for lock release):", path)
							return
						}

						if !PeekForKiCad(path) {
							fmt.Println("--> Ignored non-KiCad zip:", filepath.Base(path))
							return
						}

						// If we reached here, the file is ready and valid
						success = true

						filename := filepath.Base(path)
						fmt.Println("Real file verified:", filename)

						if a.mainWindow != nil {
							macActivate()
							a.mainWindow.Show()
						}
						a.app.Event.Emit("file-detected", filename)
					}(event.Name)
				}
			}
		case <-watcher.Errors:
		}
	}
}

// ---- THESE FUNCTIONS ARE EXPOSED TO JAVASCRIPT ----

func (a *App) GetConfig() Config {
	return LoadConfig()
}

func (a *App) SelectDirectory() string {
	// Updated to use the app.Dialog manager based on the v3 documentation
	dir, err := a.app.Dialog.OpenFile().
		SetTitle("Select Directory").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()

	if err != nil {
		return ""
	}
	return dir
}

func (a *App) SelectWatchDirectory() string {
	dir := a.SelectDirectory()
	if dir != "" {
		a.mu.Lock()
		conf := LoadConfig()
		conf.WatchDir = dir
		SaveConfig(conf)
		a.mu.Unlock()
		a.StartWatcher() // Restart the watcher loop on the new directory
		fmt.Println("--> Watch directory updated to:", dir)
	}
	return dir
}

func (a *App) SaveSetup(path string) error {
	a.mu.Lock()
	conf := LoadConfig()
	conf.BaseLibPath = path
	if err := SaveConfig(conf); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("failed to save config: %w", err)
	}
	a.mu.Unlock()
	fmt.Println("--> Saved new Base Library Path:", path)
	// Immediately register KiCad tables and env var so parts appear without restart
	InitializeKiCadLibraries(conf)
	return nil
}

func (a *App) AddRepository(name string, url string) error {
	a.mu.Lock()
	conf := LoadConfig()
	if conf.BaseLibPath == "" {
		a.mu.Unlock()
		return fmt.Errorf("base library path is not set")
	}
	a.mu.Unlock()

	destPath := filepath.Join(conf.BaseLibPath, name)

	if url != "" {
		fmt.Printf("--> Validating Git URL: %s\n", url)
		if err := ValidateGitURL(url); err != nil {
			return fmt.Errorf("cannot reach Git repository: %w", err)
		}
		if err := GitClone(url, destPath); err != nil {
			return fmt.Errorf("failed to clone repository: %w", err)
		}
	} else {
		os.MkdirAll(destPath, os.ModePerm)
	}

	a.mu.Lock()
	conf = LoadConfig() // reload in case another goroutine modified it during the clone
	conf.Repositories = append(conf.Repositories, Repository{Name: name, URL: url})
	SaveConfig(conf)
	a.mu.Unlock()
	return nil
}

// RemoveRepository unlinks a repository from the app config without deleting files on disk.
func (a *App) RemoveRepository(repoName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	conf := LoadConfig()

	found := false
	filtered := conf.Repositories[:0]
	for _, r := range conf.Repositories {
		if r.Name == repoName {
			found = true
		} else {
			filtered = append(filtered, r)
		}
	}
	if !found {
		return fmt.Errorf("repository %q not found", repoName)
	}
	if len(filtered) == 0 {
		return fmt.Errorf("cannot remove the last repository")
	}
	conf.Repositories = filtered
	if conf.DefaultRepo == repoName {
		conf.DefaultRepo = ""
	}
	SaveConfig(conf)
	return nil
}

// SetDefaultRepository marks a repository as the default import target.
func (a *App) SetDefaultRepository(repoName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	conf := LoadConfig()

	found := false
	for _, r := range conf.Repositories {
		if r.Name == repoName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("repository %q not found", repoName)
	}
	conf.DefaultRepo = repoName
	SaveConfig(conf)
	return nil
}

// UndoAction reverts a previously imported component
func (a *App) UndoAction(id string) bool {
	a.mu.Lock()
	conf := LoadConfig()
	var newHistory []HistoryItem
	var target HistoryItem
	found := false

	for _, item := range conf.History {
		if item.ID == id {
			target = item // copy by value — safe after slice is reassigned below
			found = true
		} else {
			newHistory = append(newHistory, item)
		}
	}

	if !found {
		a.mu.Unlock()
		return false
	}

	conf.History = newHistory
	if err := SaveConfig(conf); err != nil {
		fmt.Println("Warning: failed to save config after undo:", err)
	}
	a.mu.Unlock()

	for _, f := range target.AddedFiles {
		os.Remove(f)
		fmt.Println("    [Undo] Removed:", f)
	}

	if target.SymbolBackup != "" && target.SymbolMaster != "" {
		if _, err := os.Stat(target.SymbolBackup); err == nil {
			err = os.Rename(target.SymbolBackup, target.SymbolMaster)
			if err != nil {
				copyFile(target.SymbolBackup, target.SymbolMaster)
				os.Remove(target.SymbolBackup)
			}
			fmt.Println("    [Undo] Restored symbol library from backup:", target.SymbolMaster)
		}
	}

	fmt.Println("--> Successfully undone import of", target.Filename)
	return true
}

func (a *App) SkipFile(filename string) {
	fmt.Printf("--> User chose to skip %s\n", filename)
}

func (a *App) HandleDroppedItem(path string) error {
	fmt.Println("--> Dropped item detected:", path)

	if !a.isValidKiCadItem(path) {
		fmt.Println("--> Rejected invalid item:", path)
		a.app.Event.Emit("file-rejected", path)
		return nil
	}

	if a.mainWindow != nil {
		macActivate()
		a.mainWindow.Show()
	}
	a.app.Event.Emit("file-detected", path)
	return nil
}

func (a *App) isValidKiCadItem(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	if info.IsDir() {
		found := false
		filepath.Walk(path, func(p string, i os.FileInfo, e error) error {
			if e == nil && !i.IsDir() {
				ext := strings.ToLower(filepath.Ext(i.Name()))
				switch ext {
				case ".kicad_sym", ".kicad_mod", ".step", ".stp", ".wrl", ".kicad_sch", ".kicad_pcb":
					found = true
					return fmt.Errorf("found")
				}
			}
			return nil
		})
		return found
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".zip" || ext == ".epw" {
		return PeekForKiCad(path)
	}
	switch ext {
	case ".kicad_sym", ".kicad_mod", ".step", ".stp", ".wrl", ".kicad_sch", ".kicad_pcb":
		return true
	}
	return false
}

// extractAssets is a helper function to cleanly handle getting files out of a zip or folder.
func extractAssets(fullPath string) (*KiCadAssets, string, error) {
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		return nil, "", fmt.Errorf("cannot access file: %w", err)
	}

	var assets *KiCadAssets
	var tempDir string

	if fileInfo.IsDir() || strings.ToLower(filepath.Ext(fullPath)) != ".zip" {
		assets = &KiCadAssets{}

		if !fileInfo.IsDir() {
			ext := strings.ToLower(filepath.Ext(fullPath))
			switch ext {
			case ".kicad_sym":
				assets.SymbolPath = fullPath
			case ".kicad_mod":
				assets.FootprintPath = fullPath
			case ".step", ".stp", ".wrl":
				assets.ModelPath = fullPath
			case ".kicad_sch":
				assets.SchBlockPath = fullPath
			case ".kicad_pcb":
				assets.PcbBlockPath = fullPath
			}
		} else {
			filepath.Walk(fullPath, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(info.Name()))
				switch ext {
				case ".kicad_sym":
					assets.SymbolPath = p
				case ".kicad_mod":
					assets.FootprintPath = p
				case ".step", ".stp", ".wrl":
					assets.ModelPath = p
				case ".kicad_sch":
					assets.SchBlockPath = p
				case ".kicad_pcb":
					assets.PcbBlockPath = p
				}
				return nil
			})
		}
	} else {
		assets, tempDir, err = ExtractAndFind(fullPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to extract zip: %w", err)
		}
	}

	return assets, tempDir, nil
}

// CheckConflicts scans the target library locations and checks if any files with matching names already exist.
func (a *App) CheckConflicts(filename string, category string, repoName string) ([]string, error) {
	a.mu.Lock()
	conf := LoadConfig()
	if repoName == "" {
		if conf.DefaultRepo != "" {
			repoName = conf.DefaultRepo
		} else if len(conf.Repositories) > 0 {
			repoName = conf.Repositories[0].Name
		} else {
			repoName = "CustomLibs"
		}
	}
	baseLibPath := conf.BaseLibPath
	watchDir := conf.WatchDir
	a.mu.Unlock()

	if baseLibPath == "" {
		return nil, fmt.Errorf("base library path is not configured")
	}

	fullPath := filename
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(watchDir, filename)
	}

	assets, tempDir, err := extractAssets(fullPath)
	if err != nil {
		return nil, err
	}
	if tempDir != "" {
		defer os.RemoveAll(tempDir)
	}

	targetRepoRoot := filepath.Join(baseLibPath, repoName)
	var conflicts []string

	if assets.FootprintPath != "" {
		dest := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category), filepath.Base(assets.FootprintPath))
		if _, err := os.Stat(dest); err == nil {
			conflicts = append(conflicts, fmt.Sprintf("Footprint '%s' already exists.", filepath.Base(assets.FootprintPath)))
		}
	}

	if assets.ModelPath != "" {
		dest := filepath.Join(targetRepoRoot, "packages3d", fmt.Sprintf("%s.3dshapes", category), filepath.Base(assets.ModelPath))
		if _, err := os.Stat(dest); err == nil {
			conflicts = append(conflicts, fmt.Sprintf("3D Model '%s' already exists.", filepath.Base(assets.ModelPath)))
		}
	}

	if assets.SymbolPath != "" {
		masterSym := filepath.Join(targetRepoRoot, "symbols", fmt.Sprintf("%s.kicad_sym", category))
		if _, err := os.Stat(masterSym); err == nil {
			srcBytes, _ := os.ReadFile(assets.SymbolPath)
			reSymName := regexp.MustCompile(`(?s)\(\s*symbol\s+"([^"]+)"`)
			match := reSymName.FindStringSubmatch(string(srcBytes))
			if len(match) > 1 {
				symName := match[1]
				masterBytes, _ := os.ReadFile(masterSym)
				if strings.Contains(string(masterBytes), fmt.Sprintf(`(symbol "%s"`, symName)) {
					conflicts = append(conflicts, fmt.Sprintf("Symbol '%s' already exists in category '%s'.", symName, category))
				}
			}
		}
	}

	return conflicts, nil
}

func (a *App) ProcessFile(filename string, category string, repoName string, conflictStrategy string, newName string) error {
	fmt.Printf("--> Processing %s into the %s category of %s (Strategy: %s)...\n", filename, category, repoName, conflictStrategy)

	// --- Phase 1: read config snapshot and persist any new category ---
	a.mu.Lock()
	conf := LoadConfig()

	if repoName == "" {
		if conf.DefaultRepo != "" {
			repoName = conf.DefaultRepo
		} else if len(conf.Repositories) > 0 {
			repoName = conf.Repositories[0].Name
		} else {
			repoName = "CustomLibs"
		}
	}

	// Safely add category and auto-seed keywords to the dictionary
	conf.AddCustomCategory(category)
	SaveConfig(conf)

	baseLibPath := conf.BaseLibPath
	watchDir := conf.WatchDir
	a.mu.Unlock()

	// --- Phase 2: heavy file I/O (no lock held) ---
	fullPath := filename
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(watchDir, filename)
	}

	assets, tempDir, err := extractAssets(fullPath)
	if err != nil {
		return fmt.Errorf("failed to process file assets: %w", err)
	}
	if tempDir != "" {
		defer os.RemoveAll(tempDir)
	}

	if baseLibPath == "" {
		return fmt.Errorf("base library path is not configured")
	}

	targetRepoRoot := filepath.Join(baseLibPath, repoName)
	commitMsg := fmt.Sprintf("Added new part from %s into %s", filepath.Base(fullPath), category)

	// --- Phase 2.5: Pre-emptive pull to minimise the conflict window ---
	isGit := isGitRepository(targetRepoRoot)
	if isGit {
		a.app.Event.Emit("sync-status", "syncing")
		if pullErr := GitPull(targetRepoRoot); pullErr != nil {
			fmt.Printf("    [Git Warning] Pre-emptive pull skipped: %v. Proceeding in local-only mode.\n", pullErr)
			a.app.Event.Emit("sync-status", "warning")
			isGit = false
		}
	}

	// --- Phase 3: Integrate → Commit → Push, with retry on push rejection ---
	const maxPushRetries = 3
	var addedFiles []string
	var master, backup string
	pushed := false

	for attempt := 0; attempt < maxPushRetries; attempt++ {
		if attempt > 0 {
			fmt.Printf("    [Git] Push rejected, retrying (%d/%d)...\n", attempt, maxPushRetries-1)
			a.app.Event.Emit("sync-status", "syncing")

			if resetErr := GitResetLastCommit(targetRepoRoot); resetErr != nil {
				fmt.Printf("    [Git Error] Reset failed: %v. Saving locally.\n", resetErr)
				break
			}
			if pullErr := GitPull(targetRepoRoot); pullErr != nil {
				fmt.Printf("    [Git Warning] Re-pull failed: %v. Saving locally.\n", pullErr)
				break
			}
		}

		var intErr error
		addedFiles, master, backup, intErr = IntegrateParts(assets, category, targetRepoRoot, repoName, conflictStrategy, newName)
		if intErr != nil {
			return fmt.Errorf("integration failed: %w", intErr)
		}

		if !isGit {
			pushed = true
			break
		}

		var pushErr error
		pushed, pushErr = GitCommitAndPush(targetRepoRoot, commitMsg)
		if pushErr != nil {
			return fmt.Errorf("git sync error: %w", pushErr)
		}
		if pushed {
			break
		}
	}

	if isGit {
		if pushed {
			a.app.Event.Emit("sync-status", "synced")
		} else {
			fmt.Printf("    [Git Warning] Could not push after %d attempts. Changes saved locally.\n", maxPushRetries)
			a.app.Event.Emit("sync-status", "warning")
		}
	}

	fmt.Println("--> Successfully integrated parts into", repoName)

	// --- Phase 4: lock again to append history and save ---
	newItem := HistoryItem{
		ID:           fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp:    time.Now().Unix(),
		Filename:     filepath.Base(fullPath),
		Category:     category,
		RepoName:     repoName,
		AddedFiles:   addedFiles,
		SymbolMaster: master,
		SymbolBackup: backup,
	}

	a.mu.Lock()
	conf = LoadConfig() // reload so we don't clobber concurrent changes
	conf.History = append(conf.History, newItem)

	// Increased history size to 10 entries instead of 5
	if len(conf.History) > 10 {
		conf.History = conf.History[len(conf.History)-10:]
	}

	if err := SaveConfig(conf); err != nil {
		fmt.Println("Warning: failed to save config:", err)
	}
	a.mu.Unlock()

	if !filepath.IsAbs(filename) {
		os.Remove(fullPath)
	}

	return nil
}

func (a *App) HideWindow() {
	fmt.Println("--> User canceled.")
	if a.mainWindow != nil {
		a.mainWindow.Hide()
	}
	macDeactivate()
}

func (a *App) GetItemSummary(filename string) string {
	conf := LoadConfig()

	fullPath := filename
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(conf.WatchDir, filename)
	}

	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		return "Error reading item."
	}

	hasSymbol, hasFootprint, has3D, hasBlocks := false, false, false, false

	checkExt := func(ext string) {
		switch ext {
		case ".kicad_sym":
			hasSymbol = true
		case ".kicad_mod":
			hasFootprint = true
		case ".step", ".stp", ".wrl":
			has3D = true
		case ".kicad_sch", ".kicad_pcb":
			hasBlocks = true
		}
	}

	if fileInfo.IsDir() {
		filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				checkExt(strings.ToLower(filepath.Ext(info.Name())))
			}
			return nil
		})
	} else if strings.HasSuffix(strings.ToLower(fullPath), ".zip") {
		// Read entire zip into memory instantly to prevent file locking
		data, err := os.ReadFile(fullPath)
		if err == nil {
			bytesReader := bytes.NewReader(data)
			r, err := zip.NewReader(bytesReader, int64(len(data)))
			if err == nil {
				for _, f := range r.File {
					checkExt(strings.ToLower(filepath.Ext(f.Name)))
				}
			}
		}
	} else {
		checkExt(strings.ToLower(filepath.Ext(fullPath)))
	}

	var found []string
	if hasSymbol {
		found = append(found, "Symbol")
	}
	if hasFootprint {
		found = append(found, "Footprint")
	}
	if has3D {
		found = append(found, "3D Model")
	}
	if hasBlocks {
		found = append(found, "Design Block")
	}

	if len(found) == 0 {
		return "No KiCad assets recognized."
	}
	return "Includes: " + strings.Join(found, ", ")
}

func (a *App) GuessCategory(filename string) string {
	conf := LoadConfig()
	fullPath := filename
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(conf.WatchDir, filename)
	}

	// 1. Targeted regex: Extract ONLY the official description and keywords fields
	descRegex := regexp.MustCompile(`(?i)\(property\s+"(?:ki_description|ki_keywords)"\s+"([^"]+)"`)

	// Improved Punctuation filter: Added semicolon, colon, pipe, and underscore
	f := func(c rune) bool {
		return c == ' ' || c == ',' || c == '.' || c == '-' || c == '/' ||
			c == '(' || c == ')' || c == ';' || c == ':' || c == '|' || c == '_'
	}

	// Internal helper to score content based on the longest keyword match
	scanContent := func(content string) (string, int) {
		matches := descRegex.FindAllStringSubmatch(content, -1)
		if len(matches) == 0 {
			return "", 0
		}

		var textToScan string
		for _, match := range matches {
			if len(match) > 1 {
				textToScan += match[1] + " "
			}
		}

		words := strings.FieldsFunc(strings.ToLower(textToScan), f)
		normalizedText := " " + strings.Join(words, " ") + " "

		var bestMatch string
		var longestKw int

		for cat, keywords := range conf.AutoCategoryMap {
			for _, kw := range keywords {
				kwWords := strings.FieldsFunc(strings.ToLower(kw), f)
				paddedKw := " " + strings.Join(kwWords, " ") + " "

				if strings.Contains(normalizedText, paddedKw) {
					if len(paddedKw) > longestKw {
						longestKw = len(paddedKw)
						bestMatch = cat
					}
				}
			}
		}
		return bestMatch, longestKw
	}

	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		return ""
	}

	var finalMatch string
	var maxScore int

	// Logic to capture the best match across multiple files (or single file)
	processResult := func(match string, score int) {
		if score > maxScore {
			maxScore = score
			finalMatch = match
			// fmt.Printf("--> Scored: %s (%d) | Found in: %s\n", match, score, filepath.Base(fullPath))
		}
	}

	if fileInfo.IsDir() {
		filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".kicad_sym") {
				symBytes, _ := os.ReadFile(path)
				m, s := scanContent(string(symBytes))
				processResult(m, s)
			}
			return nil
		})
	} else if strings.HasSuffix(strings.ToLower(fullPath), ".zip") {
		data, err := os.ReadFile(fullPath)
		if err == nil {
			r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err == nil {
				for _, f := range r.File {
					if strings.HasSuffix(strings.ToLower(f.Name), ".kicad_sym") {
						rc, _ := f.Open()
						symBytes, _ := io.ReadAll(rc)
						rc.Close()
						m, s := scanContent(string(symBytes))
						processResult(m, s)
					}
				}
			}
		}
	} else if strings.HasSuffix(strings.ToLower(fullPath), ".kicad_sym") {
		symBytes, err := os.ReadFile(fullPath)
		if err == nil {
			m, s := scanContent(string(symBytes))
			processResult(m, s)
		}
	}

	return finalMatch
}
