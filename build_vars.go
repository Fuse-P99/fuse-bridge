package main

// serverURL, apiKey, and clientVersion are embedded at build time via -ldflags.
// Example: wails build -ldflags "-X main.serverURL=https://host/submit -X main.apiKey=secret -X main.clientVersion=1.0.0"
var (
	serverURL     = "http://localhost:8765/submit"
	apiKey        = "dev-key"
	clientVersion = "0.0.0"
)
