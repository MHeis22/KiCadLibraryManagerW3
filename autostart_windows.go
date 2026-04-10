//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

const (
	autoStartRegPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	autoStartAppName = "KiCadLibMgr"
)

// ToggleAutoStart adds or removes the app from the Windows Registry Run key.
// Using the Registry is locale-independent; the "Start Menu" folder path is
// localised on non-English Windows and must not be hardcoded.
func (a *App) ToggleAutoStart(enable bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open autostart registry key: %w", err)
	}
	defer key.Close()

	if enable {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to resolve executable path: %w", err)
		}
		if err := key.SetStringValue(autoStartAppName, exe); err != nil {
			return fmt.Errorf("failed to write autostart registry value: %w", err)
		}
	} else {
		if err := key.DeleteValue(autoStartAppName); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("failed to remove autostart registry value: %w", err)
		}
	}

	a.mu.Lock()
	conf := LoadConfig()
	conf.AutoStart = enable
	SaveConfig(conf)
	a.mu.Unlock()

	fmt.Println("--> AutoStart set to:", enable)
	return nil
}
