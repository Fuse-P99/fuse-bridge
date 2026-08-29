package main

// Single-instance enforcement. A second FuseBridge would fight over the tray
// icon, the log tailer, and the middlemand UDP port (a classic source of
// "checkbox is on but nothing works"). A named mutex scoped to the Windows
// session guarantees one instance per logged-in user; a duplicate launch
// signals the running instance to show its window, then exits.

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	singleInstanceMutexName = `Local\FuseBridge.SingleInstance`
	showWindowEventName     = `Local\FuseBridge.ShowWindow`
)

// Held for the process lifetime; the OS releases it on exit (including crashes).
var singleInstanceMutex windows.Handle

// exitIfAlreadyRunning exits this process when another instance already holds
// the single-instance mutex, after asking it to bring its window forward.
// Safe with the auto-updater: the swap script waits (Wait-Process) for the old
// process to exit before relaunching, so the new binary acquires the mutex
// cleanly.
func exitIfAlreadyRunning() {
	name, _ := windows.UTF16PtrFromString(singleInstanceMutexName)
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		writeLog("another FuseBridge instance is already running — showing it and exiting")
		signalShowWindow()
		os.Exit(0)
	}
	singleInstanceMutex = h
	go watchShowWindowEvent()
}

// signalShowWindow pulses the named event the primary instance listens on.
func signalShowWindow() {
	name, _ := windows.UTF16PtrFromString(showWindowEventName)
	ev, _ := windows.CreateEvent(nil, 0, 0, name) // auto-reset; opens existing
	if ev == 0 {
		return
	}
	windows.SetEvent(ev)
	windows.CloseHandle(ev)
}

// watchShowWindowEvent brings the window forward whenever a duplicate launch
// signals us.
func watchShowWindowEvent() {
	name, _ := windows.UTF16PtrFromString(showWindowEventName)
	ev, _ := windows.CreateEvent(nil, 0, 0, name)
	if ev == 0 {
		return
	}
	for {
		s, err := windows.WaitForSingleObject(ev, windows.INFINITE)
		if err != nil || s != windows.WAIT_OBJECT_0 {
			return
		}
		select {
		case <-wailsReady:
			wailsApp.Show()
		default:
			// UI not up yet — the startup path shows the window itself.
		}
	}
}
