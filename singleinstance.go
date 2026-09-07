package main

// Single-instance enforcement. A second FuseBridge would fight over the tray
// icon, the log tailer, and the middlemand UDP port (a classic source of
// "checkbox is on but nothing works"). A named mutex scoped to the Windows
// session guarantees one instance per logged-in user; a duplicate launch
// signals the running instance to show its window, then exits.

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	singleInstanceMutexName = `Local\FuseBridge.SingleInstance`
	showWindowEventName     = `Local\FuseBridge.ShowWindow`
	// singleInstanceWait: how long to keep retrying the mutex before concluding
	// another instance really is running. An auto-update relaunch can start the
	// new process while the old one is still tearing down; the named mutex object
	// lives until the old process's handle closes, so a plain instant check would
	// see "already exists" and make the RELAUNCH exit — "updated, never reopened".
	// Retrying bridges that overlap. A genuine duplicate just waits this out.
	singleInstanceWait = 8 * time.Second
)

// Held for the process lifetime; the OS releases it on exit (including crashes).
var singleInstanceMutex windows.Handle

// exitIfAlreadyRunning exits this process when another instance already holds
// the single-instance mutex, after asking it to bring its window forward.
//
// It retries for singleInstanceWait before giving up, so an auto-update
// relaunch that briefly overlaps the exiting old instance succeeds instead of
// mistaking the still-closing old process for a live duplicate. It FAILS OPEN:
// any outcome other than a persistent "already exists" — a clean acquire, or an
// unexpected CreateMutex error — lets the app start. Refusing to start is the
// one unrecoverable failure for a self-updating client, so it is never the
// default; at worst a session runs without the single-instance guard.
func exitIfAlreadyRunning() {
	name, _ := windows.UTF16PtrFromString(singleInstanceMutexName)
	deadline := time.Now().Add(singleInstanceWait)
	signalled := false
	for {
		h, err := windows.CreateMutex(nil, false, name)
		if err != windows.ERROR_ALREADY_EXISTS {
			// Acquired it (err == nil), or an unexpected error — either way, run.
			singleInstanceMutex = h
			go watchShowWindowEvent()
			return
		}
		// Another instance holds the name. Close our extra handle so it isn't us
		// keeping the object alive, nudge the existing instance's window forward
		// once, and retry until the old process (possibly mid-relaunch teardown)
		// releases it or we time out.
		windows.CloseHandle(h)
		if !signalled {
			signalShowWindow()
			signalled = true
		}
		if time.Now().After(deadline) {
			writeLog("another FuseBridge instance is already running — showing it and exiting")
			os.Exit(0)
		}
		time.Sleep(250 * time.Millisecond)
	}
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
