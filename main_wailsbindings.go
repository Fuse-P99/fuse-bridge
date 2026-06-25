//go:build wailsbindings

package main

// In Wails binding-generation mode, main() must not block. wails.Run() with
// the wailsbindings tag prints type info and calls os.Exit(0) immediately.
func main() {
	wailsApp = NewApp()
	startWails()
}
