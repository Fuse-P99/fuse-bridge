package main

// serverURL and clientVersion are embedded at build time via -ldflags.
// Example: wails build -ldflags "-X main.serverURL=https://host/submit -X main.clientVersion=1.0.0"
// There is no built-in credential: all authenticated calls use the per-client
// token minted by the Discord link flow, and the endpoints an unlinked client
// needs (/version, /client, /register/*) are unauthenticated.
var (
	serverURL     = "http://localhost:8765/submit"
	clientVersion = "0.0.0"
)
