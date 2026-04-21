package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events" // Added the events package
)

//go:embed frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name: "KiCad Library Manager",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// Setting ActivationPolicyAccessory hides the dock icon natively on macOS
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	// Create main window (hidden by default)
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:          "KiCad Library Manager",
		Width:          500,
		Height:         700,
		Hidden:         true,
		EnableFileDrop: true, // Required for Wails v3 to accept file drops
	})

	// Create our service and register it
	appService := NewApp(app, window)
	app.RegisterService(application.NewService(appService))

	// Listen for the native Wails v3 file drop event
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		files := event.Context().DroppedFiles()
		for _, file := range files {
			// Pass each file path directly into the existing logic
			appService.HandleDroppedItem(file)
		}
	})

	// Native Wails v3 System Tray Setup
	systray := app.SystemTray.New()
	systray.SetIcon(trayIcon) // trayIcon comes from autostart_*.go OS specifics
	// systray.SetLabel("Library Manager")

	menu := app.NewMenu()
	menu.Add("Open Settings").OnClick(func(ctx *application.Context) {
		macActivate()
		window.Show()
	})
	menu.AddSeparator()
	menu.Add("Undo Last Import").OnClick(func(ctx *application.Context) {
		conf := LoadConfig()
		if len(conf.History) > 0 {
			lastItem := conf.History[len(conf.History)-1]
			appService.UndoAction(lastItem.ID)
		}
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(ctx *application.Context) {
		app.Quit()
	})

	systray.SetMenu(menu)
	systray.OnClick(func() {
		macActivate()
		window.Show()
	})

	err := app.Run()
	if err != nil {
		log.Fatal(err)
	}
}
