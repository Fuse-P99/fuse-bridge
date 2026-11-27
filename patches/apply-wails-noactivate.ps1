# Re-applies the FuseBridge patches to the Wails module cache:
#   V2 -- "no-steal shows": overlays never take the game's foreground.
#   V3 -- show() fallback-timer guard: no nil-controller crash.
#
# WHY V2: Wails' windowsWebviewWindow.show() calls ShowWindow(hwnd, SW_SHOW),
# which is documented to ACTIVATE the window, and its setAlwaysOnTop calls
# SetWindowPos without SWP_NOACTIVATE, which also activates. Overlays are
# shown many times during a character login and every activation steals the
# game's foreground mid-race. V2 keys the fix off a REGISTRY of overlay HWNDs
# (application.FuseBridgeSetNoStealShow) rather than WS_EX_NOACTIVATE: the v1
# style approach also suppressed CLICK activation, which broke the OS move
# loop and WebView2 mouse capture (machine-wide input lockouts, Aug 2026).
#
# WHY V3: show() arms a 3-second fallback timer whenever a window is shown
# before its WebView2 navigation has completed. The timer's closure runs on a
# plain timer goroutine (no recover) and calls straight into the WebView2
# controller. A window whose controller never came up -- setupChromium bails
# when the WebView2 version lookup fails, which an in-progress Edge Update can
# cause -- has w.chromium set but no controller, so PutIsVisible dereferenced
# nil and took the whole process down three seconds after an overlay opened.
# A member's FuseBridge died this way on every character swap (crash log, Nov
# 2026). V3 checks IsReady() and the destroyed flag first and makes the call
# on the main thread, like every other controller call in the file.
#
# Idempotent and safe to run any time; upgrades a v1- or V2-patched file in
# place. build.bat calls the guard, not this -- run this yourself when the
# guard fails. See README.md in this folder for the full story.
#
# -FileOverride exists for testing the transform against a copy.

param([string]$FileOverride = '')

$ErrorActionPreference = 'Stop'

# Must match the wails/v3 version in eq-relay/go.mod (and the CLI pin in
# build.bat). A version bump intentionally makes this fail rather than
# silently patching a file nothing builds against.
$version = 'v3.0.0-alpha2.117'

if ($FileOverride) {
    $file = $FileOverride
} else {
    $modcache = (& go env GOMODCACHE 2>$null)
    if (-not $modcache) {
        Write-Host "ERROR: could not run 'go env GOMODCACHE' -- is Go on PATH?" -ForegroundColor Red
        exit 1
    }
    $modcache = $modcache.Trim()
    $file = Join-Path $modcache "github.com\wailsapp\wails\v3@$version\pkg\application\webview_window_windows.go"
}

if (-not (Test-Path -LiteralPath $file)) {
    Write-Host "ERROR: Wails source not found for $version" -ForegroundColor Red
    Write-Host "  Looked for: $file"
    Write-Host "  If you bumped the Wails version, update `$version at the top of this"
    Write-Host "  script and the pinned path in build.bat's patch guard, then re-run."
    exit 1
}

# Read as UTF-8 explicitly. Get-Content -Raw in Windows PowerShell 5.1 decodes
# with the system ANSI codepage, which turns the em-dashes in Wails' own
# comments into mojibake the moment we write the file back out.
$text = [System.IO.File]::ReadAllText($file, (New-Object System.Text.UTF8Encoding($false)))

$needV2 = -not ($text -match 'FUSEBRIDGE-PATCH-V2')
$needV3 = -not ($text -match 'FUSEBRIDGE-PATCH-V3')

if (-not $needV2 -and -not $needV3) {
    Write-Host "Already patched (V2 + V3): $file" -ForegroundColor Green
    exit 0
}

# Fails the run unless $needle occurs exactly once in $haystack: every hunk
# below anchors on stock (or V2) text, and a changed upstream must stop us
# rather than patch the wrong spot. `exit` inside a function ends the script.
function Assert-Once([string]$haystack, [string]$needle, [string]$label) {
    if (([regex]::Matches($haystack, [regex]::Escape($needle))).Count -ne 1) {
        Write-Host "ERROR: expected exactly one occurrence of the $label" -ForegroundColor Red
        Write-Host "  Upstream code has changed. Patch by hand and update this script."
        exit 1
    }
}

