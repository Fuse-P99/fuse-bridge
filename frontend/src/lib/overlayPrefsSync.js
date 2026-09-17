// Pushes every overlay look/setting entry the browser store holds into Go
// (SetOverlayPrefs) once at main-window startup, so the daily settings
// snapshot (settings_snapshot.go) reflects what is configured even for
// overlays that haven't been opened since the app started — and for every
// character, not just the one whose overlay mounted last. Popout.svelte pushes
// its own entry on mount and on every save; this covers the rest. SETTINGS
// TELEMETRY (CLAUDE.md): a new per-kind key needs nothing here, Go whitelists.
//
// Store keys (Popout.svelte BASEKEY/KEY): "fuse.popout.<kind>" for the map
// and the special overlays, "fuse.popout.<kind>:<category>" for timer/alert
// overlays, each with an "@<char>" suffix when per-character. Character
// names are letters only, so the last "@" is the split; a category may hold
// anything but the kind never holds ":", so the first ":" is.
import { SetOverlayPrefs } from "../../bindings/FuseBridge/app.js";

const PREFIX = "fuse.popout.";

export function pushStoredOverlayPrefs() {
  const keys = [];
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k && k.startsWith(PREFIX)) keys.push(k);
    }
  } catch {
    return; // storage blocked — nothing to mirror
  }
  for (const k of keys) {
    let settings;
    try {
      const raw = JSON.parse(localStorage.getItem(k) || "{}");
      settings = raw && raw.settings;
    } catch {
      continue;
    }
    if (!settings || typeof settings !== "object") continue;
    let rest = k.slice(PREFIX.length);
    let char = "";
    const at = rest.lastIndexOf("@");
    if (at >= 0) {
      char = rest.slice(at + 1);
      rest = rest.slice(0, at);
    }
    const colon = rest.indexOf(":");
    const kind = colon >= 0 ? rest.slice(0, colon) : rest;
    const category = colon >= 0 ? rest.slice(colon + 1) : "";
    if (!kind) continue;
    const { bg, ...prefs } = settings; // colors never travel
    SetOverlayPrefs(kind, category, JSON.stringify(prefs), char).catch(
      () => {},
    );
  }
}
