package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const autoStartValueName = "FuseBridge"

func isAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`,
		registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autoStartValueName)
	return err == nil
}

func setAutoStart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`,
		registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return k.SetStringValue(autoStartValueName, `"`+exe+`"`)
	}
	return k.DeleteValue(autoStartValueName)
}
