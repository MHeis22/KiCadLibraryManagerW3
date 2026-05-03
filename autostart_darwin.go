//go:build darwin
// +build darwin

package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed build/appicon.png
var trayIcon []byte

const (
	launchAgentLabel = "com.kicadlibmanager"
	plistFilename    = "com.kicadlibmanager.plist"
)

// ToggleAutoStart creates or removes a LaunchAgent plist in ~/Library/LaunchAgents/.
func (a *App) ToggleAutoStart(enable bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	plistPath := filepath.Join(agentsDir, plistFilename)

	if enable {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot determine executable path: %w", err)
		}
		// Resolve symlinks so the plist points at the real binary
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}

		if err := os.MkdirAll(agentsDir, 0755); err != nil {
			return fmt.Errorf("cannot create LaunchAgents directory: %w", err)
		}

		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>LaunchOnlyOnce</key>
    <true/>
</dict>
</plist>`, launchAgentLabel, exe)

		if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
			return fmt.Errorf("failed to write LaunchAgent plist: %w", err)
		}

		// Load immediately so the change takes effect without a reboot
		if out, err := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err != nil {
			fmt.Printf("    [AutoStart] launchctl load warning: %v — %s\n", err, out)
		}
	} else {
		// Unload before removing so launchd stops tracking it
		exec.Command("launchctl", "unload", "-w", plistPath).Run()
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove LaunchAgent plist: %w", err)
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
