//go:build !windows && !darwin

package main

import (
	_ "embed"
	"fmt"
)

//go:embed build/appicon.png
var trayIcon []byte

// ToggleAutoStart provides a dummy implementation for Mac/Linux.
// Because we defined ToggleAutoStart in autostart_windows.go, we must provide
// a stub here so that Wails can generate the frontend bindings without failing.
func (a *App) ToggleAutoStart(enable bool) error {
	fmt.Println("--> AutoStart toggle is currently only implemented for Windows")
	return nil
}
