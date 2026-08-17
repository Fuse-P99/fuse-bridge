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
	LoadMainWindowGeom() // must precede runWails — it feeds the window options
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
	// The routine open never steals focus — launching the bridge and alt-tabbing
	// back to the game must leave the game in front when the window appears.
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
		showMainWindowNoSteal()
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

	// Poll who's talking in the guild voice channel for the footer indicator
	// (linked clients only — the endpoint needs a bearer token).
	startSpeakerPoller()

	// Keep the aggregated in-game clock (footer) in sync with the server.
	startGameClockSync()

	// Hide overlays while another app is the active window (opt-in setting).
	startFocusWatcher()

	// GINA-format triggers: load the imported set and run the timer ticker.
	startTriggerEngine()

	// Reminders on the server-wide timers board (boats, zone events, quakes).
	startWorldAlarmLoop()

	// Map tombstones for corpses left behind (expire on their own after 3h).
	LoadCorpses()

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

	// Per-character quest tracking: catalog cache, assignments, loot/inventory
	// detection, epic auto-assign, zone nudges. Works unlinked (local-first).
	go questTrackInit(done)

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
				RecordBoatLine(line)
				RecordBoatTrackLine(line)
				RecordCorpseLine(line)
				RecordQuestLootLine(line)
				RecordRandomLine(line)
				RecordThreatLine(line)
				sender.MaybeSendThreat()
				RecordRaidDPSLine(line)
				sender.MaybeSendRaidDPS()
				RecordRacingLine(line)
				ProcessTriggerLine(line)
				// Zone load: hold the character-state timers still until we're
				// in the new zone, since buffs don't tick during the handoff.
				if IsZoneLoadingLine(line) {
					NoteZoneLoading(time.Now())
					// Zoning wipes the player off every hate list behind them.
					// Coming back and resuming the same fight starts from zero
					// hate, so the ledger must not carry across the load.
					ThreatZoneReset()
					// The shared map marker belongs to the zone being left —
					// take it down now rather than leaving guildmates a
					// two-minute ghost of where we used to be.
					SendMapLocClear(currentCharName, "zoned")
					// Same for our own dot: its coordinates are still the old
					// zone's, and drawing them on the new zone's map puts us
					// somewhere we have never stood. No position beats a wrong
					// one, and the next /loc restores it.
					ClearPosition()
				}
				if zone := ExtractZone(line); zone != "" {
					zone = canonicalZone(zone)
					UpdateLocalZone(currentCharName, zone)
					SetCurrentZone(zone)
					if held, n := NoteZoneEntered(time.Now()); n > 0 {
						addStatus("Timers: held %d buff/discipline bar(s) for the %s zone load into %s",
							n, held.Round(100*time.Millisecond), zone)
					}
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
					// Reaching your own corpse retires its map marker.
					CorpseCheckLoc(currentCharName, x, y, z)
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
				} else if flush, isWho := whoGateFeed(line); isWho {
					// /who output forwards as complete LOCAL-zone blocks or not
					// at all (whogate.go). Consulted only for lines no other
					// filter claimed, so guild chat quoting who-like text still
					// forwards as guild chat above.
					for _, wl := range flush {
						addStatus("Forwarded: %s", wl)
						select {
						case fwdLines <- wl:
						case <-done:
							return
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

	// Save the auto-pause categories' timers so they survive the restart; the
	// periodic checkpoint covers crashes.
	PersistTriggerTimersNow()

	// Write out any window move/resize still sitting in the debounce.
	FlushMainWindowGeom()

	// Quitting: stop the login proxy and put eqhost.txt back, or EQ logins
	// would silently fail while the bridge isn't running.
	if GetSettings().UseMiddlemand {
		SetMiddlemandEnabled(false)
	}
}
