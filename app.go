package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const AppVersion = "1.15"

type App struct {
	app           *application.App
	mainWindow    *application.WebviewWindow
	watcherCtx    context.Context
	watcherCancel context.CancelFunc
	mu            sync.Mutex
	processing    sync.Map
}

func NewApp(app *application.App, window *application.WebviewWindow) *App {
	return &App{
		app:        app,
		mainWindow: window,
	}
}

func (a *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	conf := LoadConfig()
	if conf.BaseLibPath == "" {
		if a.mainWindow != nil {
			macActivate()
			a.mainWindow.Show()
		}
	}

	InitializeKiCadLibraries(conf)
	a.StartWatcher()
	a.startSyncPoller()
	return nil
}

func (a *App) startSyncPoller() {
	go func() {
		time.Sleep(5 * time.Second)
		a.pollSyncStatus()

		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			a.pollSyncStatus()
			a.SyncAllRepositories() // Keep manual edits backed up in the background
		}
	}()
}

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
			// Background push of any stashed/popped manual edits
			GitCommitAndPush(repoPath, "Auto-commit manual KiCad edits")
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
		a.watcherCancel()
	}
	a.watcherCtx, a.watcherCancel = context.WithCancel(context.Background())
	go a.watchFolder(a.watcherCtx)
}

