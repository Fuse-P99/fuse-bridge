# Local dependency patches

Two hunks, both applied to one file of the Wails v3 module in the Go module
cache by a single script. `build.bat` refuses to build without the file's
patch marker, and the app references a symbol from each hunk, so neither can
silently regress — if the guard or the compile fails, run:

```
patches\apply-wails-noactivate.bat
```

**File patched:** `<GOMODCACHE>\github.com\wailsapp\wails\v3@v3.0.0-alpha2.117\pkg\application\webview_window_windows.go`

| Hunk | Marker | Compile guard (referenced from `popouts.go`) | Fixes |
|---|---|---|---|
| V2 | `FUSEBRIDGE-PATCH-V2` | `application.FuseBridgeSetNoStealShow` | overlays stealing the game's focus |
| V3 | `FUSEBRIDGE-PATCH-V3` | `application.FuseBridgePatchV3` | process crash 3 s after an overlay opens |

---

## wails: no-steal shows (patch V2)

**Touched:** a new `fusebridgeNoStealShows` registry + exported
`FuseBridgeSetNoStealShow`, `(*windowsWebviewWindow).show()`, and
`(*windowsWebviewWindow).setAlwaysOnTop()`. Marker: `FUSEBRIDGE-PATCH-V2`
(contains `FUSEBRIDGE-PATCH`, so the existing build.bat guard still matches).

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

`ShowWindow(hwnd, SW_SHOW)` is documented to *activate* the window; Windows
provides `SW_SHOWNOACTIVATE` for exactly this case. Wails calls `SW_SHOW`
unconditionally and exposes no option to change it — verified against
**v3.0.0-beta.10** (2026-08-19), whose `show()` is unchanged. Nor can this be
fixed from our side: the first group above comes from Wails' own
`navigationCompleted`, which shows the window off unexported internal state.
`setAlwaysOnTop` has the same defect in miniature — `SetWindowPos` without
`SWP_NOACTIVATE` activates, and the overlay settings panel toggles
always-on-top during the login burst for users with a stored opt-out.

### Why V2 (history — read before "improving" this)

**V1 of this patch keyed off `WS_EX_NOACTIVATE`**, which the app then applied
to every overlay (v2.5.2469). That fixed the login burst but caused
**machine-wide input lockouts**: a window that refuses *click* activation
breaks the OS modal move loop (title-bar drags) and WebView2's own mouse
capture — every click on the machine swallowed until an alt-tab. Two repair
attempts (activation bracketing, manual dragging) both failed in the field;
the style was unwound wholesale in v2.5.3469.

The lesson: **non-activating SHOWS were always the safe half; suppressing
CLICK activation was the half that wedged.** V2 splits them. Never re-apply
`WS_EX_NOACTIVATE` as a standing style under WebView2.

### The patch

The patched file keeps a registry of overlay HWNDs:

```go
var fusebridgeNoStealShows sync.Map
func FuseBridgeSetNoStealShow(hwnd uintptr, on bool)
```

`popouts.go` registers each overlay right after creation and unregisters it in
its closing hook (HWND values are recycled). Registered windows get
`SW_SHOWNOACTIVATE` in `show()` and `SWP_NOACTIVATE` in `setAlwaysOnTop()` —
and are otherwise **bone-stock**: clicks activate, drags run the normal OS
move loop, WebView2 capture behaves. The app's main window is never
registered and keeps fully stock behaviour (tray, single-instance re-launch,
`BeginUpgrade`, its always-on-top front trick).

`FuseBridgeSetNoStealShow` is exported *deliberately*: the app calls it
directly, so building against an unpatched module is a **compile error**, not
a silent regression — a second guard behind build.bat's `findstr`.

---

## wails: show() fallback-timer guard (patch V3)

**Touched:** the 3-second `time.AfterFunc` fallback inside
`(*windowsWebviewWindow).show()`, plus an exported marker `FuseBridgePatchV3`
placed after the V2 registry. Marker: `FUSEBRIDGE-PATCH-V3`.

### The bug

