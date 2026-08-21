# Re-applies the FuseBridge "no-activate show" patch to the Wails module cache.
#
# WHY: Wails' windowsWebviewWindow.show() calls ShowWindow(hwnd, SW_SHOW),
# which is documented to ACTIVATE the window. WS_EX_NOACTIVATE does not stop
# that -- it only governs implicit activation (clicks, alt-tab, app raise).
# Our overlays are shown many times during a character login, and every one of
# those stole the foreground from EverQuest, breaking mouselook mid-race.
#
# Idempotent and safe to run any time. build.bat calls the guard, not this --
# run this yourself when the guard fails.
#
# See README.md in this folder for the full story.

$ErrorActionPreference = 'Stop'

# Must match the wails/v3 version in eq-relay/go.mod (and the CLI pin in
# build.bat). A version bump intentionally makes this fail rather than
# silently patching a file nothing builds against.
$version = 'v3.0.0-alpha2.117'

$modcache = (& go env GOMODCACHE 2>$null)
if (-not $modcache) {
    Write-Host "ERROR: could not run 'go env GOMODCACHE' -- is Go on PATH?" -ForegroundColor Red
    exit 1
}
$modcache = $modcache.Trim()

$file = Join-Path $modcache "github.com\wailsapp\wails\v3@$version\pkg\application\webview_window_windows.go"

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
if ($text -match 'FUSEBRIDGE-PATCH') {
    Write-Host "Already patched: $file" -ForegroundColor Green
    exit 0
}

# The Go source is tab-indented, so every line below carries explicit `t
# escapes rather than literal whitespace that an editor might reformat.
# Do NOT build these with string concatenation: inside a comma-separated list
# PowerShell binds "," tighter than "+", so "`t" + 'code' becomes two array
# elements and the tab lands on its own line.
$anchor = "`tw32.ShowWindow(w.hwnd, w32.SW_SHOW)"

if (([regex]::Matches($text, [regex]::Escape($anchor))).Count -ne 1) {
    Write-Host "ERROR: expected exactly one occurrence of the anchor line:" -ForegroundColor Red
    Write-Host "  $anchor"
    Write-Host "  Upstream code has changed. Patch by hand and update this script."
    exit 1
}

$replacement = @(
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
) -join "`n"   # LF: the module cache ships this file with Unix line endings

# Module cache files are read-only by design.
Set-ItemProperty -LiteralPath $file -Name IsReadOnly -Value $false

$patched = $text.Replace($anchor, $replacement)
# UTF-8 without a BOM, matching how the module cache ships the file. A BOM
# here would be a gratuitous diff and some Go tooling dislikes it.
[System.IO.File]::WriteAllText($file, $patched, (New-Object System.Text.UTF8Encoding($false)))

Write-Host "Patched: $file" -ForegroundColor Green
Write-Host "Note: 'go mod verify' will now report this module as modified. That is expected."