func waitForFileReady(path string) bool {
	maxRetries := 120
	var lastSize int64 = -1

	for i := 0; i < maxRetries; i++ {
		info, err := os.Stat(path)

		if err == nil {
			currentSize := info.Size()
			if currentSize > 0 {
				if currentSize == lastSize {
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
		a.app.Event.Emit("watcher-stopped", "")
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

					if _, loaded := a.processing.LoadOrStore(event.Name, true); loaded {
						continue
					}

					go func(path string) {
						success := false

						defer func() {
							if success {
								time.AfterFunc(2*time.Second, func() {
									a.processing.Delete(path)
								})
							} else {
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

func (a *App) GetConfig() Config {
	c := LoadConfig()
	c.Version = AppVersion
	return c
}

func (a *App) SelectDirectory() string {
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
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			a.app.Event.Emit("watcher-error", fmt.Sprintf("Selected directory does not exist: %s", dir))
			return ""
		}
		a.mu.Lock()
		conf := LoadConfig()
		conf.WatchDir = dir
		SaveConfig(conf)
		a.mu.Unlock()
		a.StartWatcher()
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
	conf = LoadConfig()
	conf.Repositories = append(conf.Repositories, Repository{Name: name, URL: url})
	SaveConfig(conf)
	a.mu.Unlock()
	return nil
}

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

func (a *App) AddCategory(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("category name cannot be empty")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	conf := LoadConfig()
	for _, existing := range conf.Categories {
		if strings.EqualFold(existing, name) {
			return fmt.Errorf("category %q already exists", name)
		}
	}
	conf.AddCustomCategory(name)
	if err := SaveConfig(conf); err != nil {
		return err
	}
	InitializeKiCadLibraries(conf)
	return nil
}

func (a *App) RenameCategory(oldName string, newName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	conf := LoadConfig()
	if err := conf.RenameCategory(oldName, newName); err != nil {
		return err
	}
	if err := SaveConfig(conf); err != nil {
		return err
	}
	InitializeKiCadLibraries(conf)
	return nil
}

func (a *App) DeleteCategory(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	conf := LoadConfig()
	if err := conf.DeleteCategory(name); err != nil {
		return err
	}
	if err := SaveConfig(conf); err != nil {
		return err
	}
	InitializeKiCadLibraries(conf)
	return nil
}

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

func (a *App) UndoAction(id string) bool {
	a.mu.Lock()
	conf := LoadConfig()
	var newHistory []HistoryItem
	var target HistoryItem
	found := false

	for _, item := range conf.History {
		if item.ID == id {
			target = item
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
			var walkErr error
			filepath.Walk(fullPath, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(info.Name()))
				switch ext {
				case ".kicad_sym":
					if assets.SymbolPath != "" {
						walkErr = fmt.Errorf("multiple .kicad_sym files found; provide a single-component folder")
						return walkErr
					}
					assets.SymbolPath = p
				case ".kicad_mod":
					if assets.FootprintPath != "" {
						walkErr = fmt.Errorf("multiple .kicad_mod files found; provide a single-component folder")
						return walkErr
					}
					assets.FootprintPath = p
				case ".step", ".stp", ".wrl":
					assets.ModelPath = p
				case ".kicad_sch":
					if assets.SchBlockPath != "" {
						walkErr = fmt.Errorf("multiple .kicad_sch files found; provide a single-component folder")
						return walkErr
					}
					assets.SchBlockPath = p
				case ".kicad_pcb":
					if assets.PcbBlockPath != "" {
						walkErr = fmt.Errorf("multiple .kicad_pcb files found; provide a single-component folder")
						return walkErr
					}
					assets.PcbBlockPath = p
				}
				return nil
			})
			if walkErr != nil {
				return nil, "", walkErr
			}
		}
	} else {
		assets, tempDir, err = ExtractAndFind(fullPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to extract zip: %w", err)
		}
	}

	return assets, tempDir, nil
}

func (a *App) CheckConflicts(filename string, category string, repoName string) ([]string, error) {
	a.mu.Lock()
	conf := LoadConfig()
	if repoName == "" {
		if conf.DefaultRepo != "" {
			repoName = conf.DefaultRepo
		} else if len(conf.Repositories) > 0 {
			repoName = conf.Repositories[0].Name
		} else {
			a.mu.Unlock()
			return nil, fmt.Errorf("no repositories configured; add a repository before importing")
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

	var autoName string
	if assets.SymbolPath != "" {
		srcBytes, _ := os.ReadFile(assets.SymbolPath)
		reSymName := regexp.MustCompile(`(?s)\(\s*symbol\s+"([^"]+)"`)
		match := reSymName.FindStringSubmatch(string(srcBytes))
		if len(match) > 1 {
			autoName = match[1]
			autoName = strings.ReplaceAll(autoName, "/", "_")
			autoName = strings.ReplaceAll(autoName, "\\", "_")
		}
	}

	if assets.FootprintPath != "" {
		fpName := filepath.Base(assets.FootprintPath)
		if autoName != "" {
			fpName = autoName + ".kicad_mod"
		}
		dest := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category), fpName)
		if _, err := os.Stat(dest); err == nil {
			conflicts = append(conflicts, fmt.Sprintf("Footprint '%s' already exists.", fpName))
		}
	}

	if assets.ModelPath != "" {
		modName := filepath.Base(assets.ModelPath)
		if autoName != "" {
			modName = autoName + filepath.Ext(assets.ModelPath)
		}
		dest := filepath.Join(targetRepoRoot, "packages3d", fmt.Sprintf("%s.3dshapes", category), modName)
		if _, err := os.Stat(dest); err == nil {
			conflicts = append(conflicts, fmt.Sprintf("3D Model '%s' already exists.", modName))
		}
	}

	if assets.SymbolPath != "" {
		masterSym := filepath.Join(targetRepoRoot, "symbols", fmt.Sprintf("%s.kicad_sym", category))
		if _, err := os.Stat(masterSym); err == nil {
			if autoName != "" {
				masterBytes, _ := os.ReadFile(masterSym)
				if strings.Contains(string(masterBytes), fmt.Sprintf(`(symbol "%s"`, autoName)) {
					conflicts = append(conflicts, fmt.Sprintf("Symbol '%s' already exists in category '%s'.", autoName, category))
				}
			}
		}
	}

	if assets.SchBlockPath != "" || assets.PcbBlockPath != "" {
		blockSrc := assets.SchBlockPath
		if blockSrc == "" {
			blockSrc = assets.PcbBlockPath
		}
		blockName := autoName
		if blockName == "" {
			blockName = strings.TrimSuffix(filepath.Base(blockSrc), filepath.Ext(blockSrc))
		}
		blockDir := filepath.Join(targetRepoRoot, "blocks", fmt.Sprintf("%s.kicad_blocks", category), fmt.Sprintf("%s.kicad_block", blockName))
		if _, err := os.Stat(blockDir); err == nil {
			conflicts = append(conflicts, fmt.Sprintf("Design block '%s' already exists in category '%s'.", blockName, category))
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
			a.mu.Unlock()
			return fmt.Errorf("no repositories configured; add a repository before importing")
		}
	}

	conf.AddCustomCategory(category)
	SaveConfig(conf)

	baseLibPath := conf.BaseLibPath
	watchDir := conf.WatchDir
	a.mu.Unlock()

	// --- Phase 2: extract assets ---
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

	// --- Phase 2.5: Pre-Flight Sync ---
	// Safely stash manual edits, pull newest, and pop manual edits back on top
	isGit := isGitRepository(targetRepoRoot)
	if isGit {
		a.app.Event.Emit("sync-status", "syncing")
		if pullErr := GitPull(targetRepoRoot); pullErr != nil {
			fmt.Printf("    [Git Warning] Pre-flight pull failed: %v. Proceeding in local-only mode.\n", pullErr)
			a.app.Event.Emit("sync-status", "warning")
		}
	}

	// --- Phase 3: Integrate (Inject new Zip files into the clean repo) ---
	var addedFiles []string
	var master, backup string
	var intErr error

	addedFiles, master, backup, intErr = IntegrateParts(assets, category, targetRepoRoot, repoName, conflictStrategy, newName)
	if intErr != nil {
		return fmt.Errorf("integration failed: %w", intErr)
	}

	// --- Phase 4: Commit & Push ---
	// Pushes the manual edits AND the new zip part seamlessly.
	if isGit {
		const maxPushRetries = 3
		pushed := false

		for attempt := 0; attempt < maxPushRetries; attempt++ {
			if attempt > 0 {
				fmt.Printf("    [Git] Push rejected, pulling remote changes before retry (%d/%d)...\n", attempt, maxPushRetries-1)
				a.app.Event.Emit("sync-status", "syncing")

				// Safe re-pull (rebases our local commit on top of any remote changes teammates just pushed)
				if pullErr := GitPull(targetRepoRoot); pullErr != nil {
					fmt.Printf("    [Git Warning] Re-pull failed: %v. Saving locally.\n", pullErr)
					break
				}
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

		if pushed {
			a.app.Event.Emit("sync-status", "synced")
		} else {
			fmt.Printf("    [Git Warning] Could not push after %d attempts. Changes saved locally.\n", maxPushRetries)
			a.app.Event.Emit("sync-status", "warning")
		}
	}

	fmt.Println("--> Successfully integrated parts into", repoName)

	// --- Phase 5: lock again to append history and save ---
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
	conf = LoadConfig()
	conf.History = append(conf.History, newItem)

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

func (a *App) BrowseLibrary() []LibraryPart {
	conf := LoadConfig()
	if conf.BaseLibPath == "" {
		return nil
	}

	topLevelRe := regexp.MustCompile(`(?m)^  \(symbol "([^"]+)"`)

	var parts []LibraryPart
	for _, repo := range conf.Repositories {
		symDir := filepath.Join(conf.BaseLibPath, repo.Name, "symbols")
		entries, err := os.ReadDir(symDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".kicad_sym") {
				continue
			}
			category := strings.TrimSuffix(entry.Name(), ".kicad_sym")
			content, err := os.ReadFile(filepath.Join(symDir, entry.Name()))
			if err != nil {
				continue
			}
			for _, match := range topLevelRe.FindAllSubmatch(content, -1) {
				if len(match) > 1 {
					parts = append(parts, LibraryPart{
						Name:     string(match[1]),
						Category: category,
						Repo:     repo.Name,
					})
				}
			}
		}
	}

	for i := 1; i < len(parts); i++ {
		for j := i; j > 0 && strings.ToLower(parts[j].Name) < strings.ToLower(parts[j-1].Name); j-- {
			parts[j], parts[j-1] = parts[j-1], parts[j]
		}
	}
	return parts
}

func (a *App) FindDuplicates(filename string) ([]DuplicateInfo, error) {
	conf := LoadConfig()
	if conf.BaseLibPath == "" {
		return nil, nil
	}

	fullPath := filename
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(conf.WatchDir, filename)
	}

	assets, tempDir, err := extractAssets(fullPath)
	if err != nil || assets == nil || assets.SymbolPath == "" {
		return nil, nil
	}
	if tempDir != "" {
		defer os.RemoveAll(tempDir)
	}

	srcBytes, err := os.ReadFile(assets.SymbolPath)
	if err != nil {
		return nil, nil
	}
	nameRe := regexp.MustCompile(`\(\s*symbol\s+"([^"]+)"`)
	match := nameRe.FindSubmatch(srcBytes)
	if len(match) < 2 {
		return nil, nil
	}
	targetName := string(match[1])
	needle := fmt.Sprintf(`(symbol "%s"`, targetName)

	var results []DuplicateInfo
	for _, repo := range conf.Repositories {
		symDir := filepath.Join(conf.BaseLibPath, repo.Name, "symbols")
		entries, err := os.ReadDir(symDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".kicad_sym") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(symDir, entry.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(string(content), needle) {
				results = append(results, DuplicateInfo{
					Name:     targetName,
					Category: strings.TrimSuffix(entry.Name(), ".kicad_sym"),
					Repo:     repo.Name,
				})
			}
		}
	}
	return results, nil
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

	descRegex := regexp.MustCompile(`(?i)\(property\s+"(?:ki_description|ki_keywords)"\s+"([^"]+)"`)

	f := func(c rune) bool {
		return c == ' ' || c == ',' || c == '.' || c == '-' || c == '/' ||
			c == '(' || c == ')' || c == ';' || c == ':' || c == '|' || c == '_'
	}

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

	processResult := func(match string, score int) {
		if score > maxScore {
			maxScore = score
			finalMatch = match
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

type UpdateInfo struct {
	HasUpdate     bool   `json:"hasUpdate"`
	LatestVersion string `json:"latestVersion"`
	ReleaseURL    string `json:"releaseURL"`
}

func (a *App) CheckForUpdates() UpdateInfo {
	const releaseURL = "https://github.com/MHeis22/KiCadLibraryManager/releases/latest"
	const apiURL = "https://api.github.com/repos/MHeis22/KiCadLibraryManager/releases/latest"

	a.mu.Lock()
	conf := LoadConfig()
	a.mu.Unlock()

	if conf.LastUpdateCheck != "" {
		if t, err := time.Parse(time.RFC3339, conf.LastUpdateCheck); err == nil {
			if time.Since(t) < 24*time.Hour {
				return UpdateInfo{}
			}
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return UpdateInfo{}
	}
	req.Header.Set("User-Agent", "KiCadLibraryManager/"+AppVersion)
	resp, err := client.Do(req)
	if err != nil {
		return UpdateInfo{}
	}
	defer resp.Body.Close()

	var result struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return UpdateInfo{}
	}

	a.mu.Lock()
	conf = LoadConfig()
	conf.LastUpdateCheck = time.Now().UTC().Format(time.RFC3339)
	SaveConfig(conf)
	a.mu.Unlock()

	latest := strings.TrimLeft(result.TagName, "Vv")
	if latest == "" || latest == AppVersion {
		return UpdateInfo{}
	}

	return UpdateInfo{
		HasUpdate:     true,
		LatestVersion: latest,
		ReleaseURL:    releaseURL,
	}
}

func (a *App) DismissUpdate(version string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	conf := LoadConfig()
	conf.DismissedUpdateVersion = version
	SaveConfig(conf)
}

func (a *App) OpenReleaseURL() {
	application.Get().Browser.OpenURL("https://github.com/MHeis22/KiCadLibraryManager/releases/latest")
}
