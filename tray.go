package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lxn/walk"
)

var trayIcon *walk.NotifyIcon

// runTray sets up the system tray icon and blocks until the user quits or a
// signal is received. openSettings is called when the user clicks "Settings".
func runTray(openSettings func()) {
	mw, err := walk.NewMainWindow()
	if err != nil {
		fmt.Println("Failed to create main window:", err)
		os.Exit(1)
	}

	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		fmt.Println("Failed to create tray icon:", err)
		os.Exit(1)
	}
	defer ni.Dispose()
	trayIcon = ni

	if err := ni.SetToolTip("Fuse Bridgekeeper Relay"); err != nil {
		fmt.Println("SetToolTip:", err)
	}
	if err := ni.SetVisible(true); err != nil {
		fmt.Println("SetVisible:", err)
	}

	// "Settings" menu item
	settingsAction := walk.NewAction()
	if err := settingsAction.SetText("Settings"); err == nil {
		settingsAction.Triggered().Attach(func() {
			openSettings()
		})
	}
	ni.ContextMenu().Actions().Add(settingsAction)

	// Separator
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())

	// "Quit" menu item
	quitAction := walk.NewAction()
	if err := quitAction.SetText("Quit"); err == nil {
		quitAction.Triggered().Attach(func() {
			walk.App().Exit(0)
		})
	}
	ni.ContextMenu().Actions().Add(quitAction)

	// Handle OS signals for clean shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		walk.App().Exit(0)
	}()

	mw.Run()
}

// SetTrayStatus updates the tray icon tooltip with the current status.
func SetTrayStatus(status string) {
	if trayIcon == nil {
		return
	}
	trayIcon.SetToolTip(status)
}
