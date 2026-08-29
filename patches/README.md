# Local dependency patches

One patch, applied to the Wails v3 module in the Go module cache. `build.bat`
refuses to build without it, so it can never silently regress — if the guard
fails, run:

```
patches\apply-wails-noactivate.bat
```

---

## wails: no-steal shows (patch V2)

**File patched:** `<GOMODCACHE>\github.com\wailsapp\wails\v3@v3.0.0-alpha2.117\pkg\application\webview_window_windows.go`
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

### Maintenance

- **After any Wails version bump**: update `$version` in
  `apply-wails-noactivate.ps1` *and* the pinned path in `build.bat`'s guard,
  then re-run the script. The guard fails loudly until you do.
- The script is idempotent and **upgrades a v1-patched file in place**; an
  unrecognized patch state fails loudly (restore the module and re-run).
- **`go mod verify` will report the module as modified.** That is expected and
  is the cost of the patch; it is not run by the build.
- **A cleared module cache drops the patch.** `build.bat` catches this before
  compiling — and the compile itself now fails too (`FuseBridgeSetNoStealShow`
  undefined) — so the worst case is a failed build, never a shipped
  regression.
- If upstream ever adds a real option for this (or accepts a PR), delete the
  patch, this folder, the guard block in `build.bat`, and the
  `FuseBridgeSetNoStealShow` calls in `popouts.go`.
