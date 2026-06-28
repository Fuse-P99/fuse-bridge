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
// signal is received. openSettings is called from the tray menu.
func runTray(openSettings func()) {
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

	// Static icon + tooltip — the tray no longer reflects connection/EQ state.
	icon := iconConnected
	if icon == nil {
		icon = iconStartup
	}
	if icon != nil {
		ni.SetIcon(icon)
	}
	ni.SetToolTip("Fuse Bridge")
	ni.SetVisible(true)

	settingsAction := walk.NewAction()
	settingsAction.SetText("Open")
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

// SetTrayStatus is intentionally a no-op: the tray tooltip stays "Fuse Bridge"
// regardless of EQ/connection state. Kept so existing callers still compile.
func SetTrayStatus(status string) {}

// SetTrayConnected is intentionally a no-op: the tray icon is static and no
// longer reflects connection state. Kept so existing callers still compile.
func SetTrayConnected(connected bool) {}
