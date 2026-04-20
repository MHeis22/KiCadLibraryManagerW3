//go:build darwin
// +build darwin

package main

import (
	_ "embed"
	"fmt"
)

//go:embed build/appicon.png
var trayIcon []byte

// ToggleAutoStart must be defined here for macOS so Wails can generate the binding.
func (a *App) ToggleAutoStart(enable bool) error {
	fmt.Println("--> AutoStart toggle is not yet implemented for macOS")
	return nil
}