# The Go source is tab-indented, so every line below carries explicit `t
# escapes rather than literal whitespace that an editor might reformat.
# Do NOT build these with string concatenation: inside a comma-separated list
# PowerShell binds "," tighter than "+", so "`t" + 'code' becomes two array
# elements and the tab lands on its own line.
$showDecl = "func (w *windowsWebviewWindow) show() {"

# The V2 registry's helper; V3 inserts its marker right after it, so both the
# V2 hunk and the V3 hunk need the exact text.
$isNoSteal = @(
    "func fusebridgeIsNoSteal(hwnd uintptr) bool {",
    "`t_, ok := fusebridgeNoStealShows.Load(hwnd)",
    "`treturn ok",
    "}"
) -join "`n"

# ══════════════════════════ V2: no-steal shows ══════════════════════════
if ($needV2) {
    $anchor = "`tw32.ShowWindow(w.hwnd, w32.SW_SHOW)"

    # ── V1 upgrade: revert the old style-keyed show patch back to stock first ──
    $v1block = @(
        "`t//",
        "`t// FUSEBRIDGE-PATCH: no-activate show.",
        "`t// SW_SHOW is documented to ACTIVATE the window, and WS_EX_NOACTIVATE does",
        "`t// NOT prevent that: the style only governs IMPLICIT activation (a user",
        "`t// click, alt-tab, the app being raised). FuseBridge's game overlays carry",
        "`t// WS_EX_NOACTIVATE and are shown repeatedly while a character logs in",
        "`t// (once per window from navigationCompleted, then again from the app's",
        "`t// visibility sweep). Each SW_SHOW stole the foreground from EverQuest --",
        "`t// ~25 activations in 1.5s -- which breaks the game's mouselook capture",
        "`t// and eats keystrokes exactly when a racer cannot afford it.",
        "`t//",
        "`t// Honour the style with the show verb Windows provides for it. Windows",
        "`t// WITHOUT WS_EX_NOACTIVATE (the app's main window) are unaffected, so",
        "`t// normal activating behaviour is preserved everywhere else.",
        "`t//",
        "`t// Upstream as of v3.0.0-beta.10 still calls SW_SHOW unconditionally and",
        "`t// exposes no option for this, so re-apply after any Wails version bump.",
        "`t// See eq-relay/patches/README.md; build.bat fails the build without it.",
        "`tshowVerb := w32.SW_SHOW",
        "`tif w32.GetWindowLong(w.hwnd, w32.GWL_EXSTYLE)&w32.WS_EX_NOACTIVATE != 0 {",
        "`t`tshowVerb = w32.SW_SHOWNOACTIVATE",
        "`t}",
        "`tw32.ShowWindow(w.hwnd, showVerb)"
    ) -join "`n"

    if ($text.Contains($v1block)) {
        $text = $text.Replace($v1block, $anchor)
        Write-Host "Found v1 patch -- upgrading to V2."
    } elseif ($text -match 'FUSEBRIDGE-PATCH') {
        Write-Host "ERROR: file carries an unrecognized FUSEBRIDGE-PATCH state." -ForegroundColor Red
        Write-Host "  Neither stock, v1, nor V2. Restore the module (go clean -modcache or"
        Write-Host "  re-download) and re-run, or patch by hand per README.md."
        exit 1
    }

    # ── Sanity: the file must now look stock at all three patch sites ──
    $aotOld = @(
        "`tw32.SetWindowPos(w.hwnd,",
        "`t`thwndInsertAfter,",
        "`t`t0,",
        "`t`t0,",
        "`t`t0,",
        "`t`t0,",
        "`t`tuint(w32.SWP_NOMOVE|w32.SWP_NOSIZE))",
        "}"
    ) -join "`n"

    Assert-Once $text $anchor 'show anchor'
    Assert-Once $text $showDecl 'show declaration'
    Assert-Once $text $aotOld 'setAlwaysOnTop body'

    # ── Hunk 1: the registry, inserted immediately before show() ──
    $registry = @(
        "// FUSEBRIDGE-PATCH-V2: no-steal-show registry.",
        "//",
        "// HWNDs registered here are shown with SW_SHOWNOACTIVATE (show, below) and",
        "// re-Z-ordered with SWP_NOACTIVATE (setAlwaysOnTop, above), so no",
        "// programmatic show or raise ever takes the foreground from the game --",
        "// including this file's own async navigationCompleted re-show, which no",
        "// app-side bracketing can reach. The windows carry NO special styles:",
        "// unlike the WS_EX_NOACTIVATE approach (v1 of this patch), clicking and",
        "// dragging behave completely stock, which matters because a window that",
        "// refuses click activation breaks the OS modal move loop and WebView2's",
        "// mouse capture (machine-wide input lockouts, Aug 2026).",
        "//",
        "// FuseBridge registers each overlay right after creation and unregisters it",
        "// as it closes -- HWND values are recycled, and a stale entry would silence",
        "// an unrelated future window's shows.",
        "var fusebridgeNoStealShows sync.Map // hwnd (uintptr) -> struct{}",
        "",
        "// FuseBridgeSetNoStealShow marks or unmarks a window. Exported on purpose:",
        "// the FuseBridge app calls it directly, so a build against an UNPATCHED",
        "// module fails to compile instead of silently losing its focus protection.",
        "func FuseBridgeSetNoStealShow(hwnd uintptr, on bool) {",
        "`tif on {",
        "`t`tfusebridgeNoStealShows.Store(hwnd, struct{}{})",
        "`t} else {",
        "`t`tfusebridgeNoStealShows.Delete(hwnd)",
        "`t}",
        "}",
        "",
        $isNoSteal
    ) -join "`n"

    $text = $text.Replace($showDecl, ($registry + "`n`n" + $showDecl))

    # ── Hunk 2: non-activating show verb for registered windows ──
    $v2show = @(
        "`t//",
        "`t// FUSEBRIDGE-PATCH-V2: non-activating shows for registered overlays.",
        "`t// SW_SHOW is documented to ACTIVATE the window, and FuseBridge's overlays",
        "`t// are shown many times while a character logs in (once per window from",
        "`t// navigationCompleted, then again from the app's visibility sweeps) --",
        "`t// every activation steals the game's foreground exactly when a racer",
        "`t// cannot afford it. Windows registered in fusebridgeNoStealShows (above)",
        "`t// get the non-activating verb; everything else (the app's main window)",
        "`t// keeps stock behaviour. The WS_EX_NOACTIVATE check is kept from v1 for",
        "`t// completeness, but FuseBridge no longer applies that style anywhere --",
        "`t// a window that refuses CLICK activation wedges input machine-wide.",
        "`t//",
        "`t// Upstream as of v3.0.0-beta.10 still calls SW_SHOW unconditionally and",
        "`t// exposes no option for this, so re-apply after any Wails version bump.",
        "`t// See eq-relay/patches/README.md; build.bat fails the build without it.",
        "`tshowVerb := w32.SW_SHOW",
        "`tif fusebridgeIsNoSteal(w.hwnd) ||",
        "`t`tw32.GetWindowLong(w.hwnd, w32.GWL_EXSTYLE)&w32.WS_EX_NOACTIVATE != 0 {",
        "`t`tshowVerb = w32.SW_SHOWNOACTIVATE",
        "`t}",
        "`tw32.ShowWindow(w.hwnd, showVerb)"
    ) -join "`n"

    $text = $text.Replace($anchor, $v2show)

    # ── Hunk 3: non-activating Z-order changes for registered windows ──
    $aotNew = @(
        "`tflags := uint(w32.SWP_NOMOVE | w32.SWP_NOSIZE)",
        "`t// FUSEBRIDGE-PATCH-V2: raising or lowering a registered overlay in the",
        "`t// Z-order must not activate it -- SetWindowPos without SWP_NOACTIVATE",
        "`t// activates the window, and the overlay settings panel toggles",
        "`t// always-on-top during the login burst for users with a stored opt-out.",
        "`t// The main window keeps stock behaviour: its show-and-front path relies",
        "`t// on the activation.",
        "`tif fusebridgeIsNoSteal(w.hwnd) {",
        "`t`tflags |= w32.SWP_NOACTIVATE",
        "`t}",
        "`tw32.SetWindowPos(w.hwnd,",
        "`t`thwndInsertAfter,",
        "`t`t0,",
        "`t`t0,",
        "`t`t0,",
        "`t`t0,",
        "`t`tflags)",
        "}"
    ) -join "`n"

    $text = $text.Replace($aotOld, $aotNew)
    Write-Host "Applied V2 (no-steal shows)."
}

