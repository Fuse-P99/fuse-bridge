package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const autoStartValueName = "FuseBridge"

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func isAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autoStartValueName)
	return err == nil
}

func setAutoStart(enable bool) error {
	// Remove any startup-folder shortcut left by older builds.
	lnk := filepath.Join(os.Getenv("APPDATA"),
		"Microsoft", "Windows", "Start Menu", "Programs", "Startup", "FuseBridge.lnk")
	os.Remove(lnk)

	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if !enable {
		k.DeleteValue(autoStartValueName) // ignore "not found"
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(autoStartValueName, exe)
}
