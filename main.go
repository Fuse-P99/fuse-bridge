package main

import "time"

// serverURL, apiKey, and clientVersion are embedded at build time via -ldflags.
// Example: go build -ldflags "-H windowsgui -X main.serverURL=https://host/submit -X main.apiKey=secret -X main.clientVersion=1.0.0"
var (
	serverURL     = "http://localhost:8765/submit"
	apiKey        = "dev-key"
	clientVersion = "0.0.0"
)

func main() {
	// Give Windows time to finish loading the shell and notification area
	// before we try to create a tray icon.
	time.Sleep(5 * time.Second)

	currentSettings = LoadSettings()
	LoadZones()
	loadFilteredToons()
	go fetchBotToons()

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

	// Run tray on the main goroutine (walk requires this); blocks until Quit
	runTray(openSettingsWindow)

	close(done)
}
