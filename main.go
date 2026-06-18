package main

import (
	"fmt"
	"path/filepath"
)

// serverURL and apiKey are embedded at build time via -ldflags.
// Example: go build -ldflags "-X main.serverURL=https://host:8765/submit -X main.apiKey=secret"
var (
	serverURL = "http://localhost:8765/submit"
	apiKey    = "dev-key"
)

func main() {
	fmt.Println("Fuse Bridgekeeper Relay")
	fmt.Println("Server:", serverURL)

	// Load saved settings (defaults to all-enabled on first run)
	currentSettings = LoadSettings()

	done := make(chan struct{})
	rawLines := make(chan string, 256)
	fwdLines := make(chan string, 256)

	// Start HTTP sender — reads filtered lines
	sender := NewSender(serverURL, apiKey)
	go sender.Run(fwdLines, done)

	// Background: wait for EQ, then start tailing
	go func() {
		installDir := findEQInstallDir(func(status string) {
			fmt.Println(status)
			SetTrayStatus(status)
		})
		fmt.Println("EverQuest found at:", installDir)

		logPath := findActiveLogFile(installDir)
		if logPath == "" {
			fmt.Println("No EQ log file found in", filepath.Join(installDir, "Logs"))
			fmt.Println("Make sure logging is enabled in EverQuest (Options > General > Log).")
		} else {
			fmt.Println("Following log:", filepath.Base(logPath))
			SetTrayStatus("Relay active — " + filepath.Base(logPath))
		}

		tailLogFile(installDir, logPath, rawLines, done, func(status string) {
			fmt.Println(status)
			SetTrayStatus(status)
		})
	}()

	// Filter: rawLines → ShouldForward → fwdLines
	go func() {
		for {
			select {
			case line := <-rawLines:
				if ShouldForward(line) {
					fmt.Println("Forwarded:", line)
					select {
					case fwdLines <- line:
					case <-done:
						return
					}
				}
			case <-done:
				return
			}
		}
	}()

	// Run tray on the main goroutine (walk requires this); blocks until Quit
	runTray(func() {
		openSettingsWindow()
	})

	close(done)
}
