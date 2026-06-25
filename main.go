//go:build !bindings

package main

import "time"

func main() {
	// Give Windows time to finish loading the shell and notification area
	// before we try to create a tray icon.
	time.Sleep(5 * time.Second)

	currentSettings = LoadSettings()
	LoadZones()
	loadFilteredToons()
	LoadRaidMobs()
	go fetchBotToons()

	wailsApp = NewApp()
	go startWails()

	// On first run, or when migrating from the old registry-based approach,
	// enable auto-start and record that we've done it.
	if !currentSettings.StartupConfigured || hasLegacyRegistryAutoStart() {
		setAutoStart(true)
		currentSettings.StartupConfigured = true
		SaveSettings(currentSettings)
	}

	startUpdateChecker()

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
				if zone := ExtractZone(line); zone != "" {
					UpdateLocalZone(currentCharName, zone)
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
			select {
			case <-wailsReady:
				wailsApp.Show()
			case <-wailsFailed:
				trayOwner.Synchronize(openSettingsWindow)
			case <-time.After(15 * time.Second):
				trayOwner.Synchronize(openSettingsWindow)
			}
		}()
	})

	close(done)
}
