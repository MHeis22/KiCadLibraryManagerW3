package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	maxRetries := 30 // Wait up to 15 seconds (30 * 500ms)
	var lastSize int64 = -1

	for i := 0; i < maxRetries; i++ {
		file, err := os.OpenFile(path, os.O_RDONLY, 0666)
		if err == nil {
			info, statErr := file.Stat()
			file.Close() // Close immediately so we don't lock it

			if statErr == nil {
				currentSize := info.Size()
				// 1. Ignore 0-byte browser placeholders entirely
				if currentSize > 0 {
					// 2. Ensure the file is completely finished growing
					if currentSize == lastSize {
						return true
					}
					lastSize = currentSize
				}
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
						// Ensure we remove the file from the processing map when done.
						// We add a small 2-second buffer to swallow any lingering browser events.
						defer func() {
							time.AfterFunc(2*time.Second, func() {
								a.processing.Delete(path)
							})
						}()

						if !waitForFileReady(path) {
							fmt.Println("--> Ignored file (timed out waiting for lock release):", path)
							return
						}

						if !PeekForKiCad(path) {
							fmt.Println("--> Ignored non-KiCad zip:", filepath.Base(path))
							return
						}

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

func (a *App) ProcessFile(filename string, category string, repoName string) error {
	fmt.Printf("--> Processing %s into the %s category of %s...\n", filename, category, repoName)

	// --- Phase 1: read config snapshot and persist any new category ---
	a.mu.Lock()
	conf := LoadConfig()

	if repoName == "" && len(conf.Repositories) > 0 {
		repoName = conf.Repositories[0].Name
	} else if repoName == "" {
		repoName = "CustomLibs"
	}

	isNew := true
	for _, c := range conf.Categories {
		if strings.EqualFold(c, category) {
			isNew = false
			break
		}
	}
	if isNew {
		conf.Categories = append(conf.Categories, category)
		SaveConfig(conf)
	}

	baseLibPath := conf.BaseLibPath
	watchDir := conf.WatchDir
	a.mu.Unlock()

	// --- Phase 2: heavy file I/O (no lock held) ---
	fullPath := filename
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(watchDir, filename)
	}

	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("cannot access file: %w", err)
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
			return fmt.Errorf("failed to extract zip: %w", err)
		}
		defer os.RemoveAll(tempDir)
	}

	if baseLibPath == "" {
		return fmt.Errorf("base library path is not configured")
	}

	targetRepoRoot := filepath.Join(baseLibPath, repoName)

	addedFiles, master, backup, err := IntegrateParts(assets, category, targetRepoRoot, repoName)
	if err != nil {
		return fmt.Errorf("integration failed: %w", err)
	}

	fmt.Println("--> Successfully integrated parts into", repoName)

	// --- Phase 3: lock again to append history and save ---
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
	if len(conf.History) > 5 {
		conf.History = conf.History[len(conf.History)-5:]
	}
	if err := SaveConfig(conf); err != nil {
		fmt.Println("Warning: failed to save config:", err)
	}
	a.mu.Unlock()

	commitMsg := fmt.Sprintf("Added new part from %s into %s", filepath.Base(fullPath), category)
	go GitSmartSync(targetRepoRoot, commitMsg)

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

	keywords := map[string]string{
		"regulator":       "Regulators",
		"opamp":           "OpAmps",
		"amplifier":       "OpAmps",
		"mcu":             "MCU",
		"microcontroller": "MCU",
		"connector":       "Connectors",
		"header":          "Connectors",
		"resistor":        "Passives",
		"capacitor":       "Passives",
		"inductor":        "Passives",
	}

	scanContent := func(content string) string {
		lowerContent := strings.ToLower(content)
		for kw, cat := range keywords {
			if strings.Contains(lowerContent, kw) {
				return cat
			}
		}
		return ""
	}

	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		return ""
	}

	if fileInfo.IsDir() {
		var match string
		filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".kicad_sym") {
				symBytes, _ := os.ReadFile(path)
				match = scanContent(string(symBytes))
				if match != "" {
					return fmt.Errorf("found")
				}
			}
			return nil
		})
		return match
	}

	if strings.HasSuffix(strings.ToLower(fullPath), ".zip") {
		// Read entire zip into memory instantly to prevent file locking
		data, err := os.ReadFile(fullPath)
		if err == nil {
			bytesReader := bytes.NewReader(data)
			r, err := zip.NewReader(bytesReader, int64(len(data)))
			if err == nil {
				for _, f := range r.File {
					if strings.HasSuffix(strings.ToLower(f.Name), ".kicad_sym") {
						rc, err := f.Open()
						if err == nil {
							symBytes, _ := io.ReadAll(rc)
							rc.Close()
							return scanContent(string(symBytes))
						}
					}
				}
			}
		}
	} else if strings.HasSuffix(strings.ToLower(fullPath), ".kicad_sym") {
		symBytes, err := os.ReadFile(fullPath)
		if err == nil {
			return scanContent(string(symBytes))
		}
	}

	return ""
}
