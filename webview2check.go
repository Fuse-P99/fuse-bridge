package main

// WebView2 preflight. Fuse Bridge draws every window with Microsoft Edge
// WebView2, and when that runtime is missing or half-installed the window
// layer dies without a word: Wails treats it as fatal and exits, Windows
// records no crash (a clean exit), and the launch log ends at "runWails()
// called". A member's client did exactly that at every launch for a week
// (2026-10-07); the only trace anywhere was an Edge Update "InstallError
// webview" event, because the runtime's own installer was failing on that PC.
//
// So before the window layer runs, look at the runtime the way the WebView2
// loader itself does — the Edge Update client registration and the browser
// binary that registration points at — write what was found to the launch log
// and the Status window, and when nothing usable is there say so in a plain
// message with the download page instead of vanishing.
//
// Detection follows Microsoft's published guidance ("Detect if a suitable
// WebView2 Runtime is already installed"): the Evergreen runtime registers
// under EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5} with a pv
// (version) value, per machine or per user. Only Evergreen is checked; this
// client ships no fixed-version runtime.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	webview2ClientsKey  = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	webview2DownloadURL = "https://developer.microsoft.com/microsoft-edge/webview2/"
)

// webview2Install is one registered Evergreen runtime: where it is registered,
// its version, and the browser executable that registration implies.
type webview2Install struct {
	scope   string // "machine" or "user"
	version string
	exe     string
}

// webview2Installs reads the Evergreen registrations. Edge Update is a 32-bit
// client, so on a 64-bit OS the per-machine key lives under WOW6432Node;
// asking for the 32-bit view explicitly finds it on either OS width. The
// per-user key has no such redirection.
func webview2Installs() []webview2Install {
	readPV := func(root registry.Key, access uint32) string {
		k, err := registry.OpenKey(root, `SOFTWARE\`+webview2ClientsKey, registry.QUERY_VALUE|access)
		if err != nil {
			return ""
		}
		defer k.Close()
		pv, _, err := k.GetStringValue("pv")
		if err != nil {
			return ""
		}
		// Edge Update writes 0.0.0.0 for a product it knows about but has not
		// (or no longer has) installed.
		if pv = strings.TrimSpace(pv); pv == "" || pv == "0.0.0.0" {
			return ""
		}
		return pv
	}
	var out []webview2Install
	if pv := readPV(registry.LOCAL_MACHINE, registry.WOW64_32KEY); pv != "" {
		base := os.Getenv("ProgramFiles(x86)")
		if base == "" {
			base = os.Getenv("ProgramFiles")
		}
		out = append(out, webview2Install{scope: "machine", version: pv,
			exe: filepath.Join(base, "Microsoft", "EdgeWebView", "Application", pv, "msedgewebview2.exe")})
	}
	if pv := readPV(registry.CURRENT_USER, 0); pv != "" {
		out = append(out, webview2Install{scope: "user", version: pv,
			exe: filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "EdgeWebView", "Application", pv, "msedgewebview2.exe")})
	}
	return out
}

// logEdgeUpdateHealth journals the two things that stop the WebView2 runtime
// from installing or repairing itself: Edge Update policies that block it, and
// the Edge Update services being disabled or gone. Both are the fingerprint of
// a "remove Edge" debloat script, the likeliest reason an Evergreen install
// fails on a gaming PC. Read-only; nothing is changed. Silent when healthy.
func logEdgeUpdateHealth() {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\EdgeUpdate`, registry.QUERY_VALUE); err == nil {
		var found []string
		for _, name := range []string{"UpdateDefault", "InstallDefault",
			"Update{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}", "Install{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"} {
			if v, _, err := k.GetIntegerValue(name); err == nil {
				found = append(found, fmt.Sprintf("%s=%d", name, v))
			}
		}
		k.Close()
		if len(found) > 0 {
			writeLog("webview2 runtime: Edge Update policies set (0 blocks installs/updates): " + strings.Join(found, " "))
		}
	}
	for _, svc := range []string{"edgeupdate", "edgeupdatem"} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+svc, registry.QUERY_VALUE)
		if err != nil {
			writeLog("webview2 runtime: the " + svc + " service is not installed")
			continue
		}
		start, _, err := k.GetIntegerValue("Start")
		k.Close()
		if err == nil && start == 4 {
			writeLog("webview2 runtime: the " + svc + " service is disabled")
		}
	}
}

// preflightWebView2 records what the WebView2 runtime looks like on this
// machine and, when nothing usable is registered, offers the download page. It
// never refuses a launch on its own: Cancel continues exactly as before,
// because this reads the registration rather than exercising the runtime, and
// a working install in an unusual place must not be turned away by a
// heuristic. OK opens Microsoft's page and exits, since the window layer would
// only die silently a moment later.
func preflightWebView2() {
	var usable, broken []string
	for _, in := range webview2Installs() {
		if _, err := os.Stat(in.exe); err == nil {
			usable = append(usable, fmt.Sprintf("%s v%s", in.scope, in.version))
		} else {
			broken = append(broken, fmt.Sprintf("%s v%s is registered but %s is missing", in.scope, in.version, in.exe))
		}
	}
	logEdgeUpdateHealth()
	if len(usable) > 0 {
		line := "WebView2 runtime: " + strings.Join(usable, ", ")
		if len(broken) > 0 {
			line += " (also " + strings.Join(broken, "; ") + ")"
		}
		writeLog(line)
		addStatus("%s", line)
		return
	}
	why := "no Evergreen runtime is registered for this machine or this user"
	if len(broken) > 0 {
		why = strings.Join(broken, "; ")
	}
	writeLog("WebView2 runtime: UNUSABLE — " + why)
	addStatus("WebView2 runtime unusable: %s", why)

	text := "Fuse Bridge needs the Microsoft Edge WebView2 Runtime, and Windows does not have a working copy:\n\n" +
		why + "\n\n" +
		"Click OK to open Microsoft's download page (use the Evergreen Standalone Installer, x64) and close Fuse Bridge, " +
		"or Cancel to try starting anyway.\n\nDetails: " + logPath
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Fuse Bridge cannot start")
	ret, _ := windows.MessageBox(0, t, c, windows.MB_OKCANCEL|windows.MB_ICONERROR|windows.MB_SETFOREGROUND|windows.MB_TOPMOST)
	const idOK = 1 // MessageBox's IDOK; x/sys defines the MB_ flags but not the return ids
	if ret == idOK {
		verb, _ := windows.UTF16PtrFromString("open")
		u, _ := windows.UTF16PtrFromString(webview2DownloadURL)
		windows.ShellExecute(0, verb, u, nil, nil, windows.SW_SHOWNORMAL)
		writeLog("WebView2 runtime: user chose to install — exiting")
		os.Exit(1)
	}
	writeLog("WebView2 runtime: user chose to continue anyway")
}
