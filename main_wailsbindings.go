//go:build bindings

package main

// In Wails binding-generation mode (compiled with -tags bindings), main() must
// not block. wails.Run() with the bindings tag generates TypeScript bindings
// and returns immediately; then main() exits cleanly with code 0.
func main() {
	wailsApp = NewApp()
	startWails()
}
