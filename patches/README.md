# Local dependency patches

One patch, applied to the Wails v3 module in the Go module cache. `build.bat`
refuses to build without it, so it can never silently regress — if the guard
fails, run:

```
patches\apply-wails-noactivate.bat
```

---

## wails: no-activate show

**File patched:** `<GOMODCACHE>\github.com\wailsapp\wails\v3@v3.0.0-alpha2.117\pkg\application\webview_window_windows.go`
**Function:** `(*windowsWebviewWindow).show()`

### The bug

Overlay windows stole keyboard focus from EverQuest every time a character
logged in. A player would log in, start strafe-running, and lose control of
the game for a second or two as the overlays came up — fatal during a race,
where the rules require logging in from character select and immediately
running.

The client log (`%TEMP%\FuseBridge.log`) showed **~25 `overlay activated:`
lines in 1.5 seconds**, in two distinct groups:

1. One per overlay, ~150 ms apart — WebView2's `navigationCompleted` handler
   showing each window as its content finishes loading.
2. Sixteen in ~390 ms — the app's visibility sweep when the camp-out hide
   latch releases on the first log line of the new session.

### Why our own fixes weren't enough

`eq-relay/popouts_noactivate.go` puts `WS_EX_NOACTIVATE` on every overlay
window before it is ever shown. That was necessary but not sufficient: the
style only governs **implicit** activation — a user clicking the window,
alt-tab, the app being raised. `ShowWindow(hwnd, SW_SHOW)` is documented to
*activate* the window, and it does so regardless of the style. Windows
provides `SW_SHOWNOACTIVATE` for exactly this case.

Wails calls `SW_SHOW` unconditionally and exposes no option to change it.
Neither does upstream: verified against **v3.0.0-beta.10** (2026-08-19), whose
`show()` is unchanged and which still has no `NoActivate`/`Focusable` window
option. The constants `SW_SHOWNOACTIVATE` and `SW_SHOWNA` exist in Wails'
`pkg/w32` and are referenced nowhere in the library.

Nor can this be fixed from our side. The show calls we make ourselves could be
replaced with `SetWindowPos(..., SWP_NOACTIVATE)`, but the first group above
comes from Wails' own `navigationCompleted`, which shows the window whenever
its internal `showRequested && !windowShown` — unexported state we cannot set
without calling the very `Show()` that activates.

### The patch

```go
showVerb := w32.SW_SHOW
if w32.GetWindowLong(w.hwnd, w32.GWL_EXSTYLE)&w32.WS_EX_NOACTIVATE != 0 {
    showVerb = w32.SW_SHOWNOACTIVATE
}
w32.ShowWindow(w.hwnd, showVerb)
```

It keys off the style **we** set, so it changes behaviour only for windows that
asked for it. The app's main window carries no such style and still activates
normally when shown, so the tray, the single-instance re-launch, and
`BeginUpgrade` are all unaffected.

### Maintenance

- **After any Wails version bump**: update `$version` in
  `apply-wails-noactivate.ps1` *and* the pinned path in `build.bat`'s guard,
  then re-run the script. The guard fails loudly until you do.
- **`go mod verify` will report the module as modified.** That is expected and
  is the cost of the patch; it is not run by the build.
- **A cleared module cache drops the patch.** `build.bat` catches this before
  compiling, so the worst case is a failed build, never a shipped regression.
- If upstream ever adds a real option for this (or accepts a PR), delete the
  patch, this folder, and the guard block in `build.bat`.
