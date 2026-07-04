//go:build !bindings

package main

import "time"

func main() {
	// Bail out immediately (before the shell-settle sleep) if another
	// instance is already running — it gets asked to show its window.
	exitIfAlreadyRunning()

	// Give Windows time to finish loading the shell and notification area
	// before we try to create a tray icon.
	time.Sleep(5 * time.Second)

	writeLog("FuseBridge starting, clientVersion=" + clientVersion)
	currentSettings = LoadSettings()
	LoadZones()
	LoadCharCache()
	loadFilteredToons()
	LoadRaidMobs()
	go fetchBotToons()

	wailsApp = NewApp()
	go startWails()

	// On launch: wait for the UI, run the startup update check, and either show
	// the "Upgrading…" screen (then restart) or open the window for everyone.
	go func() {
		select {
		case <-wailsReady:
		case <-wailsFailed:
			return
		case <-time.After(15 * time.Second):
			return
		}
		if base, newVer, ok := updateInfo(); ok {
			wailsApp.BeginUpgrade(newVer) // shows the upgrade screen + window
			time.Sleep(3 * time.Second)   // give the user a moment to read it
			applyUpdate(base)             // downloads, relaunches, and exits
			return
		}
		wailsApp.Show()
	}()

	// On first run, enable auto-start and record that we've done so.
	if !currentSettings.StartupConfigured {
		setAutoStart(true)
		currentSettings.StartupConfigured = true
		SaveSettings(currentSettings)
	}

	// Middlemand login proxy: start it (and point eqhost.txt at it) when
	// enabled; otherwise undo a leftover eqhost.txt redirect from a previous
	// run that didn't shut down cleanly.
	if currentSettings.UseMiddlemand {
		go SetMiddlemandEnabled(true)
	} else {
		restoreEqhost(currentSettings.EQDirectory)
	}
	// Keep the enabled state enforced (proxy up + eqhost.txt pointed at it),
	// healing external eqhost.txt resets and transient bind failures.
	go middlemandWatchdog()

	// Periodic update checks while running (every 6h); the initial check is above.
	startUpdateChecker()

	// Heartbeat so a running client shows as connected on the Clients tab even
	// when idle (EQ closed / minimized to tray).
	startHeartbeat()

	done := make(chan struct{})
	rawLines := make(chan string, 256)
	fwdLines := make(chan string, 256)

	// Start HTTP sender — reads filtered lines; updates tray icon on connect/disconnect
	sender := NewSender(serverURL, apiKey)
	sender.OnConnect = func() {
		setConnected(true)
		SetTrayConnected(true)
		addStatus("Connected to server.")
	}
	sender.OnDisconnect = func() {
		setConnected(false)
		SetTrayConnected(false)
		addStatus("Lost connection to server, retrying...")
	}
	go sender.Run(fwdLines, done)

	// Background: wait for EQ, then start tailing
	go func() {
		installDir := findEQInstallDir()
		addStatus("EverQuest found at: %s", installDir)
		go identifyClient(installDir)

		logPath := findActiveLogFile(installDir)
		if logPath == "" {
			addStatus("No EQ log file found. Enable logging in EverQuest: Options > General > Log.")
			SetTrayStatus("Relay active — no log file found")
		}

		tailLogFile(installDir, logPath, rawLines, done)
	}()

	// Filter: rawLines → ShouldForward → rewrite self-guild-say → fwdLines
	go func() {
		for {
			select {
			case line := <-rawLines:
				lastLogActivity = time.Now()
				RecordLoginLine(line)
				RecordRaidHPFromLine(line)
				if zone := ExtractZone(line); zone != "" {
					UpdateLocalZone(currentCharName, zone)
					SetCurrentZone(zone)
				}
				// A plain /who footer also reveals the player's current zone — useful
				// when logging in already inside a zone (no "You have entered" line).
				if zone := ExtractWhoZone(line); zone != "" {
					UpdateLocalZone(currentCharName, zone)
					SetCurrentZone(zone)
				}
				if y, x, z, ok := ExtractLoc(line); ok {
					UpdatePosition(x, y, z)
					if GetSettings().ShareMapPosition {
						sender.SendMapLoc(currentCharName, GetPosition())
					}
				}
				if ShouldForward(line) {
					line = rewriteSelfGuildSay(line)
					addStatus("Forwarded: %s", line)
					select {
					case fwdLines <- line:
					case <-done:
						return
					}
					// Engage alerts are time-critical — flush immediately rather than
					// waiting for the 2-second batch window.
					if engagePattern.MatchString(line) {
						select {
						case sender.FlushNow <- struct{}{}:
						default:
						}
					}
				}
			case <-done:
				return
			}
		}
	}()

	// Run tray on the main goroutine (walk requires this); blocks until Quit.
	// Settings click shows the Wails window; falls back to the walk dialog if
	// Wails failed to start within 15 seconds.
	runTray(func() {
		go func() {
			writeLog("Settings clicked, waiting for wailsReady...")
			select {
			case <-wailsReady:
				writeLog("wailsReady received, calling Show()")
				wailsApp.Show()
			case <-wailsFailed:
				writeLog("wailsFailed received, falling back to walk dialog")
				trayOwner.Synchronize(openSettingsWindow)
			case <-time.After(5 * time.Second):
				writeLog("timeout waiting for Wails, falling back to walk dialog")
				trayOwner.Synchronize(openSettingsWindow)
			}
		}()
	})

	close(done)

	// Quitting: stop the login proxy and put eqhost.txt back, or EQ logins
	// would silently fail while the bridge isn't running.
	if GetSettings().UseMiddlemand {
		SetMiddlemandEnabled(false)
	}
}
