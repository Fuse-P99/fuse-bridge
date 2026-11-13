package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Feature-usage audit. Each user action listed below becomes one row in the
// server's audit_records table (POST /audit → clientAudit.go), the same table
// the Discord slash commands write, so "who uses what" is one query for the
// bot and the app alike. Rows are named app_<verb><noun>_<detail>:
//
//	app_enabled_<setting> / app_disabled_<setting>  General tab toggles: every
//	                                                Settings bool saved from the
//	                                                tab (named by its json tag),
//	                                                plus auto_start,
//	                                                share_magelos, automation_*
//	app_poppedoverlay_<overlay>                     any Pop out button (map tab,
//	                                                Manage Timers, Manage
//	                                                Overlays all reach OpenPopout)
//	app_createdtimer_<name>                         a new trigger saved
//	app_updatedtimer_<what>                         enable/disable, mute,
//	                                                clipboard, customization —
//	                                                consolidated to once per
//	                                                hour per <what>
//	app_updatedmapsettings_<setting>_<value>        map tab + map overlay
//	                                                settings (frontend →
//	                                                AuditEvent)
//
// Quest adds/removes/completions, magelo create/edit/delete, and shares are
// audited SERVER-side inside the handlers that perform them (toonQuests.go,
// mageloHandler.go, shareHandler.go) — the client sends nothing extra for
// those.
//
// Every post is fire-and-forget on its own goroutine: an audit never delays
// or fails the action it describes. Auth is whatever authHeader() gives — a
// linked client is attributed to its member; an unlinked one (or an admin's
// "View as: Unlinked" preview) is recorded under the server's placeholder
// member. Off the home world nothing is sent, honoring the General tab's
// "nothing is sent to the server from this world"; before any log is attached
// (no world known yet) events still go.

func auditAllowed() bool {
	tok := getCurrentServerToken()
	return tok == "" || onHomeServer()
}

// auditEvent records one action. name carries the app_ prefix; opts is free
// text for the command_options column ("" is fine).
func auditEvent(name, opts string) {
	name = strings.TrimSpace(name)
	if name == "" || !auditAllowed() {
		return
	}
	go func() {
		body, _ := json.Marshal(map[string]string{"name": name, "options": opts})
		req, err := http.NewRequest(http.MethodPost, registerBase()+"/audit", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if h := authHeader(); h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()
}

var (
	auditHourlyMu   sync.Mutex
	auditHourlyLast = map[string]time.Time{}
)

const auditHourlyWindow = time.Hour

// auditEventHourly records name at most once per hour (per process). For the
// high-frequency timer settings the question is "is this feature in use",
// not "every flip".
func auditEventHourly(name, opts string) {
	now := time.Now()
	auditHourlyMu.Lock()
	if t, ok := auditHourlyLast[name]; ok && now.Sub(t) < auditHourlyWindow {
		auditHourlyMu.Unlock()
		return
	}
	auditHourlyLast[name] = now
	auditHourlyMu.Unlock()
	auditEvent(name, opts)
}

// auditToggle records app_enabled_<setting> or app_disabled_<setting>.
func auditToggle(setting string, on bool, opts string) {
	if on {
		auditEvent("app_enabled_"+setting, opts)
	} else {
		auditEvent("app_disabled_"+setting, opts)
	}
}

// auditTimerHourly is auditEventHourly with the name picked by a bool.
func auditTimerHourly(on bool, onName, offName string) {
	if on {
		auditEventHourly(onName, "")
	} else {
		auditEventHourly(offName, "")
	}
}

// auditSettingsDiff records every bool in Settings that changed between old
// and cur, named by its json tag (app_enabled_guild_chat, ...). Reflection
// keeps this in step with the struct — a new toggle audits itself.
func auditSettingsDiff(old, cur Settings) {
	ov, cv := reflect.ValueOf(old), reflect.ValueOf(cur)
	t := ov.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type.Kind() != reflect.Bool {
			continue
		}
		was, now := ov.Field(i).Bool(), cv.Field(i).Bool()
		if was == now {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" || tag == "startup_configured" {
			continue
		}
		auditToggle(tag, now, "")
	}
}

// auditAutomationsDiff records the General tab automation flags that flipped
// and a changed main-character pick.
func auditAutomationsDiff(prev, next AutomationSettings) {
	if prev.AddTracking != next.AddTracking {
		auditToggle("automation_add_tracking", next.AddTracking, "")
	}
	if prev.SwapBot != next.SwapBot {
		auditToggle("automation_swap_bot", next.SwapBot, "")
	}
	if prev.AddMissed != next.AddMissed {
		auditToggle("automation_add_missed", next.AddMissed, "")
	}
	if next.MainToon != "" && !strings.EqualFold(prev.MainToon, next.MainToon) {
		auditEvent("app_updated_automation_main_toon", next.MainToon)
	}
}

// AuditEvent is the frontend's hook for actions that live only in the UI
// (map settings). The name must carry the app_ prefix. Bound.
func (a *App) AuditEvent(name, options string) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "app_") {
		return
	}
	auditEvent(name, options)
}
