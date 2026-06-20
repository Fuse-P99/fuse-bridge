package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/lxn/walk"
)

var (
	trayIcon  *walk.NotifyIcon
	trayOwner *walk.MainWindow // needed to call Synchronize from other goroutines
)

// runTray sets up the system tray icon and blocks until the user quits or a
// signal is received. openSettings and openStatus are called from the tray menu.
func runTray(openSettings func(), openStatus func()) {
	initIcons()

	mw, err := walk.NewMainWindow()
	if err != nil {
		os.Exit(1)
	}
	trayOwner = mw
	overrideClassIcon(mw.Handle()) // fix taskbar icon for main window class

	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		os.Exit(1)
	}
	defer ni.Dispose()
	trayIcon = ni

	if iconStartup != nil {
		ni.SetIcon(iconStartup)
	}
	ni.SetToolTip("Fuse Bridge — waiting for EverQuest...")
	ni.SetVisible(true)

	statusAction := walk.NewAction()
	statusAction.SetText("Status")
	statusAction.Triggered().Attach(func() { openStatus() })
	ni.ContextMenu().Actions().Add(statusAction)

	settingsAction := walk.NewAction()
	settingsAction.SetText("Settings")
	settingsAction.Triggered().Attach(func() { openSettings() })
	ni.ContextMenu().Actions().Add(settingsAction)
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())

	quitAction := walk.NewAction()
	quitAction.SetText("Quit")
	quitAction.Triggered().Attach(func() { walk.App().Exit(0) })
	ni.ContextMenu().Actions().Add(quitAction)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		walk.App().Exit(0)
	}()

	mw.Run()
}

// SetTrayStatus updates the tray tooltip. Safe to call from any goroutine.
func SetTrayStatus(status string) {
	if trayOwner == nil || trayIcon == nil {
		return
	}
	trayOwner.Synchronize(func() {
		trayIcon.SetToolTip(status)
	})
}

// SetTrayConnected switches the tray icon green (connected) or grey
// (disconnected). Safe to call from any goroutine.
func SetTrayConnected(connected bool) {
	if trayOwner == nil || trayIcon == nil {
		return
	}
	trayOwner.Synchronize(func() {
		if connected && iconConnected != nil {
			trayIcon.SetIcon(iconConnected)
		} else if !connected && iconDisconnected != nil {
			trayIcon.SetIcon(iconDisconnected)
		}
	})
}