A member's FuseBridge died on character swaps, roughly three seconds after the
incoming character's overlays opened. `%TEMP%\FuseBridge-crash.log` (2026-11-25):

```
panic: runtime error: invalid memory address or nil pointer dereference
goroutine 450 [running]:
  edge.(*ICoreWebView2Controller).PutIsVisible(0x0, ...)      ICoreWebView2Controller.go:119
  edge.(*Chromium).Show(...)                                   chromium.go:319
  application.(*windowsWebviewWindow).show.func1()              webview_window_windows.go:1360
created by time.goFunc
```

Stock `show()` shows the HWND at once and, if the WebView2 navigation has not
completed yet, arms a 3-second timer that calls `w.chromium.Show()` anyway
(upstream's fix for Windows efficiency mode leaving windows invisible). The
closure checks `w.chromium != nil` but not the *controller* inside it, and it
runs on a bare timer goroutine — none of Wails' `handlePanic` wrappers cover
it, so the nil dereference is an unrecovered panic that ends the process.

How the controller comes to be nil: `setupChromium()` returns early when the
WebView2 version lookup (`GetAvailableCoreWebView2BrowserVersionString`)
errors. The window is created, shown and registered, but `Embed()` never runs,
so `w.chromium` exists with no controller for the window's whole life and
navigation never completes. The lookup reads the Edge Update registry key and
the runtime's `EmbeddedBrowserWebView.dll`; a PC where Edge Update is mid-way
through replacing the WebView2 runtime (or stuck retrying a failed install)
makes that fail intermittently — which is why one member hit it on most swaps
and the developer's PC never did. Upstream already guards this exact state in
`hide()` and `focus()`; `show()`'s timer was the one call site they missed.

### The patch

The closure body becomes

```go
InvokeAsync(func() {
	if !w.webviewNavigationCompleted && w.chromium != nil &&
		w.chromium.IsReady() && !w.parent.isDestroyed() {
		_ = w.chromium.Show()
	}
	w.visibilityTimeout = nil
})
```

`IsReady()` is the webview2 module's own "safe to call into the controller"
test (a non-nil controller is not sufficient during setup). The destroyed
check covers an overlay closed by a character swap inside the 3-second window.
`InvokeAsync` moves the call to the main thread, where every other controller
call in the file is made, and its `handlePanic` wrapper would turn any future
surprise into a logged `wails error:` instead of a silent death.

A window that *does* lose its controller this way still comes up as an empty,
transparent overlay until it is next recreated. With the patch that is a
cosmetic gap rather than a crash; the launch log carries the cause as
`wails error: error getting WebView2 version: …`.

`FuseBridgePatchV3` is referenced from `popouts.go` (`var _ =
application.FuseBridgePatchV3`), so a module cache carrying V2 but not V3 is a
compile error — build.bat's `findstr FUSEBRIDGE-PATCH` alone cannot tell the
two apart.

---

## Maintenance

- **After any Wails version bump**: update `$version` in
  `apply-wails-noactivate.ps1` *and* the pinned path in `build.bat`'s guard,
  then re-run the script. The guard fails loudly until you do. Re-check both
  anchors against the new upstream `show()`: the script refuses to run unless
  each anchor occurs exactly once.
- The script is idempotent, **upgrades a v1-patched file in place**, and adds
  V3 to a file that already carries V2; an unrecognized patch state fails
  loudly (restore the module and re-run). It was verified against a pristine
  copy (gains V2 + V3) and a V2-only copy (gains V3); both results are
  byte-identical and `gofmt`-clean.
- **`go mod verify` will report the module as modified.** That is expected and
  is the cost of the patch; it is not run by the build.
- **A cleared module cache drops both hunks.** `build.bat` catches this before
  compiling — and the compile itself fails too (`FuseBridgeSetNoStealShow` and
  `FuseBridgePatchV3` undefined) — so the worst case is a failed build, never a
  shipped regression.
- If upstream ever fixes either defect, delete that hunk from the script, its
  section here, and its compile-guard reference in `popouts.go`; when both are
  gone, also drop the guard block in `build.bat` and this folder.
