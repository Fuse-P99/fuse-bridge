package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const autoStartValueName = "FuseBridge"

func startupShortcutPath() string {
	return filepath.Join(os.Getenv("APPDATA"),
		"Microsoft", "Windows", "Start Menu", "Programs", "Startup", "FuseBridge.lnk")
}

func isAutoStartEnabled() bool {
	_, err := os.Stat(startupShortcutPath())
	return err == nil
}

func setAutoStart(enable bool) error {
	// Remove any legacy registry Run entry.
	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
		k.DeleteValue(autoStartValueName)
		k.Close()
	}

	shortcut := startupShortcutPath()
	if !enable {
		if err := os.Remove(shortcut); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Use PowerShell to create a proper .lnk shortcut in the Startup folder.
	// The Startup folder runs after Explorer is fully loaded, making it reliable
	// for tray apps (unlike the Run registry key, which fires before the shell is ready).
	ps := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Save()`,
		shortcut, exe, filepath.Dir(exe),
	)
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Run()
}
