<script>
  import { onMount, onDestroy, tick } from "svelte";
  import { fade, slide } from "svelte/transition";
  import { Events } from "@wailsio/runtime";
  import {
    GetTriggerState,
    GetTriggersMeta,
    SyncTriggers,
    SaveTrigger,
    CreateTrigger,
    DeleteTrigger,
    CreateTriggerGroup,
    RenameTriggerGroup,
    DeleteTriggerGroup,
    SetTriggerGroupEnabledFor,
    SetTriggerEnabledFor,
    GetTriggerTreeFor,
    GetTriggerCharacters,
    GetTriggerCategories,
    GetCategoryNames,
    GetOpenPopouts,
    SetTriggerGroupMuted,
    SetTriggerMuted,
    SetTriggerGroupClipboardBlocked,
    SetTriggerClipboardBlocked,
    BeginTriggerDefaults,
    CancelTriggerDefaults,
    SaveTriggerDefaults,
    CreateTriggerCategory,
    SaveTriggerCategory,
    DeleteTriggerCategory,
    OpenPopout,
    ClosePopout,
    GetPopoutSticky,
    SetPopoutSticky,
    DismissTimer,
    DismissTimerCategory,
    SetPopoutsHidden,
    SetAllPopoutsLocked,
    ArePopoutsManuallyHidden,
    ArePopoutsLocked,
    GetOverlayTitleMode,
    SetOverlayTitleMode,
    GetSnapToGrid,
    SetSnapToGrid,
    GetHideOverlaysUnfocused,
    SetHideOverlaysUnfocused,
    GetOverlayDefaultInfo,
    SetOverlayDefaultEditing,
    SaveOverlayDefaultFromCharacter,
    ResetCharacterToDefault,
    GetTriggerMediaFiles,
    AddTriggerMediaFile,
    PlayTriggerMediaSample,
    GetWorldTimers,
    GetWorldAlarms,
    GetGameClock,
    DefaultGINAConfigPath,
    BrowseGINAConfig,
    ScanGINAConfig,
    ImportGINAGroups,
    PublishFuseTriggers,
    RevertFuseTriggers,
    ExportFuseTriggers,
    BrowseFuseTriggersXML,
    PreviewFuseTriggersImport,
    ImportFuseTriggersXML,
    ShareTrigger,
  } from "../../bindings/FuseBridge/app.js";
  import TriggerNode from "../lib/TriggerNode.svelte";
  import AlarmBell from "../lib/AlarmBell.svelte";
  import AlarmDialog from "../lib/AlarmDialog.svelte";
  import ShareDialog from "../lib/ShareDialog.svelte";
  import TriggerActions from "../lib/TriggerActions.svelte";
  import { scale } from "../lib/scale.js";
  import { catColor, rgba } from "../lib/catColor.js";
  import { confirmDialog } from "../lib/confirm.js";

  // Sub-tabs, in setup order: pick your timers, lay out your overlays, watch
  // them run. Manage Timers is both the first tab and the default view —
  // the running bars live on the overlays anyway, so the tab itself is
  // primarily where you configure.
  const PAGES = [
    { id: "edit", label: "Manage Timers" },
    { id: "overlays", label: "Manage Overlays" },
    { id: "live", label: "Current Timers" },
  ];
  let view = "edit"; // "edit" | "overlays" | "live"
  let state = {
    imported: true,
    character: "",
    alert: null,
    alerts: [],
    timers: [],
    activity: [],
  };
  let now = Date.now();
  let pollTimer, animReq, offTriggers, offUnhidden, offOvDefaults;

  const ALERT_SHOW_MS = 10000;
  // The banner shows the most recent alert, tinted by its category so it reads
  // the same way the countdown bars and the alert overlays do.
  $: alertShown =
    state.alert && state.alert.text && now - state.alert.at_ms < ALERT_SHOW_MS;
  $: alertColor = styleColor("alerts", state.alert?.category || "Default");

  // Group running timers by category; a category renders only while it has
  // active timers. Soonest-ending first inside each category.
  $: activeTimers = (state.timers || []).filter((t) => t.ends_at_ms > now);
  $: cats = (() => {
    const m = new Map();
    for (const t of activeTimers) {
      const c = t.category || "Default";
      if (!m.has(c)) m.set(c, []);
      m.get(c).push(t);
    }
    const out = [...m.entries()].map(([name, timers]) => ({
      name,
      color: styleColor("timers", name),
      timers: timers.sort((a, b) => a.ends_at_ms - b.ends_at_ms),
    }));
    out.sort((a, b) => a.name.localeCompare(b.name));
    return out;
  })();

  function fmtRemain(ms) {
    const s = Math.max(0, Math.ceil(ms / 1000));
    const m = Math.floor(s / 60);
    return `${String(m).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
  }

  // ── server-wide board: Game Time / Events / Boats (worldtimers.go) ──────────
  // The server owns the state; this just polls the board and extrapolates
  // between polls off the ticking `now` (spawn countdowns and boat loops are
  // periodic, so rolling forward by whole periods is exact).
  let world = null; // WorldTimersData — null until the first fetch lands
  let gameClock = null; // GameClockInfo (same anchor the footer clock uses)
  let worldTimer, gameClockTimer;

  // Collapsed/expanded is a pure UI preference — remembered locally.
  let worldOpen = true;
  try {
    worldOpen = localStorage.getItem("fuse.serverTimersOpen") !== "0";
  } catch {
    /* storage unavailable — default open */
  }
  function toggleWorld() {
    worldOpen = !worldOpen;
    try {
      localStorage.setItem("fuse.serverTimersOpen", worldOpen ? "1" : "0");
    } catch {
      /* not persisted */
    }
  }

  // ── board reminders (worldalarms.go) ───────────────────────────────────────
  // Alarms are keyed to a board entry, not to a moment, so they survive the
  // board re-anchoring underneath them.
  let alarms = {}; // key → WorldAlarm
  let alarmEdit = null; // { key, label, kind, existing } while the dialog is open

  async function loadAlarms() {
    try {
      const list = (await GetWorldAlarms()) || [];
      const m = {};
      for (const a of list) m[a.key] = a;
      alarms = m;
    } catch {
      /* keep what we had */
    }
  }
  function openAlarm(key, label, kind = "lead") {
    alarmEdit = { key, label, kind, existing: alarms[key] || null };
  }
  async function alarmSaved() {
    alarmEdit = null;
    await loadAlarms();
  }
  // Summary for the bell's tooltip, so an armed alarm says what it will do
  // without opening anything.
  function alarmTip(a) {
    if (!a) return "Set a reminder";
    const when =
      a.key === "quake"
        ? "when it happens"
        : a.lead_ms > 0
          ? `${Math.round(a.lead_ms / 60000)} min before`
          : "at the event";
    const how = [a.sound ? a.sound : null, a.speak ? "spoken" : null]
      .filter(Boolean)
      .join(" + ");
    return `Reminder ${when}${how ? " — " + how : ""}${a.repeat ? "" : " (once)"}`;
  }

  // "Last Earthquake": an indicator, not a countdown — it looks backwards.
  $: quakeAgo =
    world && world.last_quake_ms ? agoStr(now - world.last_quake_ms) : null;

  // ── activity feed height ────────────────────────────────────────────────────
  // Draggable and remembered locally. A busy pull pushes dozens of lines a
  // second through this box; a fixed 150px was fine for noticing the odd fire
  // and useless for actually reading the feed.
  const ACT_MIN = 56;
  let actH = 150;
  try {
    const v = parseInt(localStorage.getItem("fuse.activityH") || "", 10);
    if (v >= ACT_MIN) actH = v;
  } catch {
    /* not persisted — the default stands */
  }
  let actDrag = null; // {y0, h0} while dragging

  function actGripDown(e) {
    actDrag = { y0: e.clientY, h0: actH };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function actGripMove(e) {
    if (!actDrag) return;
    // The handle sits on the panel's TOP edge, so dragging up grows it.
    const max = Math.max(ACT_MIN, Math.round(window.innerHeight * 0.7));
    actH = Math.min(
      max,
      Math.max(ACT_MIN, actDrag.h0 + (actDrag.y0 - e.clientY)),
    );
  }
  function actGripUp(e) {
    if (!actDrag) return;
    actDrag = null;
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      /* pointer already gone */
    }
    try {
      localStorage.setItem("fuse.activityH", String(actH));
    } catch {
      /* not persisted */
    }
  }

  // Per-category collapse for the trigger sections, keyed by category name so
  // the preference survives a category emptying out and reappearing later.
  let catClosed = {};
  try {
    catClosed =
      JSON.parse(localStorage.getItem("fuse.timerCatsClosed") || "{}") || {};
  } catch {
    catClosed = {};
  }
  function toggleLiveCat(name) {
    catClosed = { ...catClosed, [name]: !catClosed[name] };
    try {
      localStorage.setItem("fuse.timerCatsClosed", JSON.stringify(catClosed));
    } catch {
      /* not persisted */
    }
  }

  async function pollWorld() {
    try {
      world = await GetWorldTimers();
    } catch {
      /* keep last board */
    }
  }
  async function pollGameClock() {
    try {
      gameClock = await GetGameClock();
    } catch {
      /* keep last anchor */
    }
  }

  // In-game hour as a float (0..24), extrapolated from the anchor.
  function gameHourFloat(c, ms) {
    if (!c || !c.have_game) return null;
    const R = c.ms_per_game_hour || 180000;
    const gh = c.anchor_game_hour + (ms - c.anchor_earth_ms) / R;
    return ((gh % 24) + 24) % 24;
  }
  function fmtGameClock(c, ms) {
    const gh = gameHourFloat(c, ms);
    if (gh == null) return "—";
    const h = Math.floor(gh);
    let h12 = h % 12;
    if (h12 === 0) h12 = 12;
    return `${h12} ${h < 12 ? "AM" : "PM"}`;
  }
  // Real milliseconds until the game hour flips (one game hour = 3 real min).
  function nextGameHourIn(c, ms) {
    const gh = gameHourFloat(c, ms);
    if (gh == null) return 0;
    const R = c.ms_per_game_hour || 180000;
    return Math.round((1 - (gh % 1)) * R);
  }
  const hourTicks = Array.from({ length: 23 }, (_, i) => i + 1);
  const hour12 = (h) => (h % 12 === 0 ? 12 : h % 12);
  // The hour boundary the fill has PASSED — the hour it is now. Marked in gold
  // as the answer to "what time is it", with the upcoming hour greyed beside
  // it so the pair reads as now → next.
  function currentHourInfo(c, ms) {
    const gh = gameHourFloat(c, ms);
    if (gh == null) return null;
    const h = Math.floor(gh) % 24;
    return { hour: h, pos: (h / 24) * 100, label: hour12(h) };
  }
  // The boundary the fill is approaching: its tick index (0 = the
  // midnight/right edge), track position (%), and 12-hour label.
  function nextHourInfo(c, ms) {
    const gh = gameHourFloat(c, ms);
    if (gh == null) return null;
    const nh = Math.ceil(gh) % 24;
    return {
      hour: nh,
      pos: nh === 0 ? 100 : (nh / 24) * 100,
      label: hour12(nh),
    };
  }

  // Long countdowns (events run up to 24h): h:mm:ss, or m:ss under an hour.
  function fmtLong(ms) {
    const s = Math.max(0, Math.ceil(ms / 1000));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const pad = (n) => String(n).padStart(2, "0");
    return h > 0 ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`;
  }
  function agoStr(ms) {
    const s = Math.max(0, Math.floor(ms / 1000));
    if (s < 60) return "just now";
    if (s < 3600) return `${Math.floor(s / 60)}m ago`;
    return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m ago`;
  }

  // Event view: if the spawn time passed between polls, roll forward by whole
  // respawns locally (the server does the same on its side) and add one "?"
  // per unobserved respawn — mirroring Gynok's own drift counter. A windowed
  // event (window_ms half-width around the center) only elapses once its
  // whole window has closed — rolling the center forward mid-window would
  // hide a window that's still open.
  function evView(e, ms) {
    let at = e.spawn_at_ms;
    let marks = e.marks;
    const win = e.window_ms || 0;
    if (e.respawn_ms > 0) {
      while (at + win <= ms) {
        at += e.respawn_ms;
        marks += 1;
      }
    }
    // A quake-anchored timer carries no drift to report. The earthquake IS the
    // event — it repopped the world — so the anchor is exact however long ago
    // it happened, and marks accumulated by the elapsed-timer backup say
    // nothing about it. No "?" here, regardless of age.
    if (e.from_quake) marks = 0;
    const frac =
      e.respawn_ms > 0 ? Math.max(0, Math.min(1, (at - ms) / e.respawn_ms)) : 0;
    // Windowed extras: open/close instants and whether we're inside.
    const open = at - win;
    const close = at + win;
    return { at, marks, frac, win, open, close, inWin: win > 0 && ms >= open };
  }

  // Hover text for an event bar: where the timer came from, then how much to
  // trust it. A quake anchor gets no trust clause — it has no drift to caveat.
  function evTitle(e, v, ms) {
    let t;
    if (e.from_quake) {
      t =
        "Anchored on the earthquake " +
        agoStr(ms - e.updated_ms) +
        " — roll is 30 minutes prior to last quake time";
    } else {
      t =
        "Last report " +
        agoStr(ms - e.updated_ms) +
        (v.marks > 0
          ? ` — ${v.marks} respawn${v.marks === 1 ? "" : "s"} since a confirmed ToD`
          : " (exact)");
    }
    if (v.win > 0) {
      t +=
        " — spawn window " +
        new Date(v.open).toLocaleString() +
        " to " +
        new Date(v.close).toLocaleString();
    }
    return t;
  }

  // Boat anchor staleness. Predictions extrapolate from the last sighting,
  // and the boats' occasional slow laps make an old anchor drift roughly a
  // minute or two per hour — so a day-old timer deserves a caution and a
  // three-day-old one a stronger warning.
  const BOAT_STALE_MS = 24 * 3600 * 1000;
  const BOAT_VERY_STALE_MS = 3 * 24 * 3600 * 1000;
  function boatStale(b, ms) {
    if (!b.seen_ms) return "";
    const age = ms - b.seen_ms;
    if (age > BOAT_VERY_STALE_MS) return "red";
    if (age > BOAT_STALE_MS) return "yellow";
    return "";
  }
  function boatWarn(b, ms) {
    const lvl = boatStale(b, ms);
    if (!lvl) return "";
    return lvl === "red"
      ? " — WARNING: this timer is over 3 days old; treat these times as unreliable until someone sights the boat"
      : " — WARNING: this timer is over 24 hours old; times may have drifted by several minutes";
  }

  // Fixed 2×2 boat layout: Oasis→TD upper left, TD→OT lower left, BB→FV
  // upper right, NRO→IC lower right. The grid fills row-by-row, so the DOM
  // order is barrel, maiden, bloated, icebreaker; unknown keys sort last.
  const BOAT_GRID_ORDER = { barrel: 0, maiden: 1, bloated: 2, icebreaker: 3 };
  const boatOrder = (list) =>
    [...(list || [])].sort(
      (a, b) => (BOAT_GRID_ORDER[a.key] ?? 9) - (BOAT_GRID_ORDER[b.key] ?? 9),
    );

  // Boat view: dock times roll forward by the loop period; the ship marker
  // travels toward whichever dock is next (A is the left end, B the right).
  function boatView(b, ms) {
    const P = b.period_ms;
    const clamp = (x) => Math.max(0, Math.min(1, x));
    let a = b.dock_a_ms;
    let bb = b.dock_b_ms;
    if (P > 0) {
      while (a > 0 && a <= ms) a += P;
      while (bb > 0 && bb <= ms) bb += P;
    }
    let pos = null;
    let dir = 1; // 1 = sailing toward B (right), -1 = toward A (left)
    if (P > 0 && a > 0 && bb > 0) {
      if (a < bb) {
        // next stop is A: it left B one period before B's next docking
        const start = bb - P;
        dir = -1;
        pos = 1 - clamp((ms - start) / (a - start));
      } else {
        const start = a - P;
        dir = 1;
        pos = clamp((ms - start) / (bb - start));
      }
    } else if (P > 0 && a > 0) {
      // one-sided schedule (Bloated Belly): sweep toward the known dock
      dir = -1;
      pos = 1 - clamp((a - ms) / P);
    }
    return { a, b: bb, pos, dir };
  }
  function fmtClock(ms) {
    return new Date(ms).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    });
  }
  function barFrac(t) {
    const total = t.ends_at_ms - t.started_at_ms;
    if (total <= 0) return 0;
    return Math.max(0, Math.min(1, (t.ends_at_ms - now) / total));
  }

  // Overlap guard: the engine pushes "triggers-changed" the instant a trigger
  // fires, so polls can arrive faster than they complete. If one is in flight,
  // remember to re-poll after it so we never settle on stale state.
  let polling = false,
    pollAgain = false;

  // Which overlays are open right now, for the tree's popout shortcuts (an
  // open one shows lit + disabled). Re-assigned each poll so props update.
  let poppedSet = new Set();

  // Window names, as popoutIdent builds them: specials are one per kind,
  // category overlays one per (kind, category). An overlay that is currently
  // up is one you'd want to take down, so its button becomes Remove.
  const popName = (kind, cat) =>
    cat ? `popout-${kind}-${cat}` : `popout-${kind}`;
  $: isPopped = (kind, cat) => poppedSet.has(popName(kind, cat));

  async function poll() {
    if (polling) {
      pollAgain = true;
      return;
    }
    polling = true;
    try {
      poppedSet = new Set((await GetOpenPopouts()) || []);
      const prevChar = state.character;
      state = await GetTriggerState();
      // Enabled/disabled is per character. When the page is following the
      // logged-in toon (no explicit pick), a swap has to refetch the tree —
      // a pinned character keeps showing what was asked for.
      if (state.character !== prevChar) {
        if (treeLoaded && !editChar) await loadTree();
        if (view === "overlays") await loadCategories();
        if (chars.length) await loadChars();
      }
    } catch {
      /* keep last state */
    }
    polling = false;
    if (pollAgain) {
      pollAgain = false;
      poll();
    }
  }

  // Dismiss running timers from the live board. Optimistically drop them from
  // local state so the bar disappears immediately (the poll is ~1s behind).
  async function dismissTimer(t) {
    state.timers = (state.timers || []).filter((x) => x.id !== t.id);
    try {
      await DismissTimer(t.id);
    } catch {
      /* poll will re-sync */
    }
  }
  async function dismissCat(c) {
    state.timers = (state.timers || []).filter(
      (x) => (x.category || "Default") !== c.name,
    );
    try {
      await DismissTimerCategory(c.name);
    } catch {
      /* poll will re-sync */
    }
  }

  // Global overlay controls: hide/restore all overlays, and lock (make them
  // click-through + non-movable) / unlock all. Unlock here is the escape hatch
  // for a locked, click-through overlay that can't unlock itself.
  let winHidden = false;
  let winLocked = false;
  async function toggleHideWindows() {
    winHidden = !winHidden;
    try {
      await SetPopoutsHidden(winHidden);
    } catch {
      /* no overlays open */
    }
  }
  async function toggleLockWindows() {
    winLocked = !winLocked;
    try {
      await SetAllPopoutsLocked(winLocked);
    } catch {
      /* no overlays open */
    }
  }
  // When overlay title bars are shown: always / only-when-unlocked / only-when-active.
  let titleMode = "always";
  async function changeTitleMode() {
    try {
      await SetOverlayTitleMode(titleMode);
    } catch {
      /* ignore */
    }
  }
  // Snap overlays to a 10px grid on move + resize.
  let winSnap = false;
  async function toggleSnapGrid() {
    winSnap = !winSnap;
    try {
      await SetSnapToGrid(winSnap);
    } catch {
      /* no overlays open */
    }
  }
  // Hide overlays whenever the active window is neither EQ nor this app.
  let winFocusHide = false;
  async function toggleFocusHide() {
    winFocusHide = !winFocusHide;
    try {
      await SetHideOverlaysUnfocused(winFocusHide);
    } catch {
      /* setting not saved */
    }
  }

  // ── server sync + officer status ────────────────────────────────────────────
  // The Fuse Triggers set is downloaded from the server on open; officers'
  // edits sync back automatically. importMsg/importErr are transient notices.
  let importMsg = "";
  let importErr = "";
  let meta = { linked: false, officer: false };

  // ── default overlay layout ─────────────────────────────────────────────────
  // The default is a layout every character with none of their own starts from.
  // Editing it swaps the on-screen overlays for the default set on the Go side
  // (see popouts.go), so arranging them here IS authoring the default.
  let ovDef = {
    configured: false,
    editing: false,
    count: 0,
    char: "",
    char_set: false,
    default_key: "*default*",
  };
  let ovBusy = false;
  let ovMsg = "";
  let ovErr = "";
  let ovConfirmReset = false;
  let ovMsgTimer;

  function ovNote(msg, err = "") {
    ovMsg = msg;
    ovErr = err;
    clearTimeout(ovMsgTimer);
    ovMsgTimer = setTimeout(() => {
      ovMsg = "";
      ovErr = "";
    }, 4000);
  }

  // "New to timers?" CTA latch: the banner shows until ANY overlay layout has
  // ever existed — the default layout configured, or a character with their
  // own — and once that's been observed it never comes back, even if every
  // layout is later deleted. Persisted so the latch survives restarts.
  const CTA_KEY = "fuse.timersCtaDone";
  let ctaDone = false;
  try {
    ctaDone = localStorage.getItem(CTA_KEY) === "1";
  } catch {
    /* show it; worst case it latches next session */
  }
  $: if (!ctaDone && (ovDef.configured || ovDef.count > 0 || ovDef.char_set)) {
    ctaDone = true;
    try {
      localStorage.setItem(CTA_KEY, "1");
    } catch {}
  }

  async function loadOverlayDefaults() {
    try {
      ovDef = await GetOverlayDefaultInfo();
    } catch {
      /* keep last */
    }
  }

  async function toggleDefaultEditing() {
    if (ovBusy) return;
    ovBusy = true;
    ovConfirmReset = false;
    try {
      await SetOverlayDefaultEditing(!ovDef.editing);
    } catch (e) {
      ovNote("", String(e));
    }
    await loadOverlayDefaults();
    ovBusy = false;
  }

  async function saveCharAsDefault() {
    if (ovBusy || !ovDef.char) return;
    ovBusy = true;
    try {
      await SaveOverlayDefaultFromCharacter(ovDef.char);
      // Mirror the look settings: geometry lives on the Go side, but each
      // overlay's colour/opacity/fit is localStorage keyed by character.
      copyOverlayLook(ovDef.char.toLowerCase(), ovDef.default_key);
      ovNote(`Saved ${ovDef.char}'s overlay layout as the default.`);
    } catch (e) {
      ovNote("", String(e));
    }
    await loadOverlayDefaults();
    ovBusy = false;
  }

  async function resetCharToDefault() {
    if (ovBusy || !ovDef.char) return;
    ovBusy = true;
    ovConfirmReset = false;
    try {
      await ResetCharacterToDefault(ovDef.char);
      copyOverlayLook(ovDef.default_key, ovDef.char.toLowerCase(), true);
      ovNote(`${ovDef.char} is back to the default overlay layout.`);
    } catch (e) {
      ovNote("", String(e));
    }
    await loadOverlayDefaults();
    ovBusy = false;
  }

  // Overlay look settings are localStorage entries named
  // "fuse.popout.<overlay>@<profile>" — written by the overlay windows, which
  // are same-origin with this one. Copy every entry from one profile to
  // another; with wipe set, the destination's leftovers are cleared first so a
  // reset really is a reset and not a merge.
  const OV_LOOK_PREFIX = "fuse.popout.";
  function copyOverlayLook(fromKey, toKey, wipe = false) {
    if (!fromKey || !toKey || fromKey === toKey) return;
    try {
      const fromSuffix = `@${fromKey}`;
      const toSuffix = `@${toKey}`;
      const all = Object.keys(localStorage);
      if (wipe) {
        for (const k of all) {
          if (k.startsWith(OV_LOOK_PREFIX) && k.endsWith(toSuffix))
            localStorage.removeItem(k);
        }
      }
      for (const k of all) {
        if (!k.startsWith(OV_LOOK_PREFIX) || !k.endsWith(fromSuffix)) continue;
        const base = k.slice(0, k.length - fromSuffix.length);
        const v = localStorage.getItem(k);
        if (v != null) localStorage.setItem(base + toSuffix, v);
      }
    } catch {
      /* storage blocked — geometry still transferred */
    }
  }

  async function loadMeta() {
    try {
      meta = await GetTriggersMeta();
    } catch {
      /* keep last */
    }
  }
  async function syncNow() {
    try {
      await SyncTriggers(); // background fetch (and officer seed) on the Go side
    } catch {
      /* offline — the cached set stays usable */
    }
    await loadMeta();
    // Give the background sync a moment, then refresh what's on screen.
    setTimeout(async () => {
      await poll();
      if (view === "edit") await loadTree();
      await loadMeta();
    }, 2500);
  }

  // ── Fuse Triggers revisions ──────────────────────────────────────────────
  // Officer edits stay local until published. The Fuse root node in the tree
  // carries the revision state (version / server_version / dirty), so the
  // publish bar refreshes with every loadTree without extra requests.
  $: fuseRootNode = (tree || []).find((g) => g && !g.personal) || null;

  let publishing = false;
  async function publishTriggers() {
    const ok = await confirmDialog({
      title: "Publish to guild",
      message: "Publish your Fuse Trigger changes to the whole guild?",
      detail:
        fuseRootNode && fuseRootNode.server_version > fuseRootNode.version
          ? `Everyone's client will update to the new version. This also overwrites v${fuseRootNode.server_version}, published by another officer since you started editing.`
          : "Everyone's client will update to the new version.",
      confirmLabel: "Publish",
    });
    if (!ok) return;
    publishing = true;
    try {
      const v = await PublishFuseTriggers();
      importMsg = `Published Fuse Triggers v${v}.`;
      setTimeout(() => (importMsg = ""), 6000);
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 8000);
    }
    publishing = false;
  }
  async function revertTriggers() {
    const ok = await confirmDialog({
      title: "Discard changes",
      message:
        "Discard your local Fuse Trigger changes and revert to the published server copy?",
      detail: "Your unpublished edits can't be recovered.",
      confirmLabel: "Discard",
      danger: true,
    });
    if (!ok) return;
    publishing = true;
    try {
      await RevertFuseTriggers();
      importMsg =
        "Local changes discarded — back on the published Fuse Triggers.";
      setTimeout(() => (importMsg = ""), 6000);
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 8000);
    }
    publishing = false;
  }

  // ── Fuse Triggers XML round trip (officer-only) ──────────────────────────
  // Bulk editing is easier in a text editor, but the app rewrites its own
  // storage on every edit — so the handoff is an explicit export/import rather
  // than editing the live file. Import lands as unpublished edits; the publish
  // bar above is still what pushes anything to the guild.
  let fuseImp = null; // preview result from PreviewFuseTriggersImport
  let fuseImpBusy = false;

  async function exportFuseXML() {
    try {
      const p = await ExportFuseTriggers();
      if (!p) return; // cancelled
      importMsg = `Exported to ${p}`;
      setTimeout(() => (importMsg = ""), 8000);
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 8000);
    }
  }

  async function beginFuseImport() {
    try {
      const p = await BrowseFuseTriggersXML();
      if (!p) return; // cancelled
      fuseImp = await PreviewFuseTriggersImport(p);
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 8000);
    }
  }

  async function confirmFuseImport() {
    if (!fuseImp || !fuseImp.valid) return;
    fuseImpBusy = true;
    try {
      await ImportFuseTriggersXML(fuseImp.path);
      const n = fuseImp.triggers;
      fuseImp = null;
      importMsg = `Imported ${n} Fuse triggers — review them, then Publish to guild.`;
      setTimeout(() => (importMsg = ""), 10000);
      await loadTree();
      await loadMeta();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 8000);
    }
    fuseImpBusy = false;
  }

  // ── edit view: tree, context menu, form ─────────────────────────────────────
  let tree = [];
  let expanded = {}; // groupId -> true
  let highlightId = 0;
  let treeLoaded = false;

  // Which character's enable/disable state Manage Timers is showing. "" tracks
  // whoever is logged in; picking a name pins the page to that toon so you can
  // set up an alt before ever logging it in.
  let editChar = "";
  let chars = [];
  $: shownChar = editChar || state.character;

  async function loadChars() {
    try {
      chars = (await GetTriggerCharacters()) || [];
    } catch {
      chars = [];
    }
  }

  // "Configure Defaults": the tree edits a staging set through this reserved
  // pseudo-character; SaveTriggerDefaults applies it to everyone at once.
  const DEFAULTS_CHAR = "*defaults*";
  let defaultsMode = false;
  // The character the tree reads/writes right now.
  const cfgChar = () => (defaultsMode ? DEFAULTS_CHAR : editChar);

  async function beginDefaults() {
    try {
      await BeginTriggerDefaults();
      defaultsMode = true;
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 6000);
    }
  }
  async function cancelDefaults() {
    try {
      await CancelTriggerDefaults();
    } catch {
      /* mode exits regardless */
    }
    defaultsMode = false;
    await loadTree();
  }
  async function saveDefaults() {
    const n = chars.length;
    const ok = await confirmDialog({
      title: "Save defaults for ALL characters",
      message: "Apply these timer selections to every character?",
      detail:
        `This overwrites the current enable/disable choices on ${n ? `all ${n} of your characters` : "every character"} ` +
        "and becomes the starting set for new ones. Class-specific folders stay auto-detected per character.",
      confirmLabel: "Apply to all",
    });
    if (!ok) return;
    try {
      await SaveTriggerDefaults();
      defaultsMode = false;
      await loadTree();

      importMsg = "Defaults applied to all characters.";
      setTimeout(() => (importMsg = ""), 5000);
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 6000);
    }
  }

  async function loadTree() {
    try {
      tree = (await GetTriggerTreeFor(cfgChar())) || [];
      treeLoaded = true;
    } catch {
      tree = [];
    }
  }

  async function pickChar(name) {
    // Selecting the logged-in character returns to "follow the current toon".
    editChar =
      name && name.toLowerCase() === (state.character || "").toLowerCase()
        ? ""
        : name;
    await loadTree();
  }

  async function setView(v) {
    view = v;
    if (v === "edit" && !treeLoaded) {
      await loadChars();
      await loadTree();
    }
    if (v === "overlays") await loadCategories();
  }

  // ── overlays view: every category that can produce a bar or an alert ────────
  // Special Overlays: fixed app-wide overlays (the map + live raid-card
  // sections), not trigger categories. Position/size are shared by every
  // character; each overlay's background color/opacity is set from its own ⚙
  // settings panel.
  const SPECIAL_OVERLAYS = [
    {
      kind: "map",
      name: "Map",
      desc: "The live zone map — same overlay as the Map tab's pop out.",
    },
    {
      kind: "raidassign",
      name: "Raid Assignments",
      desc: "Current tank with proc count, ramp tank, and the tank/bump lists.",
    },
    {
      kind: "raiddebuffs",
      name: "Raid Debuffs",
      desc: "Debuffs and sieve counts per mob, with debuff timer bars.",
    },
    {
      kind: "raidclerics",
      name: "Raid Clerics",
      desc: "Fluffer assignment and the CH chain, with 10s cast bars.",
    },
    {
      kind: "othertimers",
      name: "Raid Specific Timers",
      desc: "Raid and event timers specific to their given raid — mob AE timers, Ring War timers, Sirran timer, etc.",
    },
    {
      kind: "voicespeakers",
      name: "Voice Speakers",
      desc: "Displays current speaker(s) in the Fuse discord #raid-voice channel.",
    },
    {
      kind: "randoms",
      name: "Randoms",
      desc: "Live /random results, grouped by number rolled. Lists item when detected.",
      // Read entirely from the local log, so unlike the raid overlays this one
      // works without a Discord link.
      local: true,
    },
    {
      kind: "threat",
      name: "DPS & Threat",
      desc: "Auto switches between group/raid mode. Group/solo - simple parser view. Raids - adds a threat indicator that monitors your threat relative to the tank.",
      // The group-mode parser reads the local log, so like Randoms it works
      // without a Discord link (collation and raid mode need one).
      local: true,
    },
    {
      kind: "raiddps",
      name: "Raid DPS",
      desc: "Live raid-wide damage on the current mob: total DPS, top 5, and a breakdown by class.",
    },
  ];
  // The raid overlays render server raid data an unlinked client never
  // receives; local ones stand on their own. The `officer` flag is still
  // honoured for any overlay that sets it — none do today, since the damage
  // and threat overlays describe the fight you are standing in rather than
  // anything to manage.
  $: visibleSpecials = SPECIAL_OVERLAYS.filter(
    (so) => (meta.linked || so.local) && (!so.officer || meta.officer),
  );

  let categories = [];
  $: timerCats = categories.filter((c) => c.kind === "timers");
  $: alertCats = categories.filter((c) => c.kind === "alerts");

  // Configured category colors, looked up by kind+name, so the live board draws
  // a category the same way its overlay does. The palette hash is the fallback
  // until the inventory loads.
  //
  // Deliberately NOT a `$:` declaration. styleColor() is called from the `cats`
  // and `alertColor` reactive statements, but Svelte only orders reactive
  // statements by the identifiers they reference directly — it can't see
  // through a function call. A reactive catStyles would therefore still be
  // undefined when those first run, and styleColor would throw. Those two
  // recompute every animation frame anyway (they depend on `now`), so a plain
  // variable reassigned here shows a style change just as promptly.
  let catStyles = new Map();
  function styleColor(kind, name) {
    const s = catStyles.get(`${kind}|${(name || "").toLowerCase()}`);
    if (!s) return catColor(name || "Default");
    return (kind === "alerts" ? s.font_color : s.bar_color) || catColor(name);
  }

  async function loadCategories() {
    try {
      categories = (await GetTriggerCategories()) || [];
    } catch {
      categories = [];
    }
    catStyles = new Map(
      categories.map((c) => [`${c.kind}|${c.name.toLowerCase()}`, c.style]),
    );
    // The tree isn't fetched here — it's big, and the expandable trigger list
    // under a card loads it on first expand (toggleCat).
  }

  // Categories are keyed by kind+name: one name can feed both a timer-bar and a
  // text-alert overlay, and those are configured separately.
  const catKey = (c) => `${c.kind}|${c.name.toLowerCase()}`;

  // ── expandable trigger list under a category card ───────────────────────────
  let openCat = ""; // catKey of the expanded card, "" for none
  let catExpanded = {}; // group id -> true, for the expanded card's tree

  const catOf = (t) => (t.category || "").trim() || "";
  // Which overlay a trigger feeds. A timer-ended alert counts as a text alert,
  // matching how the Go side inventories categories.
  function feedsKind(t, kind) {
    if (kind === "timers") return t.timer_enabled;
    const om = t.on_match || {};
    const ended = t.ended || {};
    return (
      (om.use_text && (om.display_text || "").trim() !== "") ||
      (t.ended_enabled &&
        ended.use_text &&
        (ended.display_text || "").trim() !== "")
    );
  }
  // Prune the tree to the triggers in this category, keeping the group
  // hierarchy so the rows read the same as they do in Manage Timers.
  function filterByCategory(nodes, name, kind) {
    const out = [];
    for (const g of nodes) {
      const kids = filterByCategory(g.groups || [], name, kind);
      const trigs = (g.triggers || []).filter(
        (t) =>
          catOf(t).toLowerCase() === name.toLowerCase() && feedsKind(t, kind),
      );
      if (kids.length || trigs.length)
        out.push({ ...g, groups: kids, triggers: trigs });
    }
    return out;
  }
  $: catTree =
    openCat && tree.length
      ? filterByCategory(
          tree,
          openCat.slice(openCat.indexOf("|") + 1),
          openCat.slice(0, openCat.indexOf("|")),
        )
      : [];

  async function toggleCat(c) {
    const k = catKey(c);
    if (openCat === k) {
      openCat = "";
      return;
    }
    if (!treeLoaded) await loadTree();
    openCat = k;
    // The filtered tree is small — start it fully expanded so the triggers are
    // visible without another click per group. Filtered here rather than read
    // off catTree, which won't have recomputed until after this handler.
    catExpanded = collectGroupIds(filterByCategory(tree, c.name, c.kind));
  }
  function onCatToggle(id) {
    catExpanded[id] = !catExpanded[id];
    catExpanded = catExpanded;
  }

  // ── category create / edit / delete ─────────────────────────────────────────
  // catForm doubles for both: oldName is "" when creating.
  let catForm = null; // {oldName, isNew, style}
  let catFormErr = "";
  let catDel = null; // {name, kind, reassign, options}

  // A small, safe set: whatever the user picks has to exist on their machine.
  const FONTS = [
    { v: "", label: "Default (app font)" },
    { v: "Segoe UI, sans-serif", label: "Segoe UI" },
    { v: "Arial, sans-serif", label: "Arial" },
    { v: "Verdana, sans-serif", label: "Verdana" },
    { v: "Tahoma, sans-serif", label: "Tahoma" },
    { v: "Georgia, serif", label: "Georgia" },
    { v: "Times New Roman, serif", label: "Times New Roman" },
    { v: "Consolas, monospace", label: "Consolas" },
    { v: "Courier New, monospace", label: "Courier New" },
    { v: "Impact, sans-serif", label: "Impact" },
  ];

  function newCatForm(kind) {
    catFormErr = "";
    catForm = {
      oldName: "",
      isNew: true,
      style: {
        name: "",
        kind,
        bar_color: "#4fb3a9",
        bar_opacity: 0.82,
        bg_color: "#000000",
        bg_opacity: 0,
        font_family: "",
        font_color: kind === "alerts" ? "#4fb3a9" : "#ffffff",
        font_size: kind === "alerts" ? 16 : 12,
        // Off for a brand-new category: only buffs and disciplines keep running
        // while you're out of game, and those two already exist.
        auto_pause: false,
      },
    };
  }
  // Sticky for the overlay this category is shown in. Applies to the OVERLAY
  // (kind + category), not the category's look, which is why it sits apart at
  // the bottom of the form and saves on the spot rather than on Save.
  let catSticky = false;
  async function loadCatSticky(kind, name) {
    catSticky = false;
    try {
      catSticky = await GetPopoutSticky(kind, name);
    } catch {
      /* default off */
    }
  }
  async function setCatSticky(on) {
    catSticky = on;
    if (!catForm) return;
    try {
      await SetPopoutSticky(catForm.style.kind, catForm.oldName || "", on);
    } catch {
      /* Go keeps the truth; reopening shows it */
    }
  }

  function editCatForm(c) {
    catFormErr = "";
    // Fall back to a fresh default if the style didn't come through, so the
    // form still opens with a valid kind rather than failing on save.
    newCatForm(c.kind);
    catForm = {
      oldName: c.name,
      isNew: false,
      style: {
        ...catForm.style,
        ...(c.style || {}),
        name: c.name,
        kind: c.kind,
      },
    };
    loadCatSticky(c.kind, c.name);
  }

  async function saveCatForm() {
    const s = catForm.style;
    s.name = (s.name || "").trim();
    if (!s.name) {
      catFormErr = "Name is required.";
      return;
    }
    s.font_size = Math.max(6, Math.round(Number(s.font_size) || 12));
    try {
      if (catForm.isNew) {
        await CreateTriggerCategory(s.kind, s.name, s.bar_color);
        // Create only registers name + color; push the rest of the style.
        await SaveTriggerCategory(s.name, s);
      } else {
        await SaveTriggerCategory(catForm.oldName, s);
      }
      catForm = null;
      await loadCategories();
      await loadTree(); // a rename rewrites Category on the triggers
    } catch (e) {
      catFormErr = String(e);
    }
  }

  async function openCatDelete(c) {
    let options = [];
    try {
      options = (await GetCategoryNames()) || [];
    } catch {
      /* fall back to no reassignment target */
    }
    catDel = {
      name: c.name,
      kind: c.kind,
      reassign: "",
      options: options.filter((n) => n.toLowerCase() !== c.name.toLowerCase()),
    };
  }
  async function confirmCatDelete() {
    const d = catDel;
    catDel = null;
    try {
      await DeleteTriggerCategory(d.name, d.reassign);
      if (openCat.endsWith(`|${d.name.toLowerCase()}`)) openCat = "";
      await loadCategories();
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 8000);
    }
  }

  // ── search: filters the tree to matching triggers/groups, ↑/↓ steps through
  // matches (like the Characters search). Matching is on trigger names AND
  // regex lines; a matching group name keeps its whole subtree visible.
  let searchQ = "";
  let matchIdx = 0;
  let searchExpanded = {}; // expansion state while a search is active
  let lastQ = "";

  function trigMatches(t, q) {
    return (
      t.name.toLowerCase().includes(q) ||
      (t.trigger_text || "").toLowerCase().includes(q)
    );
  }
  function filterTree(nodes, q) {
    const out = [];
    for (const g of nodes) {
      if (g.name.toLowerCase().includes(q)) {
        out.push(g); // group name matched — keep its whole subtree
        continue;
      }
      const kids = filterTree(g.groups || [], q);
      const trigs = (g.triggers || []).filter((t) => trigMatches(t, q));
      if (kids.length || trigs.length)
        out.push({ ...g, groups: kids, triggers: trigs });
    }
    return out;
  }
  function collectGroupIds(nodes, acc = {}) {
    for (const g of nodes) {
      acc[g.id] = true;
      collectGroupIds(g.groups || [], acc);
    }
    return acc;
  }
  // Match list mirrors the tree's visual order (subgroups render first).
  function collectMatchIds(nodes, q, acc = []) {
    for (const g of nodes) {
      collectMatchIds(g.groups || [], q, acc);
      for (const t of g.triggers || []) if (trigMatches(t, q)) acc.push(t.id);
    }
    return acc;
  }

  $: q = searchQ.trim().toLowerCase();
  $: shownTree = q ? filterTree(tree, q) : tree;
  $: matchIds = q ? collectMatchIds(shownTree, q) : [];
  $: if (q !== lastQ) {
    lastQ = q;
    matchIdx = 0;
    if (q) searchExpanded = collectGroupIds(shownTree);
  }
  $: if (q && matchIdx >= matchIds.length) matchIdx = 0;
  $: if (q) highlightId = matchIds.length ? matchIds[matchIdx] : 0;

  async function stepMatch(d) {
    if (!matchIds.length) return;
    matchIdx = (matchIdx + d + matchIds.length) % matchIds.length;
    await tick();
    const el = document.getElementById(`trig-${matchIds[matchIdx]}`);
    if (el) el.scrollIntoView({ block: "center" });
  }

  function onToggle(id) {
    if (q) {
      searchExpanded[id] = !searchExpanded[id];
      searchExpanded = searchExpanded;
    } else {
      expanded[id] = !expanded[id];
      expanded = expanded;
    }
  }

  // Enable/disable slider on a group or trigger row — applied to the character
  // the page is showing, not necessarily the one logged in.
  async function onToggleEnable(kind, obj, val) {
    try {
      if (kind === "group")
        await SetTriggerGroupEnabledFor(cfgChar(), obj.id, val);
      else await SetTriggerEnabledFor(cfgChar(), obj.id, val);
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 6000);
    }
  }

  // Context menu (right-click on a group or trigger row). Only editable nodes
  // get a menu: Fuse Triggers content is officer-only (non-officers can still
  // enable/disable via the slider), while Personal is editable by everyone.
  let menu = null; // {x, y, kind: "group"|"trigger"|"category", target}
  function onMenu(e, kind, target) {
    // Categories are app-local presentation, so everyone gets the menu; the
    // rename/delete calls themselves reject a non-officer touching Fuse
    // triggers. Groups gate on the node's own editability; trigger rows always
    // get a menu — a non-editable Fuse trigger can still be shared (its menu
    // just hides Edit/Delete).
    if (kind === "group" && !target.editable) return;
    // The shell is CSS-zoomed; convert viewport coords to layout coords.
    menu = { x: e.clientX / $scale, y: e.clientY / $scale, kind, target };
  }
  function closeMenu() {
    menu = null;
  }

  // Special-overlay editor. Special overlays have no colours or categories to
  // change, so until now there was nothing to right-click for; Sticky Mode is
  // the first per-overlay setting they have, and it needs a home here as well
  // as in the overlay's own gear menu — an overlay that has been hidden is
  // exactly the one you can't reach the gear on.
  let spForm = null; // {kind, name, sticky}
  async function editSpecial(so) {
    let on = false;
    try {
      on = await GetPopoutSticky(so.kind, "");
    } catch {
      /* default off */
    }
    spForm = { kind: so.kind, name: so.name, sticky: on };
  }
  async function setSpecialSticky(on) {
    if (!spForm) return;
    spForm.sticky = on;
    try {
      await SetPopoutSticky(spForm.kind, "", on);
    } catch {
      /* Go keeps the truth; reopening shows it */
    }
  }

  // Single-input prompt modal (group rename / new group).
  let prompt = null; // {title, value, onOK(value)}
  function openPrompt(title, value, onOK) {
    prompt = { title, value, onOK };
  }
  async function promptOK() {
    const p = prompt;
    prompt = null;
    if (p && p.value.trim()) await p.onOK(p.value.trim());
  }

  // Trigger edit/create form modal.
  let form = null; // TriggerEditUI-shaped
  let formIsNew = false;
  let formGroupId = 0;
  let formErr = "";
  let mediaFiles = []; // audio files available to assign (loaded when the form opens)

  // Master audio lives in the tab bar (App.svelte) — one app-wide control, not
  // one per tab.

  let catNames = []; // category names for the edit form's Category dropdown

  async function loadMediaFiles() {
    try {
      mediaFiles = await GetTriggerMediaFiles();
    } catch {
      mediaFiles = [];
    }
  }
  async function loadCatNames() {
    try {
      catNames = (await GetCategoryNames()) || [];
    } catch {
      catNames = [];
    }
  }

  // Audio mute on the Fuse subtree: toggles the clicked node's OWN flag
  // (inherited mutes are undone at the ancestor that set them).
  async function onToggleMute(kind, item) {
    try {
      if (kind === "group") await SetTriggerGroupMuted(item.id, !item.muted);
      else await SetTriggerMuted(item.group_id, item.name, !item.muted);
      await loadTree();
    } catch (e) {
      importErr = String(e);
    }
  }
  // Clipboard block on the Fuse subtree — same own-flag rule as the mute:
  // an inherited block is undone at the ancestor that set it.
  async function onToggleClip(kind, item) {
    try {
      if (kind === "group")
        await SetTriggerGroupClipboardBlocked(item.id, !item.clip_blocked);
      else
        await SetTriggerClipboardBlocked(
          item.group_id,
          item.name,
          !item.clip_blocked,
        );
      await loadTree();
    } catch (e) {
      importErr = String(e);
    }
  }
  // Keep the trigger's own category selectable even if it isn't a known category
  // yet, so saving doesn't clear it.
  $: catOptions =
    form && form.category && !catNames.includes(form.category)
      ? [form.category, ...catNames]
      : catNames;

  // Inline category creation from the edit form — otherwise a new category
  // can only be born on the Manage Overlays page, which is a detour mid-edit.
  // Created as a timer-bar category (the common case); it styles/pops out
  // from Manage Overlays like any other.
  let newCatOpen = false;
  let newCatName = "";
  let newCatErr = "";
  async function createCatInline() {
    const name = (newCatName || "").trim();
    if (!name) return;
    try {
      await CreateTriggerCategory("timers", name, "");
    } catch (e) {
      const msg = String(e).replace(/^Error:\s*/i, "");
      // Already existing is fine — the goal is selecting it on this trigger.
      if (!/already exists/i.test(msg)) {
        newCatErr = msg;
        return;
      }
    }
    newCatErr = "";
    await loadCatNames();
    form.category = name;
    newCatOpen = false;
    newCatName = "";
  }
  // Opens the picker, copies the file in, refreshes the list, and returns its
  // name (or "") for the TriggerActions component to select.
  async function addMediaFile() {
    try {
      const n = await AddTriggerMediaFile();
      if (n) await loadMediaFiles();
      return n || "";
    } catch (e) {
      formErr = String(e);
      return "";
    }
  }
  function sampleMediaFile(name) {
    if (name) PlayTriggerMediaSample(name);
  }

  // Which pane of the timer section is showing (Timer / Ending / Ended).
  let timerTab = "timer";

  // Import-from-GINA dialog. gina = { path, scanning, err, groups[], importing }.
  let gina = null;
  $: ginaSelectable = gina ? gina.groups.filter((g) => !g.excluded) : [];
  $: ginaAllSelected =
    ginaSelectable.length > 0 && ginaSelectable.every((g) => g.sel);
  $: ginaAnySelected = ginaSelectable.some((g) => g.sel);

  async function openGinaImport() {
    let path = "";
    try {
      path = await DefaultGINAConfigPath();
    } catch {
      /* no default */
    }
    gina = {
      path: path || "",
      scanning: false,
      err: "",
      groups: [],
      importing: false,
    };
    if (gina.path) await scanGina();
  }
  async function scanGina() {
    if (!gina || !gina.path.trim()) return;
    gina.scanning = true;
    gina.err = "";
    gina.groups = [];
    gina = gina;
    try {
      const res = await ScanGINAConfig(gina.path);
      if (!res || !res.valid) {
        gina.err = (res && res.error) || "Not a valid GINA configuration.";
      } else {
        gina.groups = (res.groups || []).map((g) => ({ ...g, sel: false }));
      }
    } catch (e) {
      gina.err = String(e);
    }
    gina.scanning = false;
    gina = gina;
  }
  async function browseGina() {
    try {
      const p = await BrowseGINAConfig();
      if (p) {
        gina.path = p;
        await scanGina();
      }
    } catch (e) {
      gina.err = String(e);
      gina = gina;
    }
  }
  function toggleAllGina(e) {
    const v = e.target.checked;
    gina.groups = gina.groups.map((g) => (g.excluded ? g : { ...g, sel: v }));
  }
  async function importGina() {
    const ids = gina.groups
      .filter((g) => g.sel && !g.excluded)
      .map((g) => g.group_id);
    if (!ids.length) {
      gina.err = "Select at least one group to import.";
      gina = gina;
      return;
    }
    gina.importing = true;
    gina.err = "";
    gina = gina;
    try {
      const n = await ImportGINAGroups(gina.path, ids);
      gina = null;
      importMsg = `Imported ${n} trigger group${n === 1 ? "" : "s"} under Personal.`;
      setTimeout(() => (importMsg = ""), 6000);
      await loadTree();
    } catch (e) {
      gina.err = String(e);
      gina.importing = false;
      gina = gina;
    }
  }

  function blankAction() {
    return {
      use_text: false,
      display_text: "",
      use_tts: false,
      tts_interrupt: false,
      tts_text: "",
      play_media: false,
      media_file: "",
    };
  }
  function blankForm() {
    return {
      id: 0,
      group_id: 0,
      name: "",
      trigger_text: "",
      enable_regex: true,
      on_match: blankAction(),
      copy_clipboard: false,
      clipboard_text: "",
      use_counter: false,
      counter_reset_seconds: 0,
      category: "Default",
      timer_enabled: false,
      timer_name: "",
      timer_seconds: 30,
      timer_visible_seconds: 0,
      timer_start_behavior: "StartNewTimer",
      early_enders: [],
      ending_enabled: false,
      ending_seconds: 3,
      ending: blankAction(),
      ended_enabled: false,
      ended: blankAction(),
      unsupported: false,
    };
  }
  // Deep-copy the nested action objects so editing the form doesn't mutate the
  // tree node in place (and a cancel truly cancels).
  function cloneForm(src) {
    return {
      ...src,
      on_match: { ...(src.on_match || blankAction()) },
      ending: { ...(src.ending || blankAction()) },
      ended: { ...(src.ended || blankAction()) },
      early_enders: (src.early_enders || []).map((e) => ({ ...e })),
    };
  }

  // Early End Conditions: extra searches that end this trigger's timer ahead of
  // schedule (GINA's TimerEarlyEnders). Editable only while a timer is set.
  function addEnder() {
    form.early_enders = [
      ...(form.early_enders || []),
      { text: "", regex: true },
    ];
  }
  function removeEnder(i) {
    form.early_enders = form.early_enders.filter((_, j) => j !== i);
  }

  function menuEditTrigger() {
    form = cloneForm(menu.target);
    formIsNew = false;
    formErr = "";
    timerTab = "timer";
    loadMediaFiles();
    loadCatNames();
    closeMenu();
  }
  // Share one trigger with another player (any trigger row, including Fuse
  // ones a non-officer can't edit — single triggers are shareable, groups
  // never are).
  let shareTrig = null;
  function menuShareTrigger() {
    shareTrig = menu.target;
    closeMenu();
  }
  function menuNewTrigger() {
    form = blankForm();
    formIsNew = true;
    formGroupId = menu.target.id;
    formErr = "";
    timerTab = "timer";
    loadMediaFiles();
    loadCatNames();
    closeMenu();
  }
  async function menuDeleteTrigger() {
    const t = menu.target;
    closeMenu();
    const ok = await confirmDialog({
      title: "Delete trigger",
      message: `Delete "${t.name}"?`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await DeleteTrigger(t.id);
      await loadTree();
    } catch (e) {
      importErr = String(e);
    }
  }
  function menuRenameGroup() {
    const g = menu.target;
    closeMenu();
    openPrompt("Rename group", g.name, async (name) => {
      await RenameTriggerGroup(g.id, name);
      await loadTree();
    });
  }
  function menuNewGroup() {
    const g = menu.target;
    closeMenu();
    openPrompt("New group name", "", async (name) => {
      await CreateTriggerGroup(g.id, name);
      expanded[g.id] = true;
      expanded = expanded;
      await loadTree();
    });
  }
  async function menuDeleteGroup() {
    const g = menu.target;
    closeMenu();
    const n = g.total_triggers || 0;
    const ok = await confirmDialog({
      title: "Delete group",
      message: `Delete "${g.name}"?`,
      detail: n
        ? `The ${n} trigger${n === 1 ? "" : "s"} inside it will be deleted too.`
        : "",
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await DeleteTriggerGroup(g.id);
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 6000);
    }
  }

  async function saveForm() {
    formErr = "";
    if (!form.name.trim()) {
      formErr = "Name is required.";
      return;
    }
    if (!form.trigger_text.trim()) {
      formErr = "Search text / regex is required.";
      return;
    }
    const intOr = (v, min) => Math.max(min, Math.round(Number(v) || 0));
    form.timer_seconds = intOr(form.timer_seconds, 0);
    form.timer_visible_seconds = intOr(form.timer_visible_seconds, 0);
    form.counter_reset_seconds = intOr(form.counter_reset_seconds, 0);
    form.ending_seconds = intOr(form.ending_seconds, 1);
    try {
      if (formIsNew) {
        const id = await CreateTrigger(formGroupId, form);
        highlightId = id;
      } else {
        await SaveTrigger(form);
        highlightId = form.id;
      }
      form = null;
      await loadTree();
    } catch (e) {
      formErr = String(e);
    }
  }

  // Activity path click → edit view, expand ancestors, highlight the trigger.
  function findTriggerPath(nodes, id, trail = []) {
    for (const g of nodes) {
      const here = [...trail, g.id];
      for (const t of g.triggers || []) {
        if (t.id === id) return here;
      }
      const deeper = findTriggerPath(g.groups || [], id, here);
      if (deeper) return deeper;
    }
    return null;
  }
  async function openInTree(triggerId) {
    await setView("edit");
    if (!treeLoaded) await loadTree();
    searchQ = ""; // a live search filter would hide the target
    const trail = findTriggerPath(tree, triggerId);
    if (trail) {
      for (const gid of trail) expanded[gid] = true;
      expanded = expanded;
    }
    highlightId = triggerId;
    await tick();
    const el = document.getElementById(`trig-${triggerId}`);
    if (el) el.scrollIntoView({ block: "center" });
  }

  function animLoop() {
    now = Date.now();
    animReq = requestAnimationFrame(animLoop);
  }

  onMount(async () => {
    await poll();
    await loadCategories(); // category colors for the live board
    // Manage Timers is the default view, so its data loads up front (the
    // other views lazy-load via setView). The cached tree shows immediately;
    // syncNow's delayed refresh below swaps in the freshly-synced set.
    await loadChars();
    await loadTree();
    await syncNow(); // pull the latest Fuse Triggers from the server on open
    // Reflect the real overlay state so the buttons are correct after a remount.
    try {
      winHidden = await ArePopoutsManuallyHidden();
      winLocked = await ArePopoutsLocked();
      titleMode = await GetOverlayTitleMode();
      winSnap = await GetSnapToGrid();
      winFocusHide = await GetHideOverlaysUnfocused();
    } catch {
      /* defaults */
    }
    await loadOverlayDefaults();
    offOvDefaults = Events.On("overlay-defaults-changed", loadOverlayDefaults);
    // Push: refresh the instant a trigger fires. The interval stays as a safety
    // net (missed event, or a change with no event).
    offTriggers = Events.On("triggers-changed", poll);
    // Popping out an overlay force-clears the global hide on the Go side —
    // keep the Hide/Show button label in sync.
    offUnhidden = Events.On("popouts-unhidden", () => (winHidden = false));
    pollTimer = setInterval(poll, 1000);
    // Server-wide board: light polls — everything between them extrapolates.
    pollWorld();
    pollGameClock();
    loadAlarms();
    worldTimer = setInterval(pollWorld, 10000);
    gameClockTimer = setInterval(pollGameClock, 30000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    clearInterval(worldTimer);
    clearInterval(gameClockTimer);
    if (offTriggers) offTriggers();
    if (offUnhidden) offUnhidden();
    if (offOvDefaults) offOvDefaults();
    clearTimeout(ovMsgTimer);
    if (animReq) cancelAnimationFrame(animReq);
  });
</script>

<svelte:window on:click={closeMenu} />

<div class="trig-tab">
  <div class="bar">
    <span class="title">Timers</span>
    {#if state.character}
      <span class="who">for {state.character}</span>
    {/if}
    <div class="subtabs">
      {#each PAGES as p}
        <button
          class="subtab"
          class:on={view === p.id}
          on:click={() => setView(p.id)}>{p.label}</button
        >
      {/each}
    </div>
    {#if importMsg}<span class="imp-ok" transition:fade>{importMsg}</span>{/if}
    {#if importErr}<span class="imp-err" transition:fade>{importErr}</span>{/if}
    <div class="bar-right">
      {#if view === "edit" && meta.linked && !meta.officer}
        <span class="ro-note" title="Only officers can edit Fuse Triggers"
          >Fuse Triggers are read-only</span
        >
      {/if}
    </div>
  </div>

  {#if alertShown}
    <div
      class="alert"
      style="color:{alertColor}; border-color:{alertColor}"
      transition:fade={{ duration: 250 }}
    >
      {state.alert.text}
    </div>
  {/if}

  <!-- Overlay controls, pinned above the content on every sub-tab: these act
       on the overlay windows themselves (lock/hide/snap/titles), which you
       want reachable whichever page you're on — especially mid-raid from
       Current Timers. -->
  <div class="ov-controls ov-controls-pinned">
    <button
      class="btn"
      class:on={winLocked}
      title="Lock trigger overlays: click-through and non-movable (the map stays interactive). Unlock here to regain control."
      on:click={toggleLockWindows}
      >{winLocked ? "Unlock overlays" : "Lock overlays"}</button
    >
    <button
      class="btn"
      class:on={winHidden}
      title="Hide or restore all overlay windows"
      on:click={toggleHideWindows}
      >{winHidden ? "Show overlays" : "Hide overlays"}</button
    >
    <button
      class="btn"
      class:on={winSnap}
      title="Snap overlays to a 10px grid when moving or resizing them"
      on:click={toggleSnapGrid}>Snap to grid</button
    >
    <button
      class="btn"
      class:on={winFocusHide}
      title="Hide all overlays whenever the active window is any application other than EverQuest or Fuse Bridge; they reappear the moment EQ is focused again"
      on:click={toggleFocusHide}>Hide when EQ inactive</button
    >
    <label
      class="ov-titles"
      title="When overlay title bars are shown. Hidden bars keep their height so timer bars don't shift."
    >
      <span>Overlay Titles</span>
      <select class="in" bind:value={titleMode} on:change={changeTitleMode}>
        <option value="always">Always Visible</option>
        <option value="locked">Hide When Locked</option>
        <option value="zero">Hide When 0 Triggers</option>
      </select>
    </label>
  </div>

  <div class="main">
    {#if view === "live"}
      {#if world && world.enabled}
        <div class="world">
          <button
            class="w-title"
            on:click={toggleWorld}
            aria-expanded={worldOpen}
          >
            <svg
              class="w-chev"
              class:w-chev-open={worldOpen}
              viewBox="0 0 24 24"
              width="11"
              height="11"
              fill="none"
              stroke="currentColor"
              stroke-width="2.5"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path d="M9 6l6 6-6 6" />
            </svg>
            <span>Server Timers</span>
            {#if !worldOpen && gameClock && gameClock.have_game}
              <span class="w-title-sub">{fmtGameClock(gameClock, now)}</span>
            {/if}
          </button>
          {#if worldOpen}
            <div class="w-body" transition:slide|local={{ duration: 150 }}>
              <!-- Game Time: the shared in-game day, hour ticks along the track -->
              <div class="w-sec">
                <div class="w-head">
                  <span class="w-dot"></span>
                  <span class="w-sec-name">Game Time</span>
                  {#if gameClock && gameClock.have_game}
                    <span class="w-sub"
                      >{fmtGameClock(gameClock, now)} · next hour in {fmtRemain(
                        nextGameHourIn(gameClock, now),
                      )}</span
                    >
                  {/if}
                </div>
                {#if gameClock && gameClock.have_game}
                  {@const curTick = currentHourInfo(gameClock, now)}
                  {@const nextTick = nextHourInfo(gameClock, now)}
                  <div class="w-day">
                    <div
                      class="w-day-fill"
                      style="width:{(gameHourFloat(gameClock, now) / 24) *
                        100}%"
                    ></div>
                    {#each hourTicks as h}
                      <div
                        class="w-tick"
                        class:w-tick-now={curTick && h === curTick.hour}
                        class:w-tick-next={nextTick && h === nextTick.hour}
                        style="left:{(h / 24) * 100}%"
                      ></div>
                    {/each}
                    <span class="w-day-edge">12 AM</span>
                    <span class="w-day-edge w-day-edge-r">12 AM</span>
                    <!-- Next first, current second: at the top of an hour the two
                     badges sit one tick apart, and the current one is the one
                     that should be legible if they ever overlap. -->
                    {#if nextTick}
                      <span
                        class="w-hour-badge w-hour-next"
                        title="Next game hour"
                        style="left:clamp(9px, {nextTick.pos}%, calc(100% - 9px))"
                        >{nextTick.label}</span
                      >
                    {/if}
                    {#if curTick}
                      <span
                        class="w-hour-badge w-hour-now"
                        title="Current game hour"
                        style="left:clamp(9px, {curTick.pos}%, calc(100% - 9px))"
                        >{curTick.label}</span
                      >
                    {/if}
                  </div>
                {:else}
                  <div class="w-empty">
                    No game-time samples yet — run /time in game.
                  </div>
                {/if}
              </div>

              <!-- Events: Scout Charisa / Ring 8 from Gynok in #bot_command_space -->
              <div class="w-sec">
                <div class="w-head">
                  <span class="w-dot"></span>
                  <span class="w-sec-name">Events</span>
                </div>
                {#each world.events || [] as e (e.key)}
                  {@const ak = "event:" + e.key}
                  <div class="w-alarmrow">
                    {#if e.have}
                      {@const v = evView(e, now)}
                      <div
                        class="tbar w-bar"
                        class:w-inwin={v.inWin}
                        title={evTitle(e, v, now)}
                      >
                        <div
                          class="tbar-fill w-fill"
                          style="width:{v.frac * 100}%"
                        />
                        <span class="tbar-name">{e.name}</span>
                        <!-- Windowed events count to the window OPENING, then
                             show the window draining until it closes. -->
                        <span class="tbar-time"
                          >{v.win > 0
                            ? v.inWin
                              ? "WINDOW " + fmtLong(v.close - now)
                              : fmtLong(v.open - now)
                            : fmtLong(v.at - now)}{#if v.marks > 0}<span
                              class="w-marks"
                              >{"?".repeat(Math.min(v.marks, 4))}</span
                            >{/if}</span
                        >
                      </div>
                    {:else}
                      <div class="tbar w-bar w-bar-none">
                        <span class="tbar-name">{e.name}</span>
                        <span class="tbar-time w-none-txt">no report yet</span>
                      </div>
                    {/if}
                    <AlarmBell
                      active={!!alarms[ak]}
                      title={alarmTip(alarms[ak])}
                      on:click={() => openAlarm(ak, e.name)}
                    />
                  </div>
                {/each}

                <!-- Last earthquake: an indicator, not a countdown. Alarmable,
                     but only ever after the fact — nobody can schedule one. -->
                <div class="w-alarmrow">
                  <div
                    class="tbar w-bar w-bar-none"
                    title={quakeAgo
                      ? "The world last shook at " +
                        new Date(world.last_quake_ms).toLocaleString()
                      : "No earthquake on record yet"}
                  >
                    <span class="tbar-name">Last Earthquake</span>
                    <span class="tbar-time" class:w-none-txt={!quakeAgo}
                      >{quakeAgo ? quakeAgo + " ago" : "none on record"}</span
                    >
                  </div>
                  <AlarmBell
                    active={!!alarms["quake"]}
                    title={alarmTip(alarms["quake"])}
                    on:click={() =>
                      openAlarm("quake", "Earthquake", "occurrence")}
                  />
                </div>
              </div>

              <!-- Boats: one sighting anywhere pins the whole loop -->
              <div class="w-sec">
                <div class="w-head">
                  <span class="w-dot"></span>
                  <span class="w-sec-name">Boats</span>
                </div>
                <!-- 2×2: Oasis→TD / TD→OT on the left, BB→FV / NRO→IC on
                     the right (boatOrder fixes the fill order). -->
                <div class="w-boats">
                  {#each boatOrder(world.boats) as b (b.key)}
                    {#if b.have}
                      {@const v = boatView(b, now)}
                      <div
                        class="w-boat"
                        title="{b.name} — last sighting {agoStr(
                          now - b.seen_ms,
                        )}{boatWarn(b, now)}"
                      >
                        <div class="w-route">
                          <AlarmBell
                            active={!!alarms["boat:" + b.key + ":a"]}
                            title={alarmTip(alarms["boat:" + b.key + ":a"])}
                            on:click={() =>
                              openAlarm(
                                "boat:" + b.key + ":a",
                                b.name + " at " + b.end_a,
                              )}
                          />
                          <span class="w-port">{b.end_a}</span>
                          <div class="w-track">
                            <div class="w-track-line"></div>
                            {#if v.pos != null}
                              <svg
                                class="w-ship"
                                class:w-ship-flip={v.dir < 0}
                                style="left:{v.pos * 100}%"
                                viewBox="0 0 24 24"
                                width="24"
                                height="24"
                                fill="none"
                                stroke="currentColor"
                                stroke-width="2"
                                stroke-linecap="round"
                                stroke-linejoin="round"
                              >
                                <!-- faces right: mast forward (~1/3 from the bow),
                               mainsail sweeping aft — sails trail, not lead -->
                                <path d="M3 16h18l-2 4H5z" />
                                <path d="M15 3v13" />
                                <path d="M15 3L7 13h8" />
                              </svg>
                            {/if}
                          </div>
                          <span class="w-port w-port-r">{b.end_b}</span>
                          <AlarmBell
                            active={!!alarms["boat:" + b.key + ":b"]}
                            title={alarmTip(alarms["boat:" + b.key + ":b"])}
                            on:click={() =>
                              openAlarm(
                                "boat:" + b.key + ":b",
                                b.name + " at " + b.end_b,
                              )}
                          />
                        </div>
                        <div class="w-eta">
                          <span class="w-eta-t"
                            >{v.a > 0 ? fmtLong(v.a - now) : "—"}</span
                          >
                          <span class="w-boat-name"
                            >{b.name}{#if boatStale(b, now)}<span
                                class="w-stale"
                                class:red={boatStale(b, now) === "red"}
                                title="Last sighting {agoStr(
                                  now - b.seen_ms,
                                )}{boatWarn(b, now)}">⚑</span
                              >{/if}</span
                          >
                          <span class="w-eta-t"
                            >{v.b > 0 ? fmtLong(v.b - now) : "—"}</span
                          >
                        </div>
                      </div>
                    {:else}
                      <div
                        class="w-boat w-boat-none"
                        title="Waiting for a dock announcement from anyone running the relay in {b.end_a} or {b.end_b}"
                      >
                        <div class="w-route">
                          <AlarmBell
                            active={!!alarms["boat:" + b.key + ":a"]}
                            title={alarmTip(alarms["boat:" + b.key + ":a"])}
                            on:click={() =>
                              openAlarm(
                                "boat:" + b.key + ":a",
                                b.name + " at " + b.end_a,
                              )}
                          />
                          <span class="w-port">{b.end_a}</span>
                          <div class="w-track">
                            <div class="w-track-line"></div>
                          </div>
                          <span class="w-port w-port-r">{b.end_b}</span>
                          <AlarmBell
                            active={!!alarms["boat:" + b.key + ":b"]}
                            title={alarmTip(alarms["boat:" + b.key + ":b"])}
                            on:click={() =>
                              openAlarm(
                                "boat:" + b.key + ":b",
                                b.name + " at " + b.end_b,
                              )}
                          />
                        </div>
                        <div class="w-eta">
                          <span class="w-eta-t">—</span>
                          <span class="w-boat-name"
                            >{b.name} · no sighting yet</span
                          >
                          <span class="w-eta-t">—</span>
                        </div>
                      </div>
                    {/if}
                  {/each}
                </div>
                <!-- The Boat Trip Recorder (calibration for these lines) is an
                     admin tool and lives in General > Admin Settings. -->
              </div>
            </div>
          {/if}
        </div>
      {/if}
      <div class="live-head">Your Enabled Timers</div>
      {#if cats.length === 0}
        <div class="hint">
          No active timers — countdown bars appear here when a trigger fires.
        </div>
      {/if}
      {#each cats as c (c.name)}
        <div class="cat" transition:slide|local={{ duration: 150 }}>
          <div class="cat-head">
            <button
              class="cat-toggle"
              on:click={() => toggleLiveCat(c.name)}
              aria-expanded={!catClosed[c.name]}
            >
              <svg
                class="w-chev"
                class:w-chev-open={!catClosed[c.name]}
                viewBox="0 0 24 24"
                width="11"
                height="11"
                fill="none"
                stroke="currentColor"
                stroke-width="2.5"
                stroke-linecap="round"
                stroke-linejoin="round"
              >
                <path d="M9 6l6 6-6 6" />
              </svg>
              <span class="cat-dot" style="background:{c.color}"></span>
              <span class="cat-name">{c.name}</span>
              {#if catClosed[c.name]}
                <span class="cat-count"
                  >{c.timers.length} timer{c.timers.length === 1
                    ? ""
                    : "s"}</span
                >
              {/if}
            </button>
            <div class="cat-actions">
              <button
                class="cat-btn"
                title="Dismiss all “{c.name}” timers"
                aria-label="Dismiss all {c.name} timers"
                on:click={() => dismissCat(c)}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="12"
                  height="12"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" />
                </svg>
              </button>
              <button
                class="cat-btn"
                title="Pop out “{c.name}” as an overlay"
                aria-label="Pop out {c.name}"
                on:click={() => OpenPopout("timers", c.name)}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="12"
                  height="12"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path
                    d="M10 6H6a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4"
                  />
                  <path d="M14 4h6v6" />
                  <path d="M20 4 12 12" />
                </svg>
              </button>
            </div>
          </div>
          {#if !catClosed[c.name]}
            <div transition:slide|local={{ duration: 150 }}>
              {#each c.timers as t (t.id)}
                <div class="tbar">
                  <div
                    class="tbar-fill"
                    style="width:{barFrac(t) * 100}%; background:{c.color}"
                  ></div>
                  <span class="tbar-name">{t.name}</span>
                  <span class="tbar-time">{fmtRemain(t.ends_at_ms - now)}</span>
                  <button
                    class="tbar-trash"
                    title="Dismiss this timer"
                    aria-label="Dismiss timer"
                    on:click={() => dismissTimer(t)}
                  >
                    <svg
                      viewBox="0 0 24 24"
                      width="12"
                      height="12"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    >
                      <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" />
                    </svg>
                  </button>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/each}
    {:else if view === "overlays"}
      <div class="ov-intro">
        Pop out any category as an overlay you can position over the game.
        Position and size are saved per character, starting from your default
        layout. Colors and fonts belong to the category itself, so they're the
        same on every character. Click a category to see its triggers;
        right-click to edit or delete it.
      </div>

      {#if ovDef.editing}
        <!-- Loud on purpose: everything below now edits the default, and the
             cost of not noticing is arranging the wrong character's overlays. -->
        <div class="ov-def ov-def-editing">
          <div class="ovd-head">
            <span class="ovd-badge">EDITING DEFAULTS</span>
            <span class="ovd-title"
              >You are arranging the <b>default overlay layout</b></span
            >
          </div>
          <div class="ovd-body">
            The overlays on screen are the default set — not {ovDef.char ||
              "your character"}'s. Pop out, move, resize and style them the way
            a fresh character should start, then finish. Every character with no
            layout of their own begins from this.
          </div>
          <div class="ovd-actions">
            <button
              class="btn ovd-go"
              disabled={ovBusy}
              on:click={toggleDefaultEditing}>Done editing defaults</button
            >
            <span class="ovd-count"
              >{ovDef.count} overlay{ovDef.count === 1 ? "" : "s"} in the default</span
            >
          </div>
        </div>
      {:else if !ovDef.configured}
        <!-- First-run call to action: set the default up BEFORE fiddling with
             one character, or that work has to be redone for every alt. -->
        <div class="ov-def ov-def-cta">
          <div class="ovd-head">
            <span class="ovd-step">START HERE</span>
            <span class="ovd-title">Set up a default overlay layout first</span>
          </div>
          <div class="ovd-body">
            Overlays are saved per character. Build the layout you want <i
              >every</i
            >
            character to start with once, and each new one inherits it — otherwise
            you'll be arranging the same windows again for every alt. You can still
            move any character's overlays afterwards; those changes stay with that
            character.
          </div>
          <div class="ovd-actions">
            <button
              class="btn ovd-go"
              disabled={ovBusy}
              on:click={toggleDefaultEditing}>Configure default overlays</button
            >
            {#if ovDef.char && ovDef.char_set}
              <button
                class="btn"
                disabled={ovBusy}
                title="Copy {ovDef.char}'s current overlay layout into the default"
                on:click={saveCharAsDefault}
                >Use {ovDef.char}'s current layout</button
              >
            {/if}
          </div>
        </div>
      {:else}
        <div class="ov-def">
          <div class="ovd-actions">
            <span class="ovd-title ovd-inline"
              >Default layout: <b
                >{ovDef.count} overlay{ovDef.count === 1 ? "" : "s"}</b
              ></span
            >
            <button
              class="btn"
              disabled={ovBusy}
              on:click={toggleDefaultEditing}>Edit defaults</button
            >
            {#if ovDef.char}
              <button
                class="btn"
                disabled={ovBusy}
                title="Replace the default with {ovDef.char}'s current overlay layout"
                on:click={saveCharAsDefault}
                >Save {ovDef.char}'s layout as default</button
              >
              {#if ovConfirmReset}
                <button
                  class="btn ovd-danger"
                  disabled={ovBusy}
                  on:click={resetCharToDefault}
                  >Confirm: discard {ovDef.char}'s layout</button
                >
                <button class="btn" on:click={() => (ovConfirmReset = false)}
                  >Cancel</button
                >
              {:else}
                <button
                  class="btn"
                  disabled={ovBusy}
                  title="Discard {ovDef.char}'s overlay positions, sizes and styling, and start them over from the default"
                  on:click={() => (ovConfirmReset = true)}
                  >Reset {ovDef.char} to Default</button
                >
              {/if}
            {/if}
          </div>
        </div>
      {/if}
      {#if ovMsg || ovErr}
        <div class="ovd-note" class:err={ovErr}>{ovErr || ovMsg}</div>
      {/if}

      <!-- Special Overlays: the map + live raid-card sections. They keep their
           in-app styling; background color/opacity is set from each overlay's
           own ⚙ settings panel. Most are guild-only — they render server raid
           data an unlinked client never receives — so an unlinked user sees
           only the ones flagged `local`. -->
      {#if visibleSpecials.length}
        <div class="ov-sec-head">
          <span class="ov-sec-title">Special Overlays</span>
        </div>
        <div class="ov-list">
          {#each visibleSpecials as so (so.kind)}
            <!-- Right-click for the overlay's settings, matching the category
                 cards above. -->
            <!-- svelte-ignore a11y-no-static-element-interactions -->
            <div
              class="ov-card sp"
              on:contextmenu|preventDefault|stopPropagation={(e) =>
                onMenu(e, "special", so)}
            >
              <div class="sp-txt">
                <span class="ov-name sp-name" title={so.name}>{so.name}</span>
                <span class="sp-desc">{so.desc}</span>
              </div>
              {#if isPopped(so.kind, "")}
                <button
                  class="btn ov-pop ov-remove"
                  title="Close the “{so.name}” overlay"
                  on:click={() => ClosePopout(so.kind, "")}>Remove</button
                >
              {:else}
                <button
                  class="btn ov-pop"
                  title="Pop out “{so.name}” as an overlay"
                  on:click={() => OpenPopout(so.kind, "")}>Pop out</button
                >
              {/if}
            </div>
          {/each}
        </div>
      {/if}

      {#each [{ kind: "timers", title: "Timer Bars", cats: timerCats, empty: "No triggers start countdown timers." }, { kind: "alerts", title: "Text Alerts", cats: alertCats, empty: "No triggers show text alerts." }] as sec (sec.kind)}
        <div class="ov-sec-head">
          <span class="ov-sec-title">{sec.title}</span>
          <button
            class="ov-add"
            title="New {sec.kind === 'alerts'
              ? 'text alert'
              : 'timer bar'} category"
            aria-label="New {sec.title} category"
            on:click={() => newCatForm(sec.kind)}>+</button
          >
        </div>
        <div class="ov-list">
          {#each sec.cats as c (c.name)}
            <!-- Left-click expands this category's triggers; right-click edits
                 or deletes it. -->
            <div
              class="ov-card"
              class:open={openCat === catKey(c)}
              role="button"
              tabindex="0"
              on:click={() => toggleCat(c)}
              on:keydown={(e) => e.key === "Enter" && toggleCat(c)}
              on:contextmenu|preventDefault|stopPropagation={(e) =>
                onMenu(e, "category", c)}
            >
              <span class="ov-caret">{openCat === catKey(c) ? "▾" : "▸"}</span>
              <!-- Alerts have no bar; their color is the text color. -->
              <span
                class="cat-dot"
                style="background:{styleColor(sec.kind, c.name)}"
              ></span>
              <span class="ov-name" title={c.name}>{c.name}</span>
              <span
                class="ov-count"
                title="{c.enabled} of {c.count} triggers enabled for this character"
                >{c.enabled}/{c.count}</span
              >
              <!-- Same edit the right-click menu offers, as a visible button —
                   a context menu is not discoverable enough for this. -->
              <button
                class="btn ov-gear"
                title="Edit “{c.name}” — colors, font, rename"
                aria-label="Edit {c.name}"
                on:click|stopPropagation={() => editCatForm(c)}>⚙</button
              >
              {#if isPopped(sec.kind, c.name)}
                <button
                  class="btn ov-pop ov-remove"
                  title="Close the “{c.name}” overlay"
                  on:click|stopPropagation={() => ClosePopout(sec.kind, c.name)}
                  >Remove</button
                >
              {:else}
                <button
                  class="btn ov-pop"
                  title="Pop out “{c.name}” as an overlay"
                  on:click|stopPropagation={() => OpenPopout(sec.kind, c.name)}
                  >Pop out</button
                >
              {/if}
            </div>
            {#if openCat === catKey(c)}
              <div class="ov-tree" transition:slide|local={{ duration: 150 }}>
                {#each catTree as g (g.id)}
                  <TriggerNode
                    node={g}
                    expanded={catExpanded}
                    {highlightId}
                    onToggle={onCatToggle}
                    {onMenu}
                    {onToggleEnable}
                    {onToggleMute}
                    {onToggleClip}
                    onPopoutCat={(kind, cat) => OpenPopout(kind, cat)}
                    popped={poppedSet}
                  />
                {:else}
                  <div class="hint">
                    No triggers are assigned to this category yet.
                  </div>
                {/each}
              </div>
            {/if}
          {:else}
            <div class="hint">{sec.empty}</div>
          {/each}
        </div>
      {/each}
    {:else}
      {#if meta.officer && fuseRootNode && fuseRootNode.dirty}
        <!-- Officer revision bar: local Fuse edits await an explicit publish. -->
        <div class="pub-bar">
          <span class="pub-msg">
            Your Fuse Trigger changes (based on v{fuseRootNode.version}) are not
            published yet — only you can see them.
            {#if fuseRootNode.server_version > fuseRootNode.version}
              <span class="pub-warn"
                >v{fuseRootNode.server_version} was published by another officer
                in the meantime; publishing now will overwrite it.</span
              >
            {/if}
          </span>
          <button
            class="btn save"
            disabled={publishing}
            on:click={publishTriggers}>Publish to guild</button
          >
          <button class="btn" disabled={publishing} on:click={revertTriggers}
            >Discard my changes</button
          >
        </div>
      {/if}
      <!-- Overlay setup call-to-action: timers are only useful once the
           overlays showing them are positioned. Shown until the first
           overlay layout ever exists, then latched off for good (CTA_KEY). -->
      {#if !ctaDone}
        <div class="ov-cta">
          <span class="ov-cta-msg">
            <strong>New to timers?</strong> Check that you have some timers enabled
            you want to use and then set up your default overlays — position each
            overlay once and every character starts from that layout.
          </span>
          <button class="btn ov-cta-btn" on:click={() => setView("overlays")}
            >Open Manage Overlays →</button
          >
        </div>
      {/if}
      {#if defaultsMode}
        <!-- Defaults editor: everything toggled here lands on EVERY character
             (and the seed for future ones) — only on an explicit, confirmed
             save. Class-specific stays auto-detected, marked in the tree. -->
        <div class="def-bar">
          <span class="def-badge">EDITING DEFAULTS</span>
          <span class="def-msg">
            These selections will apply to <b>all characters</b>. The class
            section stays auto-detected per character.
          </span>
          <button class="btn save" on:click={saveDefaults}
            >Save for all characters</button
          >
          <button class="btn" on:click={cancelDefaults}>Cancel</button>
        </div>
      {:else}
        <div class="char-row">
          <span class="char-label">Configuring triggers for</span>
          <select
            class="in char-sel"
            value={shownChar}
            on:change={(e) => pickChar(e.target.value)}
          >
            {#each chars as c (c.name)}
              <option value={c.name}>
                {c.name}{c.class ? ` — ${c.class}` : ""}{c.current
                  ? " (playing)"
                  : ""}
              </option>
            {:else}
              <option value="">{state.character || "No character"}</option>
            {/each}
          </select>
          <button
            class="btn"
            title="Edit one enable/disable selection and apply it to every character"
            on:click={beginDefaults}>Configure Defaults</button
          >
          {#if editChar && editChar.toLowerCase() !== (state.character || "").toLowerCase()}
            <span class="char-note"
              >Editing another character — changes apply to {editChar} only.</span
            >
          {/if}
        </div>
      {/if}
      <div class="search-row">
        <input
          class="in search-in"
          placeholder="Search names & regex…"
          bind:value={searchQ}
          on:keydown={(e) => e.key === "Enter" && stepMatch(1)}
        />
        <span class="match-count">
          {matchIds.length
            ? `${matchIdx + 1}/${matchIds.length}`
            : q
              ? "0 results"
              : ""}
        </span>
        <button
          class="btn"
          title="Previous match"
          disabled={!matchIds.length}
          on:click={() => stepMatch(-1)}>↑</button
        >
        <button
          class="btn"
          title="Next match"
          disabled={!matchIds.length}
          on:click={() => stepMatch(1)}>↓</button
        >
      </div>
      <div class="tree">
        {#each shownTree as g (g.id)}
          <TriggerNode
            node={g}
            expanded={q ? searchExpanded : expanded}
            {highlightId}
            query={q}
            {onToggle}
            {onMenu}
            {onToggleEnable}
            {onToggleMute}
            {onToggleClip}
            onPopoutCat={(kind, cat) => OpenPopout(kind, cat)}
            popped={poppedSet}
            onFuseExport={meta.officer ? exportFuseXML : null}
            onFuseImport={meta.officer ? beginFuseImport : null}
          />
        {:else}
          <div class="hint">
            {q ? "No triggers match your search." : "No trigger groups."}
          </div>
        {/each}
      </div>
      <div class="tree-foot">
        <button class="btn gina-btn" on:click={openGinaImport}
          >Import from GINA…</button
        >
      </div>
    {/if}
  </div>

  <!-- locked activity feed, resizable by the grip on its top edge -->
  <div
    class="act-grip"
    class:dragging={!!actDrag}
    role="separator"
    aria-orientation="horizontal"
    title="Drag to resize the activity feed"
    on:pointerdown={actGripDown}
    on:pointermove={actGripMove}
    on:pointerup={actGripUp}
    on:pointercancel={actGripUp}
  ></div>
  <div class="activity" style="height:{actH}px">
    <div class="act-title">Activity</div>
    <div class="act-list">
      {#each state.activity || [] as a}
        <div class="act-row">
          <span class="act-time">{fmtClock(a.at_ms)}</span>
          <button class="act-path" on:click={() => openInTree(a.trigger_id)}>
            {a.path.join(" > ")}
          </button>
        </div>
      {:else}
        <div class="act-empty">No triggers have fired yet.</div>
      {/each}
    </div>
  </div>
</div>

<!-- context menu -->
{#if menu}
  <div class="ctx" style="left:{menu.x}px; top:{menu.y}px">
    {#if menu.kind === "category"}
      <button
        class="ctx-item"
        on:click={() => {
          const c = menu.target;
          closeMenu();
          editCatForm(c);
        }}>Edit</button
      >
      <button
        class="ctx-item danger"
        on:click={() => {
          const c = menu.target;
          closeMenu();
          openCatDelete(c);
        }}>Delete</button
      >
    {:else if menu.kind === "special"}
      <button
        class="ctx-item"
        on:click={() => {
          const so = menu.target;
          closeMenu();
          editSpecial(so);
        }}>Edit</button
      >
    {:else if menu.kind === "trigger"}
      {#if menu.target.editable}
        <button class="ctx-item" on:click={menuEditTrigger}>Edit</button>
      {/if}
      <button class="ctx-item" on:click={menuShareTrigger}>Share…</button>
      {#if menu.target.editable}
        <button class="ctx-item danger" on:click={menuDeleteTrigger}
          >Delete</button
        >
      {/if}
    {:else}
      <button class="ctx-item" on:click={menuRenameGroup}>Edit Name</button>
      <button class="ctx-item" on:click={menuNewTrigger}>New Trigger</button>
      <button class="ctx-item" on:click={menuNewGroup}>New Group</button>
      <button class="ctx-item danger" on:click={menuDeleteGroup}>Delete</button>
    {/if}
  </div>
{/if}

<!-- server-timer reminder dialog -->
{#if alarmEdit}
  <AlarmDialog
    alarmKey={alarmEdit.key}
    label={alarmEdit.label}
    kind={alarmEdit.kind}
    existing={alarmEdit.existing}
    on:saved={alarmSaved}
    on:close={() => (alarmEdit = null)}
  />
{/if}

<!-- share-a-trigger dialog -->
{#if shareTrig}
  <ShareDialog
    title="Share Trigger"
    previewLines={[
      shareTrig.name,
      shareTrig.trigger_text ? `Matches: ${shareTrig.trigger_text}` : "",
      shareTrig.category ? `Category: ${shareTrig.category}` : "",
    ].filter(Boolean)}
    onSend={(addr) => ShareTrigger(shareTrig.id, addr)}
    onClose={() => (shareTrig = null)}
  />
{/if}

<!-- single-input prompt -->
{#if prompt}
  <div class="overlay" on:click|self={() => (prompt = null)}>
    <div class="modal">
      <div class="modal-title">{prompt.title}</div>
      <!-- svelte-ignore a11y-autofocus -->
      <input
        class="in"
        autofocus
        bind:value={prompt.value}
        on:keydown={(e) => e.key === "Enter" && promptOK()}
      />
      <div class="modal-actions">
        <button class="btn" on:click={promptOK}>OK</button>
        <button class="btn" on:click={() => (prompt = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- special overlay settings (right-click → Edit) -->
{#if spForm}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="overlay" on:click|self={() => (spForm = null)}>
    <div class="modal">
      <div class="modal-title">{spForm.name}</div>
      <div class="sticky-box">
        <label class="sw-row">
          <span class="sw-lbl">Sticky Mode</span>
          <span class="sw">
            <input
              type="checkbox"
              checked={spForm.sticky}
              on:change={(e) => setSpecialSticky(e.target.checked)}
            />
            <span class="sw-track"><span class="sw-thumb"></span></span>
          </span>
        </label>
        <div class="sticky-warn">
          This overlay will <b>never</b> go away — it stays on screen with
          EverQuest closed and with nothing to show. The <b>Hide overlays</b>
          button above still puts it away.
        </div>
      </div>
      <div class="modal-actions">
        <button class="btn" on:click={() => (spForm = null)}>Close</button>
      </div>
    </div>
  </div>
{/if}

<!-- category style form: bar/background for timer bars, font/background for
     text alerts (a text alert has no bar to color) -->
{#if catForm}
  <div class="overlay" on:click|self={() => (catForm = null)}>
    <div class="modal form">
      <div class="modal-title">
        {catForm.isNew ? "New" : "Edit"}
        {catForm.style.kind === "alerts" ? "Text Alert" : "Timer Bar"} Category
      </div>

      <label class="f-label" for="cf-name">Name</label>
      <!-- svelte-ignore a11y-autofocus -->
      <input
        id="cf-name"
        class="in"
        autofocus
        bind:value={catForm.style.name}
        on:keydown={(e) => e.key === "Enter" && saveCatForm()}
      />
      {#if !catForm.isNew}
        <div class="f-note">
          Renaming reassigns every trigger currently in this category.
        </div>
      {/if}

      <div class="f-sep" />
      {#if catForm.style.kind === "timers"}
        <div class="f-grid">
          <span class="f-label">Bar color</span>
          <div class="f-inline">
            <input type="color" bind:value={catForm.style.bar_color} />
            <input
              type="range"
              min="0"
              max="100"
              value={Math.round(catForm.style.bar_opacity * 100)}
              on:input={(e) =>
                (catForm.style.bar_opacity = e.target.value / 100)}
            />
            <span class="f-val"
              >{Math.round(catForm.style.bar_opacity * 100)}%</span
            >
          </div>
          <span class="f-label">Bar background</span>
          <div class="f-inline">
            <input type="color" bind:value={catForm.style.bg_color} />
            <input
              type="range"
              min="0"
              max="100"
              value={Math.round(catForm.style.bg_opacity * 100)}
              on:input={(e) =>
                (catForm.style.bg_opacity = e.target.value / 100)}
            />
            <span class="f-val"
              >{Math.round(catForm.style.bg_opacity * 100)}%</span
            >
          </div>
        </div>
        <label
          class="f-chk"
          title="Pause timers for this category when the character is out of game to ensure accurate timer retention across play sessions."
        >
          <input type="checkbox" bind:checked={catForm.style.auto_pause} />
          Auto pause timers
        </label>
      {:else}
        <div class="f-grid">
          <span class="f-label">Background</span>
          <div class="f-inline">
            <input type="color" bind:value={catForm.style.bg_color} />
            <input
              type="range"
              min="0"
              max="100"
              value={Math.round(catForm.style.bg_opacity * 100)}
              on:input={(e) =>
                (catForm.style.bg_opacity = e.target.value / 100)}
            />
            <span class="f-val"
              >{Math.round(catForm.style.bg_opacity * 100)}%</span
            >
          </div>
        </div>
      {/if}

      <div class="f-sep" />
      <div class="f-grid">
        <label class="f-label" for="cf-font">Font</label>
        <select id="cf-font" class="in" bind:value={catForm.style.font_family}>
          {#each FONTS as f (f.v)}
            <option value={f.v}>{f.label}</option>
          {/each}
        </select>
        <span class="f-label">Font color</span>
        <div class="f-inline">
          <input type="color" bind:value={catForm.style.font_color} />
        </div>
        <label class="f-label" for="cf-size">Font size</label>
        <input
          id="cf-size"
          class="in num"
          type="number"
          min="6"
          max="48"
          bind:value={catForm.style.font_size}
        />
      </div>

      <div class="f-sep" />
      <span class="f-label">Preview</span>
      {#if catForm.style.kind === "timers"}
        <div
          class="cf-prev"
          style="background:{rgba(
            catForm.style.bg_color,
            catForm.style.bg_opacity,
          )}; color:{catForm.style.font_color};
                 font-size:{catForm.style.font_size}px; font-family:{catForm
            .style.font_family || 'inherit'}"
        >
          <div
            class="cf-prev-fill"
            style="background:{catForm.style.bar_color}; opacity:{catForm.style
              .bar_opacity}"
          ></div>
          <span class="cf-prev-name">{catForm.style.name || "Timer name"}</span>
          <span class="cf-prev-time">01:30</span>
        </div>
      {:else}
        <div
          class="cf-prev alert"
          style="background:{rgba(
            catForm.style.bg_color,
            catForm.style.bg_opacity,
          )}; color:{catForm.style.font_color};
                 font-size:{catForm.style.font_size}px; font-family:{catForm
            .style.font_family || 'inherit'}"
        >
          {catForm.style.name || "Alert text"}
        </div>
      {/if}

      <!-- Last, and set apart: everything above styles the CATEGORY wherever it
           appears, while this pins the one overlay. A category being created
           has no overlay to pin yet. -->
      {#if !catForm.isNew}
        <div class="sticky-box">
          <label class="sw-row">
            <span class="sw-lbl">Sticky Mode</span>
            <span class="sw">
              <input
                type="checkbox"
                checked={catSticky}
                on:change={(e) => setCatSticky(e.target.checked)}
              />
              <span class="sw-track"><span class="sw-thumb"></span></span>
            </span>
          </label>
          <div class="sticky-warn">
            This overlay will <b>never</b> go away — it stays on screen with
            EverQuest closed and with nothing to show. The
            <b>Hide overlays</b> button above still puts it away.
          </div>
        </div>
      {/if}

      {#if catFormErr}<div class="f-err">{catFormErr}</div>{/if}
      <div class="modal-actions">
        <button class="btn save" on:click={saveCatForm}>Save</button>
        <button class="btn" on:click={() => (catForm = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- category delete: triggers have to go somewhere -->
{#if catDel}
  <div class="overlay" on:click|self={() => (catDel = null)}>
    <div class="modal">
      <div class="modal-title">Delete Category</div>
      <div class="f-note">
        Delete “{catDel.name}”? Its triggers keep working — choose where they
        go. This removes the category for both its timer bar and text alert
        overlays.
      </div>
      <label class="f-label" for="cd-to">Reassign its triggers to</label>
      <select id="cd-to" class="in" bind:value={catDel.reassign}>
        <option value="">No category (flagged as needing setup)</option>
        {#each catDel.options as n (n)}
          <option value={n}>{n}</option>
        {/each}
      </select>
      <div class="modal-actions">
        <button class="btn danger" on:click={confirmCatDelete}>Delete</button>
        <button class="btn" on:click={() => (catDel = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- trigger edit/create form -->
{#if form}
  <div class="overlay" on:click|self={() => (form = null)}>
    <div class="modal form">
      <div class="modal-title">
        {formIsNew ? "New Trigger" : "Edit Trigger"}
      </div>

      <label class="f-label" for="tf-name">Name</label>
      <input id="tf-name" class="in" bind:value={form.name} />

      <label class="f-label" for="tf-text">Search text</label>
      <textarea
        id="tf-text"
        class="in mono"
        rows="2"
        bind:value={form.trigger_text}
      ></textarea>
      <label class="f-chk">
        <input type="checkbox" bind:checked={form.enable_regex} /> Regular expression
      </label>

      <label class="f-label" for="tf-cat">Category</label>
      <div class="f-catrow">
        <select id="tf-cat" class="in" bind:value={form.category}>
          {#each catOptions as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
        <button
          class="btn"
          title="Create a new category"
          on:click={() => {
            newCatOpen = !newCatOpen;
            newCatErr = "";
          }}>+ New</button
        >
      </div>
      {#if newCatOpen}
        <div class="f-catrow">
          <input
            class="in"
            placeholder="New category name"
            bind:value={newCatName}
            on:keydown={(e) => e.key === "Enter" && createCatInline()}
          />
          <button class="btn save" on:click={createCatInline}>Create</button>
        </div>
        {#if newCatErr}<div class="f-caterr">{newCatErr}</div>{/if}
      {/if}

      <div class="f-sep" />
      <div class="f-section">On Match</div>
      <TriggerActions
        bind:action={form.on_match}
        {mediaFiles}
        onAdd={addMediaFile}
        onSample={sampleMediaFile}
      />
      <label class="f-chk">
        <input type="checkbox" bind:checked={form.copy_clipboard} /> Copy to clipboard
      </label>
      {#if form.copy_clipboard}
        <input
          class="in"
          placeholder="Text to copy (supports $&#123;1&#125; captures)"
          bind:value={form.clipboard_text}
        />
      {/if}
      <label class="f-chk">
        <input type="checkbox" bind:checked={form.use_counter} /> Use counter
      </label>
      {#if form.use_counter}
        <div class="f-grid">
          <label class="f-label" for="tf-creset">Reset after (seconds)</label>
          <input
            id="tf-creset"
            class="in num"
            type="number"
            min="0"
            bind:value={form.counter_reset_seconds}
          />
        </div>
        <div class="f-note">
          Put <code>&#123;counter&#125;</code> in any alert, speech, or clipboard
          text. It counts up each time the trigger matches and resets after this
          many seconds with no match (0 = never resets).
        </div>
      {/if}
      <label class="f-chk">
        <input type="checkbox" bind:checked={form.timer_enabled} /> Start countdown
        timer
      </label>
      {#if form.timer_enabled}
        <div class="tabs">
          <button
            type="button"
            class="tab"
            class:on={timerTab === "timer"}
            on:click={() => (timerTab = "timer")}>Timer</button
          >
          <button
            type="button"
            class="tab"
            class:on={timerTab === "ending"}
            on:click={() => (timerTab = "ending")}>Timer Ending</button
          >
          <button
            type="button"
            class="tab"
            class:on={timerTab === "ended"}
            on:click={() => (timerTab = "ended")}>Timer Ended</button
          >
        </div>

        {#if timerTab === "timer"}
          <div class="f-grid">
            <label class="f-label" for="tf-tname">Timer name</label>
            <input
              id="tf-tname"
              class="in"
              placeholder="Defaults to trigger name"
              bind:value={form.timer_name}
            />
            <label class="f-label" for="tf-dur">Duration (seconds)</label>
            <input
              id="tf-dur"
              class="in num"
              type="number"
              min="1"
              bind:value={form.timer_seconds}
            />
            <label class="f-label" for="tf-vis">Show bar for last (s)</label>
            <input
              id="tf-vis"
              class="in num"
              type="number"
              min="0"
              bind:value={form.timer_visible_seconds}
            />
            <label class="f-label" for="tf-behavior">If already running</label>
            <select
              id="tf-behavior"
              class="in"
              bind:value={form.timer_start_behavior}
            >
              <option value="StartNewTimer">Start another timer</option>
              <option value="RestartTimer">Restart this trigger's timer</option>
              <option value="IgnoreIfRunning">Ignore</option>
            </select>
          </div>
          <div class="f-note">
            Show bar for last: 0 shows the bar the whole time; a smaller value
            keeps it hidden until only that many seconds remain.
          </div>

          <div class="f-sep" />
          <div class="ender-head">
            <span class="f-label">Early end conditions</span>
            <button type="button" class="btn ender-add" on:click={addEnder}
              >+ Add</button
            >
          </div>
          <div class="f-note">
            If any of these matches a later log line, this timer ends early
            (before its countdown runs out).
          </div>
          {#each form.early_enders || [] as e (e)}
            <div class="ender-row">
              <input
                class="in mono"
                placeholder="Search text / regex"
                bind:value={e.text}
              />
              <label
                class="f-chk ender-rx"
                title="Treat as a regular expression"
              >
                <input type="checkbox" bind:checked={e.regex} /> regex
              </label>
              <button
                type="button"
                class="ender-del"
                title="Remove this condition"
                aria-label="Remove condition"
                on:click={() => removeEnder(form.early_enders.indexOf(e))}
                >✕</button
              >
            </div>
          {/each}
        {:else if timerTab === "ending"}
          <div class="f-note">
            Fires before the timer runs out (e.g. a "fading soon" warning).
          </div>
          <label class="f-chk">
            <input type="checkbox" bind:checked={form.ending_enabled} /> Enabled
          </label>
          {#if form.ending_enabled}
            <div class="f-grid">
              <label class="f-label" for="tf-ending-sec"
                >Seconds before end</label
              >
              <input
                id="tf-ending-sec"
                class="in num"
                type="number"
                min="1"
                bind:value={form.ending_seconds}
              />
            </div>
            <TriggerActions
              bind:action={form.ending}
              {mediaFiles}
              onAdd={addMediaFile}
              onSample={sampleMediaFile}
            />
          {/if}
        {:else}
          <div class="f-note">Fires when the timer reaches zero.</div>
          <label class="f-chk">
            <input type="checkbox" bind:checked={form.ended_enabled} /> Enabled
          </label>
          {#if form.ended_enabled}
            <TriggerActions
              bind:action={form.ended}
              {mediaFiles}
              onAdd={addMediaFile}
              onSample={sampleMediaFile}
            />
          {/if}
        {/if}
      {/if}

      {#if formErr}<div class="f-err">{formErr}</div>{/if}
      <div class="modal-actions">
        <button class="btn save" on:click={saveForm}>Save</button>
        <button class="btn" on:click={() => (form = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- Import from GINA -->
{#if gina}
  <div class="overlay" on:click|self={() => (gina = null)}>
    <div class="modal gina-modal">
      <div class="modal-title">Import triggers from GINA</div>

      <label class="f-label" for="gina-path">GINA config file</label>
      <div class="gina-path">
        <input
          id="gina-path"
          class="in"
          placeholder="Path to GINAConfig.xml"
          bind:value={gina.path}
        />
        <button class="btn" on:click={browseGina}>Browse…</button>
      </div>

      {#if gina.scanning}
        <div class="f-note">Scanning…</div>
      {:else if gina.err && !gina.groups.length}
        <div class="f-err">{gina.err}</div>
      {:else if gina.groups.length}
        <div class="gina-note">
          Choose the top-level groups to import. They'll be added under your
          <strong>Personal</strong> triggers.
        </div>
        <label class="f-chk gina-all">
          <input
            type="checkbox"
            checked={ginaAllSelected}
            on:change={toggleAllGina}
          /> Select all
        </label>
        <div class="gina-list">
          {#each gina.groups as g (g.group_id)}
            <label class="gina-item" class:excluded={g.excluded}>
              <input
                type="checkbox"
                bind:checked={g.sel}
                disabled={g.excluded}
              />
              <span class="gina-name">{g.name}</span>
              <span class="gina-count"
                >{g.triggers} trigger{g.triggers === 1 ? "" : "s"}</span
              >
              {#if g.excluded}
                <span class="gina-tag">in the Fuse package</span>
              {/if}
            </label>
          {/each}
        </div>
        {#if gina.err}<div class="f-err">{gina.err}</div>{/if}
      {/if}

      <div class="modal-actions">
        <button
          class="btn save"
          on:click={importGina}
          disabled={gina.importing || !ginaAnySelected}
        >
          {gina.importing ? "Importing…" : "Import"}
        </button>
        <button class="btn" on:click={() => (gina = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- Fuse Triggers XML import: dry-run summary before anything is replaced. -->
{#if fuseImp}
  <div class="overlay" on:click|self={() => (fuseImp = null)}>
    <div class="modal fimp-modal">
      <div class="modal-title">🌐 Import Fuse Triggers from XML</div>

      {#if !fuseImp.valid}
        <div class="f-err">{fuseImp.error}</div>
        <div class="f-note">
          Nothing has been changed. Fix the file and try again.
        </div>
      {:else}
        <div class="fimp-path" title={fuseImp.path}>
          {fuseImp.path.split(/[\\/]/).pop()}
        </div>
        <div class="fimp-sum">
          <span class="fimp-stat"
            >{fuseImp.triggers} triggers in {fuseImp.groups} groups</span
          >
          <span class="fimp-chip add">+{fuseImp.added_count} new</span>
          <span class="fimp-chip chg">{fuseImp.changed_count} changed</span>
          <span class="fimp-chip del">−{fuseImp.removed_count} removed</span>
        </div>

        {#if fuseImp.orphaned}
          <div class="fimp-warn">
            <strong>{fuseImp.orphaned}</strong> per-character on/off setting{fuseImp.orphaned ===
            1
              ? ""
              : "s"} point at a group or trigger this file no longer defines. Those
            settings are keyed by group id and trigger name, so a rename or renumber
            detaches them — the affected characters fall back to the default enablement.
          </div>
        {/if}

        {#if fuseImp.removed_count}
          <div class="fimp-sec">
            <div class="fimp-sec-t">Removed</div>
            <div class="fimp-list">
              {#each fuseImp.removed as p}<div class="fimp-row del">
                  {p}
                </div>{/each}
              {#if fuseImp.removed_count > fuseImp.removed.length}
                <div class="fimp-more">
                  …and {fuseImp.removed_count - fuseImp.removed.length} more
                </div>
              {/if}
            </div>
          </div>
        {/if}
        {#if fuseImp.changed_count}
          <div class="fimp-sec">
            <div class="fimp-sec-t">Changed</div>
            <div class="fimp-list">
              {#each fuseImp.changed as p}<div class="fimp-row chg">
                  {p}
                </div>{/each}
              {#if fuseImp.changed_count > fuseImp.changed.length}
                <div class="fimp-more">
                  …and {fuseImp.changed_count - fuseImp.changed.length} more
                </div>
              {/if}
            </div>
          </div>
        {/if}
        {#if fuseImp.added_count}
          <div class="fimp-sec">
            <div class="fimp-sec-t">Added</div>
            <div class="fimp-list">
              {#each fuseImp.added as p}<div class="fimp-row add">
                  {p}
                </div>{/each}
              {#if fuseImp.added_count > fuseImp.added.length}
                <div class="fimp-more">
                  …and {fuseImp.added_count - fuseImp.added.length} more
                </div>
              {/if}
            </div>
          </div>
        {/if}
        {#if !fuseImp.added_count && !fuseImp.changed_count && !fuseImp.removed_count}
          <div class="f-note">
            This file is identical to your current Fuse Triggers set.
          </div>
        {/if}

        <div class="f-note">
          Importing replaces your local Fuse set and marks it unpublished — the
          guild sees nothing until you press <strong>Publish to guild</strong>.
        </div>
      {/if}

      <div class="modal-actions">
        {#if fuseImp.valid}
          <button
            class="btn save"
            disabled={fuseImpBusy}
            on:click={confirmFuseImport}
          >
            {fuseImpBusy ? "Importing…" : "Import"}
          </button>
        {/if}
        <button class="btn" on:click={() => (fuseImp = null)}>
          {fuseImp.valid ? "Cancel" : "Close"}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .trig-tab {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
    flex-shrink: 0;
  }
  .title {
    color: var(--accent);
    font-weight: 600;
    font-size: 13px;
  }
  .who {
    color: var(--text-muted);
    font-size: 11px;
  }
  .imp-ok {
    color: var(--success);
    font-size: 11px;
  }
  .imp-err {
    color: #e05c5c;
    font-size: 11px;
  }
  .ro-note {
    color: var(--text-muted);
    font-size: 10.5px;
    font-style: italic;
  }
  .bar-right {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 6px;
  }
  /* Edit-form section header + Timer/Ending/Ended tabs */
  .f-section {
    color: var(--accent);
    font-size: 10.5px;
    font-weight: 700;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }
  .tabs {
    display: flex;
    gap: 2px;
    border-bottom: 1px solid var(--border);
    margin: 2px 0 4px;
  }
  .tab {
    background: transparent;
    border: 1px solid transparent;
    border-bottom: none;
    border-radius: 4px 4px 0 0;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 11px;
    padding: 4px 10px;
  }
  .tab:hover {
    color: var(--text-secondary);
  }
  .tab.on {
    color: var(--accent);
    border-color: var(--border);
    background: var(--bg-panel);
    margin-bottom: -1px;
  }
  /* Import-from-GINA button — pinned to the bottom of the (scrolling) tree pane */
  .tree-foot {
    position: sticky;
    bottom: -10px; /* cancel .main's bottom padding so it sits flush */
    z-index: 5;
    display: flex;
    justify-content: flex-end;
    padding: 10px 0;
    margin-top: 8px;
    border-top: 1px solid var(--border);
    background: var(--bg-primary);
  }
  .gina-btn {
    font-size: 11px;
  }
  /* Fuse Triggers XML import preview */
  .fimp-modal {
    width: 620px;
    max-width: 92vw;
  }
  /* File name only — the full path is the tooltip, so a long path can't push
     the useful part out of the dialog. */
  .fimp-path {
    font-size: 10px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .fimp-sum {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin: 8px 0;
  }
  .fimp-stat {
    font-size: 12px;
    color: var(--text-primary);
    font-weight: 600;
  }
  .fimp-chip {
    font-size: 10px;
    border-radius: 3px;
    padding: 1px 6px;
    border: 1px solid var(--border);
    color: var(--text-secondary);
  }
  .fimp-chip.add {
    color: #6bbf6b;
    border-color: rgba(107, 191, 107, 0.45);
  }
  .fimp-chip.chg {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .fimp-chip.del {
    color: #e05c5c;
    border-color: rgba(224, 92, 92, 0.45);
  }
  .fimp-warn {
    font-size: 11px;
    line-height: 1.5;
    color: var(--text-secondary);
    background: rgba(227, 160, 8, 0.12);
    border: 1px solid rgba(227, 160, 8, 0.5);
    border-radius: 4px;
    padding: 6px 8px;
    margin-bottom: 8px;
  }
  .fimp-sec {
    margin-bottom: 8px;
  }
  .fimp-sec-t {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-muted);
    margin-bottom: 3px;
  }
  .fimp-list {
    max-height: 130px;
    overflow-y: auto;
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 4px 6px;
  }
  .fimp-row {
    font-size: 11px;
    line-height: 1.45;
    color: var(--text-secondary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .fimp-row.add {
    color: #6bbf6b;
  }
  .fimp-row.chg {
    color: var(--accent);
  }
  .fimp-row.del {
    color: #e05c5c;
  }
  .fimp-more {
    font-size: 10px;
    color: var(--text-muted);
    padding-top: 2px;
  }

  .gina-modal {
    min-width: 420px;
    max-width: 520px;
  }
  .gina-path {
    display: flex;
    gap: 6px;
    align-items: center;
  }
  .gina-path .in {
    flex: 1;
    min-width: 0;
  }
  .gina-note {
    color: var(--text-muted);
    font-size: 11px;
    margin: 10px 0 6px;
  }
  .gina-all {
    margin-bottom: 4px;
  }
  .gina-list {
    max-height: 260px;
    overflow-y: auto;
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 4px;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .gina-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 6px;
    border-radius: 3px;
    cursor: pointer;
    font-size: 12px;
  }
  .gina-item:hover {
    background: var(--bg-panel);
  }
  .gina-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .gina-count {
    color: var(--text-muted);
    font-size: 10.5px;
    flex-shrink: 0;
  }
  .gina-item.excluded {
    opacity: 0.55;
    cursor: default;
  }
  .gina-item.excluded:hover {
    background: transparent;
  }
  .gina-tag {
    color: var(--accent);
    font-size: 10px;
    font-style: italic;
    flex-shrink: 0;
  }
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 8px;
  }
  .btn:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  .btn.on {
    color: var(--accent);
    border-color: var(--accent-dim);
  }

  /* Color comes from the alert's category (set inline), so the backdrop stays
     neutral instead of tinting everything gold. */
  .alert {
    flex-shrink: 0;
    text-align: center;
    padding: 10px 14px;
    font-size: 17px;
    font-weight: 700;
    background: rgba(255, 255, 255, 0.04);
    border-bottom: 1px solid var(--accent-dim);
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.6);
  }

  /* sub-tabs */
  .subtabs {
    display: flex;
    gap: 2px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 2px;
  }
  .subtab {
    background: none;
    border: none;
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 3px 10px;
    white-space: nowrap;
  }
  .subtab:hover {
    color: var(--text-primary);
  }
  .subtab.on {
    background: var(--bg-secondary);
    color: var(--accent);
  }

  /* Defaults editor banner (Configure Defaults mode). */
  .def-bar {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    background: rgba(227, 160, 8, 0.08);
    border: 1px solid #e3a008;
    border-radius: 5px;
    padding: 8px 12px;
    margin-bottom: 10px;
  }
  .def-badge {
    background: #e3a008;
    color: #1a1509;
    border-radius: 3px;
    font-size: 10px;
    font-weight: 800;
    letter-spacing: 0.06em;
    padding: 2px 7px;
    flex-shrink: 0;
  }
  .def-msg {
    color: var(--text-secondary);
    font-size: 12px;
    flex: 1 1 auto;
    min-width: 0;
  }

  /* character picker (Manage Timers) */
  .char-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }
  .char-label {
    color: var(--text-secondary);
    font-size: 11px;
    white-space: nowrap;
  }
  .char-sel {
    width: auto;
    min-width: 180px;
  }
  .char-note {
    color: var(--accent);
    font-size: 10.5px;
    font-style: italic;
  }

  /* overlays page */
  .ov-intro {
    color: var(--text-muted);
    font-size: 11.5px;
    line-height: 1.5;
    margin-bottom: 10px;
  }
  /* default-layout panel */
  .ov-def {
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px 12px;
    margin-bottom: 12px;
    background: var(--bg-secondary);
  }
  .ov-def-cta {
    border-color: var(--accent-dim);
    background: rgba(200, 169, 81, 0.07);
  }
  .ov-def-editing {
    border-color: #b3541e;
    background: rgba(179, 84, 30, 0.12);
  }
  .ovd-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 6px;
  }
  .ovd-badge,
  .ovd-step {
    flex: 0 0 auto;
    padding: 2px 7px;
    border-radius: 3px;
    font-size: 9px;
    font-weight: 800;
    letter-spacing: 0.09em;
  }
  .ovd-badge {
    background: #b3541e;
    color: #fff;
  }
  .ovd-step {
    background: var(--accent);
    color: #1a1400;
  }
  .ovd-title {
    color: var(--text-primary);
    font-size: 12.5px;
  }
  .ovd-inline {
    margin-right: auto;
  }
  .ovd-body {
    color: var(--text-secondary);
    font-size: 11.5px;
    line-height: 1.5;
    margin-bottom: 9px;
  }
  .ovd-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }
  .ovd-go {
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .ovd-danger {
    border-color: #e05c5c;
    color: #ff9b9b;
  }
  .ovd-count {
    color: var(--text-muted);
    font-size: 11px;
  }
  .ovd-note {
    font-size: 11.5px;
    color: var(--accent);
    margin: -4px 0 10px;
  }
  .ovd-note.err {
    color: #ff6b6b;
  }
  .ov-controls {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 14px;
  }
  /* Pinned variant: lives OUTSIDE the scrolling .main, directly under the
     tab bar, on every sub-tab. */
  .ov-controls-pinned {
    flex-shrink: 0;
    flex-wrap: wrap;
    margin-bottom: 0;
    padding: 8px 14px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-panel);
  }
  .ov-titles {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-left: auto;
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
  }
  .ov-titles .in {
    width: auto;
    padding: 2px 6px;
    font-size: 11px;
  }
  .ov-sec-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 4px 0 6px;
  }
  .ov-sec-title {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .ov-add {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 13px;
    line-height: 1;
    padding: 1px 7px 3px;
  }
  .ov-add:hover {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  /* Three columns of cards. An expanded card's trigger list is a grid item
     too, spanning every column so it gets the full width — which also drops it
     onto its own row, just below the row holding the card that opened it. */
  .ov-list {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 6px;
    margin-bottom: 14px;
    align-items: start;
  }
  .ov-card {
    display: flex;
    align-items: center;
    gap: 7px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 6px 8px;
    cursor: pointer;
  }
  .ov-card:hover {
    border-color: var(--accent-dim);
  }
  /* The open card and its list are no longer adjacent (the list is on the next
     row, the card may be in any column), so mark the open card by tint rather
     than by joining their borders. */
  .ov-card.open {
    border-color: var(--accent-dim);
    background: rgba(200, 169, 81, 0.1);
  }
  .ov-caret {
    color: var(--text-muted);
    font-size: 10px;
    width: 10px;
    flex-shrink: 0;
  }
  /* Empty-state text spans the row rather than sitting in the first column. */
  .ov-list .hint {
    grid-column: 1 / -1;
  }
  .ov-tree {
    grid-column: 1 / -1;
    border: 1px solid var(--accent-dim);
    border-radius: 4px;
    padding: 6px 8px;
    margin-bottom: 2px;
  }
  /* Takes the slack in the card so a long category name ellipsizes instead of
     pushing the count and Pop out button out of a narrow column. */
  .ov-name {
    flex: 1;
    font-size: 12px;
    color: var(--text-primary);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ov-count {
    color: var(--text-muted);
    font-size: 10.5px;
    font-family: var(--font-mono);
  }
  .ov-pop {
    flex-shrink: 0;
  }
  /* Sticky Mode — same faint red field as the overlay's own gear panel, so the
     setting looks identical wherever you meet it. */
  .sticky-box {
    padding: 7px 9px;
    border: 1px solid rgba(224, 92, 92, 0.45);
    border-radius: 5px;
    background: rgba(224, 92, 92, 0.09);
  }
  .sw-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    cursor: pointer;
  }
  .sw-lbl {
    font-size: 12px;
    font-weight: 700;
    color: #ff9b9b;
  }
  .sw {
    position: relative;
    flex: none;
    line-height: 0;
  }
  .sw input {
    position: absolute;
    inset: 0;
    opacity: 0;
    margin: 0;
    cursor: pointer;
  }
  .sw-track {
    display: block;
    width: 30px;
    height: 16px;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.16);
    transition: background 0.15s ease;
  }
  .sw-thumb {
    display: block;
    width: 12px;
    height: 12px;
    margin: 2px;
    border-radius: 50%;
    background: #cfcfcf;
    transition:
      transform 0.15s ease,
      background 0.15s ease;
  }
  .sw input:checked + .sw-track {
    background: rgba(224, 92, 92, 0.75);
  }
  .sw input:checked + .sw-track .sw-thumb {
    transform: translateX(14px);
    background: #fff;
  }
  .sticky-warn {
    margin-top: 6px;
    font-size: 10px;
    line-height: 1.45;
    color: #e6a6a6;
  }
  /* An overlay that's already up: the button takes it down, and says so in the
     colour the rest of the tab uses for destructive actions. */
  .ov-remove {
    border-color: #e05c5c;
    color: #ff9b9b;
  }
  .ov-remove:hover {
    background: rgba(224, 92, 92, 0.14);
  }
  /* The gear takes over margin-left:auto so the ⚙ + Pop out pair sit together
     at the card's right edge. */
  .ov-gear {
    margin-left: auto;
    flex-shrink: 0;
    padding: 3px 7px;
  }
  /* Special Overlay cards: no expand/edit affordances, just name + blurb.
     Taller than the category cards — the name stacks over a description that
     wraps in full instead of being ellipsized. */
  .ov-card.sp {
    cursor: default;
    align-items: center;
  }
  .sp-txt {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .ov-name.sp-name {
    flex: none;
    font-weight: 600;
  }
  .sp-desc {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.4;
    white-space: normal;
  }

  /* Officer revision bar: unpublished Fuse Trigger changes. */
  .pub-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    background: rgba(200, 169, 81, 0.08);
    border: 1px solid var(--accent-dim);
    border-radius: 4px;
    padding: 6px 10px;
    margin-bottom: 10px;
  }
  .pub-msg {
    flex: 1;
    min-width: 0;
    font-size: 11.5px;
    color: var(--text-secondary);
    line-height: 1.4;
  }
  .pub-warn {
    color: #e0a05c;
    font-weight: 600;
  }

  .main {
    flex: 1;
    overflow-y: auto;
    padding: 10px 14px;
  }
  .hint {
    color: var(--text-muted);
    font-size: 12px;
    padding: 16px 4px;
    text-align: center;
  }

  /* countdown bars */
  .cat {
    margin-bottom: 12px;
  }
  .cat-head {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-secondary);
    margin-bottom: 5px;
  }
  .cat-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
  .cat-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* the collapsible part of the header: chevron + dot + name (the action
     buttons stay outside so dismiss/pop-out don't toggle the section) */
  .cat-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    padding: 0;
    background: none;
    border: none;
    cursor: pointer;
    font: inherit;
    color: inherit;
    text-transform: inherit;
    letter-spacing: inherit;
  }
  .cat-toggle:hover {
    color: var(--text-primary);
  }
  .cat-count {
    color: var(--text-muted);
    font-weight: 600;
    text-transform: none;
    letter-spacing: normal;
    white-space: nowrap;
  }
  .cat-actions {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 2px;
    opacity: 0;
    transition: opacity 0.12s;
  }
  .cat-head:hover .cat-actions {
    opacity: 1;
  }
  .cat-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 1px 3px;
    border-radius: 3px;
    display: inline-flex;
    align-items: center;
  }
  .cat-btn:hover {
    color: var(--accent);
    background: rgba(255, 255, 255, 0.06);
  }
  .cat-btn:first-child:hover {
    color: #e05c5c;
  }

  .tbar-trash {
    position: absolute;
    right: 5px;
    top: 50%;
    transform: translateY(-50%);
    background: none;
    border: none;
    color: #fff;
    cursor: pointer;
    padding: 1px;
    display: inline-flex;
    align-items: center;
    opacity: 0;
    transition: opacity 0.12s;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.85);
    filter: drop-shadow(0 1px 1px rgba(0, 0, 0, 0.85));
  }
  .tbar:hover .tbar-trash {
    opacity: 1;
  }
  .tbar:hover .tbar-time {
    right: 28px;
  }
  .tbar-trash:hover {
    color: #ff8a8a;
  }
  .tbar {
    position: relative;
    height: 22px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    overflow: hidden;
    margin-bottom: 4px;
  }
  .tbar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    opacity: 0.5;
  }
  .tbar-name,
  .tbar-time {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    font-size: 11.5px;
    font-weight: 600;
    color: #fff;
    text-shadow:
      0 1px 2px rgba(0, 0, 0, 0.85),
      0 0 3px rgba(0, 0, 0, 0.6);
    white-space: nowrap;
  }
  .tbar-name {
    left: 8px;
    max-width: 70%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tbar-time {
    right: 8px;
    font-family: var(--font-mono);
  }

  /* server-wide board: Game Time / Events / Boats — one shared color */
  .world {
    margin-bottom: 14px;
  }
  .w-title {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 2px;
    margin-bottom: 6px;
    background: none;
    border: none;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--accent);
  }
  /* "Your Enabled Timers" header on the live board — same visual weight as
     the Server Timers section title, minus the collapse affordance. */
  .live-head {
    padding: 4px 2px;
    margin-bottom: 6px;
    border-bottom: 1px solid var(--border);
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--accent);
  }
  /* Overlay-setup call to action at the top of Manage Timers. */
  .ov-cta {
    display: flex;
    align-items: center;
    gap: 10px;
    background: rgba(200, 169, 81, 0.07);
    border: 1px solid var(--accent-dim);
    border-radius: 5px;
    padding: 8px 12px;
    margin-bottom: 10px;
  }
  .ov-cta-msg {
    flex: 1 1 auto;
    color: var(--text-secondary);
    font-size: 12px;
    line-height: 1.5;
  }
  .ov-cta-msg strong {
    color: var(--text-primary);
  }
  .ov-cta-btn {
    white-space: nowrap;
  }
  .w-title:hover {
    color: var(--text-primary);
  }
  .w-chev {
    transition: transform 0.15s;
  }
  .w-chev-open {
    transform: rotate(90deg);
  }
  .w-title-sub {
    margin-left: auto;
    text-transform: none;
    letter-spacing: normal;
    font-weight: 500;
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--text-secondary);
  }
  .w-sec {
    margin-bottom: 10px;
  }
  .w-head {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-secondary);
    margin-bottom: 5px;
  }
  .w-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent);
  }
  .w-sub {
    margin-left: auto;
    text-transform: none;
    letter-spacing: normal;
    font-weight: 500;
    font-family: var(--font-mono);
    color: var(--text-secondary);
  }
  .w-empty {
    font-size: 11px;
    color: var(--text-muted);
    padding: 4px 2px;
  }
  /* the in-game day: fill sweeps midnight→midnight, ticks mark each hour */
  .w-day {
    position: relative;
    height: 18px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    overflow: hidden;
  }
  .w-day-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    background: var(--accent);
    opacity: 0.35;
  }
  .w-tick {
    position: absolute;
    bottom: 0;
    width: 1px;
    height: 45%;
    background: var(--border-hover);
  }
  /* the hour it is NOW — the one the fill has already passed */
  .w-tick-now {
    width: 2px;
    height: 100%;
    background: var(--accent);
    box-shadow: 0 0 5px var(--accent);
  }
  /* the hour the fill is approaching — present but not the headline */
  .w-tick-next {
    width: 2px;
    height: 100%;
    background: var(--text-muted);
  }
  /* An hour's number in a circle riding its tick. Two of them: the current
     hour in accent (what time is it) and the next greyed out (what's coming),
     so the pair reads left-to-right as now → next. */
  .w-hour-badge {
    position: absolute;
    top: 50%;
    transform: translate(-50%, -50%);
    width: 15px;
    height: 15px;
    border-radius: 50%;
    background: var(--bg-panel);
    font-size: 9px;
    font-weight: 700;
    line-height: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    pointer-events: none;
  }
  .w-hour-now {
    border: 1px solid var(--accent);
    color: var(--accent);
    box-shadow: 0 0 5px rgba(200, 169, 81, 0.45);
    /* above the next badge, for the moment near an hour flip when the two
       sit one tick apart */
    z-index: 1;
  }
  .w-hour-next {
    border: 1px solid var(--text-muted);
    color: var(--text-muted);
  }
  .w-day-edge {
    position: absolute;
    left: 4px;
    top: 50%;
    transform: translateY(-50%);
    font-size: 8.5px;
    color: var(--text-muted);
    pointer-events: none;
  }
  .w-day-edge-r {
    left: auto;
    right: 4px;
  }
  /* event bars reuse the trigger countdown look, tinted the shared color */
  .w-fill {
    background: var(--accent);
  }
  .w-marks {
    color: var(--accent);
    font-weight: 700;
    margin-left: 2px;
  }
  /* Inside a spawn window: the mob can pop ANY moment, which is the one state
     on this board that deserves to glow. */
  .w-inwin {
    border: 1px solid rgba(200, 169, 81, 0.5);
  }
  .w-inwin .tbar-time {
    color: var(--accent);
    font-weight: 700;
  }
  .w-bar-none {
    opacity: 0.55;
  }
  /* World rows have no trash button, so the countdown never needs to slide
     clear of one — undo the trigger bars' hover shift. */
  .tbar.w-bar:hover .tbar-time {
    right: 8px;
  }
  /* Stale-anchor flag beside the boat name: yellow past 24h since the last
     sighting, red past 3 days. The tooltip carries the explanation. */
  .w-stale {
    margin-left: 4px;
    color: #ffd60a;
    font-size: 10px;
    cursor: help;
  }
  .w-stale.red {
    color: #ff6b6b;
  }
  .w-none-txt {
    color: var(--text-muted);
    font-weight: 500;
  }
  /* boats: a dock at each end, the ship sails the dashed track between.
     Two columns, two rows — Oasis→TD / TD→OT stacked on the left, BB→FV /
     NRO→IC on the right (fill order set by boatOrder). */
  .w-boats {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 4px 8px;
    margin-bottom: 4px;
  }
  .w-boat {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 4px 8px 3px;
    min-width: 0;
  }
  .w-boat-none {
    opacity: 0.55;
  }
  .w-route {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  /* Board row plus its reminder bell. The bar keeps its own bottom margin, so
     the row pulls the bell up to sit level with it. */
  .w-alarmrow {
    display: flex;
    align-items: flex-start;
    gap: 6px;
  }
  .w-alarmrow > :global(.tbar) {
    flex: 1 1 auto;
    min-width: 0;
  }
  .w-alarmrow > :global(button) {
    margin-top: 2px;
  }
  .w-port {
    font-size: 10.5px;
    font-weight: 600;
    color: var(--text-secondary);
    white-space: nowrap;
  }
  .w-track {
    position: relative;
    flex: 1;
    height: 16px;
    min-width: 60px;
  }
  .w-track-line {
    position: absolute;
    left: 0;
    right: 0;
    top: 50%;
    border-top: 1px dashed var(--border-hover);
  }
  .w-ship {
    position: absolute;
    top: 50%;
    transform: translate(-50%, -50%);
    color: var(--accent);
  }
  .w-ship-flip {
    transform: translate(-50%, -50%) scaleX(-1);
  }
  .w-eta {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-top: 1px;
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--text-primary);
  }
  .w-boat-name {
    font-family: inherit;
    font-size: 10px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* edit tree */
  .search-row {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 8px;
  }
  .search-in {
    flex: 1;
    min-width: 0;
  }
  .match-count {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .search-row .btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .tree {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  /* activity feed (locked bottom); height is inline and user-dragged */
  .activity {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    background: var(--bg-secondary);
  }
  /* Resize handle on the feed's top edge. Carries the border the panel used
     to, so the seam doesn't double up. */
  .act-grip {
    flex-shrink: 0;
    position: relative;
    height: 7px;
    cursor: ns-resize;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
    touch-action: none;
  }
  .act-grip::after {
    content: "";
    position: absolute;
    left: 50%;
    top: 2px;
    transform: translateX(-50%);
    width: 34px;
    height: 2px;
    border-radius: 1px;
    background: var(--border-hover);
  }
  .act-grip:hover::after,
  .act-grip.dragging::after {
    background: var(--accent);
  }
  .act-title {
    flex-shrink: 0;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
    padding: 5px 12px 3px;
  }
  .act-list {
    flex: 1;
    overflow-y: auto;
    padding: 0 12px 6px;
  }
  .act-row {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-size: 11.5px;
    line-height: 1.7;
  }
  .act-time {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 10.5px;
    flex-shrink: 0;
  }
  .act-path {
    background: none;
    border: none;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11.5px;
    padding: 0;
    text-align: left;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .act-path:hover {
    color: var(--accent);
    text-decoration: underline;
  }
  .act-empty {
    color: var(--text-muted);
    font-size: 11.5px;
    padding: 6px 0;
  }

  /* context menu */
  .ctx {
    position: fixed;
    z-index: 60;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 4px;
    display: flex;
    flex-direction: column;
    min-width: 130px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
  }
  .ctx-item {
    background: none;
    border: none;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 5px 9px;
    border-radius: 4px;
    text-align: left;
  }
  .ctx-item:hover {
    background: rgba(255, 255, 255, 0.06);
  }
  .ctx-item.danger {
    color: #e05c5c;
  }

  /* modals */
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 50;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .modal {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 14px;
    width: 320px;
    display: flex;
    flex-direction: column;
    gap: 7px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal.form {
    width: 420px;
    max-height: 85%;
    overflow-y: auto;
  }
  .modal-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 4px;
  }
  .btn.save {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .in {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 5px 8px;
    outline: none;
    width: 100%;
  }
  .in:focus {
    border-color: var(--accent-dim);
  }
  .in.mono {
    font-family: var(--font-mono);
    font-size: 11px;
    resize: vertical;
  }
  /* Keep the search textarea from being flex-shrunk to nothing in the (scrollable)
     form — always show at least one line of text. */
  .modal.form textarea.in {
    flex-shrink: 0;
    min-height: 2.4em;
  }
  .in.num {
    width: 110px;
  }
  .f-label {
    font-size: 11px;
    color: var(--text-secondary);
  }
  /* Category select + inline "new category" row in the edit form. */
  .f-catrow {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .f-catrow .in {
    flex: 1 1 auto;
    min-width: 0;
  }
  .f-caterr {
    color: var(--error, #ff8a8a);
    font-size: 11px;
  }
  .f-chk {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .f-chk input {
    accent-color: var(--accent);
  }
  .f-grid {
    display: grid;
    grid-template-columns: 130px 1fr;
    gap: 6px 8px;
    align-items: center;
  }
  .f-sep {
    height: 1px;
    background: var(--border);
    margin: 3px 0;
  }
  .f-err {
    color: #e05c5c;
    font-size: 11.5px;
  }
  .f-note {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.5;
  }
  .f-inline {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .f-inline input[type="color"] {
    width: 34px;
    height: 20px;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 3px;
    background: none;
    cursor: pointer;
    flex-shrink: 0;
  }
  .f-inline input[type="range"] {
    flex: 1;
    min-width: 0;
    accent-color: var(--accent);
  }
  .f-val {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 10.5px;
    width: 32px;
    text-align: right;
  }
  .btn.danger {
    color: #e05c5c;
    border-color: rgba(224, 92, 92, 0.5);
  }

  /* early end conditions */
  .ender-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .ender-add {
    padding: 1px 8px;
  }
  .ender-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .ender-row .in {
    flex: 1;
    min-width: 0;
  }
  .ender-rx {
    font-size: 10.5px;
    white-space: nowrap;
    flex-shrink: 0;
  }
  .ender-del {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 13px;
    line-height: 1;
    padding: 2px 4px;
    flex-shrink: 0;
  }
  .ender-del:hover {
    color: #e05c5c;
  }

  /* live preview of the category's look, matching the overlay renderers */
  .cf-prev {
    position: relative;
    height: 26px;
    border-radius: 4px;
    overflow: hidden;
    font-weight: 600;
  }
  .cf-prev.alert {
    display: flex;
    align-items: center;
    justify-content: center;
    height: auto;
    padding: 4px 6px;
    font-weight: 700;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.95);
  }
  .cf-prev-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    width: 62%;
  }
  .cf-prev-name,
  .cf-prev-time {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    white-space: nowrap;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.85);
  }
  .cf-prev-name {
    left: 8px;
    max-width: 70%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .cf-prev-time {
    right: 8px;
  }
</style>