# ═══════════════════ V3: show() fallback-timer guard ═══════════════════
if ($needV3) {
    # Stock show(): the 3-second "show the WebView anyway" fallback, armed when
    # the window is shown before its first navigation completes.
    $timerOld = @(
        "`t`t`tw.visibilityTimeout = time.AfterFunc(3*time.Second, func() {",
        "`t`t`t`t// Show WebView even if navigation hasn't completed",
        "`t`t`t`t// This prevents permanent invisibility in efficiency mode",
        "`t`t`t`tif !w.webviewNavigationCompleted && w.chromium != nil {",
        "`t`t`t`t`tw.chromium.Show()",
        "`t`t`t`t}",
        "`t`t`t`tw.visibilityTimeout = nil",
        "`t`t`t})"
    ) -join "`n"

    Assert-Once $text $timerOld 'show() fallback timer'
    Assert-Once $text $isNoSteal 'fusebridgeIsNoSteal helper (V2 registry)'

    $timerNew = @(
        "`t`t`tw.visibilityTimeout = time.AfterFunc(3*time.Second, func() {",
        "`t`t`t`t// Show WebView even if navigation hasn't completed",
        "`t`t`t`t// This prevents permanent invisibility in efficiency mode",
        "`t`t`t`t//",
        "`t`t`t`t// FUSEBRIDGE-PATCH-V3: guard the controller and hop to the main thread.",
        "`t`t`t`t// This closure runs on a plain timer goroutine with no recover, and",
        "`t`t`t`t// stock code calls straight into the WebView2 controller. A window",
        "`t`t`t`t// whose controller never came up (setupChromium bails when the WebView2",
        "`t`t`t`t// version lookup fails, which an in-progress Edge Update can cause) has",
        "`t`t`t`t// w.chromium set but no controller, so PutIsVisible dereferenced nil and",
        "`t`t`t`t// took the whole process down three seconds after an overlay opened",
        "`t`t`t`t// (field crash log, Nov 2026: PutIsVisible <- Chromium.Show <-",
        "`t`t`t`t// show.func1 <- time.goFunc). IsReady is the module's own test for a",
        "`t`t`t`t// controller that is safe to call; a destroyed window is skipped too.",
        "`t`t`t`t// Every other controller call in this file is made on the main thread,",
        "`t`t`t`t// so this one is dispatched there as well.",
        "`t`t`t`tInvokeAsync(func() {",
        "`t`t`t`t`tif !w.webviewNavigationCompleted && w.chromium != nil &&",
        "`t`t`t`t`t`tw.chromium.IsReady() && !w.parent.isDestroyed() {",
        "`t`t`t`t`t`t_ = w.chromium.Show()",
        "`t`t`t`t`t}",
        "`t`t`t`t`tw.visibilityTimeout = nil",
        "`t`t`t`t})",
        "`t`t`t})"
    ) -join "`n"

    $text = $text.Replace($timerOld, $timerNew)

    # Marker symbol, right after the V2 registry helper. popouts.go references
    # it, so a module cache that carries V2 but not V3 is a compile error.
    $marker = @(
        "// FUSEBRIDGE-PATCH-V3 marker. Referenced from FuseBridge (popouts.go), so a",
        "// module that carries V2 but not the V3 show-fallback guard fails to compile",
        "// instead of shipping the nil-controller crash again.",
        "var FuseBridgePatchV3 struct{}"
    ) -join "`n"

    $text = $text.Replace($isNoSteal, ($isNoSteal + "`n`n" + $marker))
    Write-Host "Applied V3 (show fallback-timer guard)."
}

# Module cache files are read-only by design.
Set-ItemProperty -LiteralPath $file -Name IsReadOnly -Value $false

# UTF-8 without a BOM, matching how the module cache ships the file. A BOM
# here would be a gratuitous diff and some Go tooling dislikes it.
[System.IO.File]::WriteAllText($file, $text, (New-Object System.Text.UTF8Encoding($false)))

Write-Host "Patched (V2 + V3): $file" -ForegroundColor Green
Write-Host "Note: 'go mod verify' will now report this module as modified. That is expected."
