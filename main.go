package main

import (
	"time"
)

func main() {

	// Bail out immediately (before the shell-settle sleep) if another
	// instance is already running — it gets asked to show its window.
	exitIfAlreadyRunning()

	// Give Windows time to finish loading the shell and notification area
	// before we try to create a tray icon.
	time.Sleep(5 * time.Second)

	writeLog("FuseBridge starting, clientVersion=" + clientVersion)
	// Surface (and clear) the debris of an update whose binary swap never
	// completed — otherwise "the update ran but I'm still on the old version"
	// is invisible.
	cleanupFailedUpdate()
	currentSettings = LoadSettings()
	LoadPopoutStore()
	LoadZones()
	LoadBinds()
	LoadCharCache()
	loadFilteredToons()
	LoadRaidMobs()
	go fetchBotToons()

	wailsApp = NewApp()

	//Get the ini settings
	iniSettings := wailsApp.GetIniSettings()

	for _, setting := range iniSettings {
		if setting == "BadWord=0" {
			badWordFilter = true
		}
	}

	if badWordFilter {
		addStatus("Bad word filter detected.")
	}

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
			// On success this exits for the restart; on failure back out of
			// the upgrade screen so the user isn't stuck on the spinner.
			if err := applyUpdate(base); err != nil {
				addStatus("Update failed: %v", err)
				writeLog("startup update failed: " + err.Error())
				wailsApp.EndUpgrade()
			}
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

	// Periodic log archival (gated on a fully-quiet period, like the updater).
	startLogArchiver()

	// Heartbeat so a running client shows as connected on the Clients tab even
	// when idle (EQ closed / minimized to tray).
	startHeartbeat()

	// Register the anonymous share identity and poll the share inbox (user-to-
	// user trigger/marker sharing — works for unlinked clients too).
	startSharePoller()

	// Keep the aggregated in-game clock (footer) in sync with the server.
	startGameClockSync()

	// GINA-format triggers: load the imported set and run the timer ticker.
	startTriggerEngine()

	done := make(chan struct{})
	rawLines := make(chan string, 256)
	fwdLines := make(chan string, 256)

	// Start HTTP sender — reads filtered lines; updates tray icon on connect/disconnect
	sender := NewSender(serverURL)
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

		// Watch dbg.txt for the /q quit line so overlays hide immediately on a
		// clean quit (camp-out is caught by the eqlog camp countdown instead).
		go tailDbgLog(installDir, done)

		tailLogFile(installDir, logPath, rawLines, done)
	}()

	// Filter: rawLines → ShouldForward → rewrite self-guild-say → fwdLines
	go func() {
		for {
			select {
			case line := <-rawLines:
				lastLogActivity = time.Now()
				// A line means the tailed log is being written now: open any timer
				// overlays whose restore was deferred at startup.
				maybeApplyDeferredPopouts()
				RecordLoginLine(line)
				RecordRaidHPFromLine(line)
				RecordRaidTimerLine(line)
				RecordGameTimeLine(line)
				ProcessTriggerLine(line)
				if zone := ExtractZone(line); zone != "" {
					zone = canonicalZone(zone)
					UpdateLocalZone(currentCharName, zone)
					SetCurrentZone(zone)
				}
				// A plain /who footer also reveals the player's current zone — useful
				// when logging in already inside a zone (no "You have entered" line).
				if zone := ExtractWhoZone(line); zone != "" {
					zone = canonicalZone(zone)
					UpdateLocalZone(currentCharName, zone)
					SetCurrentZone(zone)
				}
				// "/char" reports the character's bind point. Tracked locally (for the
				// Characters tab) regardless of the forwarding toggle, like the zone.
				if bind := ExtractBind(line); bind != "" {
					UpdateLocalBind(currentCharName, canonicalZone(bind))
				}
				if y, x, z, ok := ExtractLoc(line); ok {
					UpdatePosition(x, y, z)
					// Spamming /loc (5 within 5s) auto-switches the UI to the Map tab.
					noteLocForAutoMap()
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

	// Run the Wails v3 app (window + tray) on the main goroutine; blocks until
	// the tray's Quit is clicked or a termination signal arrives.
	runWails()

	close(done)

	// Save preserved timers (Buffs (Self)/Disciplines) so they survive the
	// restart; the periodic checkpoint covers crashes.
	PersistTriggerTimersNow()

	// Quitting: stop the login proxy and put eqhost.txt back, or EQ logins
	// would silently fail while the bridge isn't running.
	if GetSettings().UseMiddlemand {
		SetMiddlemandEnabled(false)
	}
}
