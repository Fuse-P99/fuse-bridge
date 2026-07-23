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
    CreateTriggerCategory,
    SaveTriggerCategory,
    DeleteTriggerCategory,
    OpenPopout,
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
    GetTriggerMediaFiles,
    AddTriggerMediaFile,
    PlayTriggerMediaSample,
    GetAudioSettings,
    SetAudioVolume,
    SetAudioMuted,
    DefaultGINAConfigPath,
    BrowseGINAConfig,
    ScanGINAConfig,
    ImportGINAGroups,
    PublishFuseTriggers,
    RevertFuseTriggers,
  } from "../../bindings/FuseBridge/app.js";
  import TriggerNode from "../lib/TriggerNode.svelte";
  import TriggerActions from "../lib/TriggerActions.svelte";
  import { scale } from "../lib/scale.js";
  import { catColor, rgba } from "../lib/catColor.js";

  // Sub-tabs: the live board, per-character trigger enablement, and overlay
  // management.
  const PAGES = [
    { id: "live", label: "Current Timers" },
    { id: "edit", label: "Manage Timers" },
    { id: "overlays", label: "Manage Overlays" },
  ];
  let view = "live"; // "live" | "edit" | "overlays"
  let state = {
    imported: true,
    character: "",
    alert: null,
    alerts: [],
    timers: [],
    activity: [],
  };
  let now = Date.now();
  let pollTimer, animReq, offTriggers, offUnhidden;

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

  async function poll() {
    if (polling) {
      pollAgain = true;
      return;
    }
    polling = true;
    try {
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

  // ── server sync + officer status ────────────────────────────────────────────
  // The Fuse Triggers set is downloaded from the server on open; officers'
  // edits sync back automatically. importMsg/importErr are transient notices.
  let importMsg = "";
  let importErr = "";
  let meta = { linked: false, officer: false };

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
    if (
      !confirm(
        "Publish your Fuse Trigger changes to the whole guild? Everyone's client will update to the new version.",
      )
    )
      return;
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
    if (
      !confirm(
        "Discard your local Fuse Trigger changes and revert to the published server copy?",
      )
    )
      return;
    publishing = true;
    try {
      await RevertFuseTriggers();
      importMsg = "Local changes discarded — back on the published Fuse Triggers.";
      setTimeout(() => (importMsg = ""), 6000);
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 8000);
    }
    publishing = false;
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

  async function loadTree() {
    try {
      tree = (await GetTriggerTreeFor(editChar)) || [];
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
  ];

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
      },
    };
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
        await SetTriggerGroupEnabledFor(editChar, obj.id, val);
      else await SetTriggerEnabledFor(editChar, obj.id, val);
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
    // triggers. Groups and triggers gate on the node's own editability.
    if (kind !== "category" && !target.editable) return;
    // The shell is CSS-zoomed; convert viewport coords to layout coords.
    menu = { x: e.clientX / $scale, y: e.clientY / $scale, kind, target };
  }
  function closeMenu() {
    menu = null;
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

  // Master audio (speaker control on the toolbar).
  let audioVol = 100;
  let audioMuted = false;
  let volOpen = false; // volume popover shown on hover
  let volTimer; // grace period so the popover survives the gap to the slider

  function volEnter() {
    clearTimeout(volTimer);
    volOpen = true;
  }
  function volLeave() {
    clearTimeout(volTimer);
    volTimer = setTimeout(() => (volOpen = false), 200);
  }

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
  // Keep the trigger's own category selectable even if it isn't a known category
  // yet, so saving doesn't clear it.
  $: catOptions =
    form && form.category && !catNames.includes(form.category)
      ? [form.category, ...catNames]
      : catNames;
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

  async function loadAudio() {
    try {
      const a = await GetAudioSettings();
      audioVol = a.volume;
      audioMuted = a.muted;
    } catch {
      /* defaults */
    }
  }
  function onVolInput() {
    SetAudioVolume(Number(audioVol));
  }
  function toggleMute() {
    audioMuted = !audioMuted;
    SetAudioMuted(audioMuted);
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
    if (!confirm(`Delete trigger "${t.name}"?`)) return;
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
    const detail = n
      ? ` and the ${n} trigger${n === 1 ? "" : "s"} inside it`
      : "";
    if (!confirm(`Delete group "${g.name}"${detail}?`)) return;
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
    await loadAudio(); // master volume/mute for the speaker control
    await syncNow(); // pull the latest Fuse Triggers from the server on open
    // Reflect the real overlay state so the buttons are correct after a remount.
    try {
      winHidden = await ArePopoutsManuallyHidden();
      winLocked = await ArePopoutsLocked();
      titleMode = await GetOverlayTitleMode();
      winSnap = await GetSnapToGrid();
    } catch {
      /* defaults */
    }
    // Push: refresh the instant a trigger fires. The interval stays as a safety
    // net (missed event, or a change with no event).
    offTriggers = Events.On("triggers-changed", poll);
    // Popping out an overlay force-clears the global hide on the Go side —
    // keep the Hide/Show button label in sync.
    offUnhidden = Events.On("popouts-unhidden", () => (winHidden = false));
    pollTimer = setInterval(poll, 1000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (offTriggers) offTriggers();
    if (offUnhidden) offUnhidden();
    if (animReq) cancelAnimationFrame(animReq);
    clearTimeout(volTimer);
  });
</script>

<svelte:window on:click={closeMenu} />

<div class="trig-tab">
  <div class="bar">
    <span class="title">Timers</span>
    {#if state.character}
      <span class="who">for {state.character}</span>
    {/if}
    {#if importMsg}<span class="imp-ok" transition:fade>{importMsg}</span>{/if}
    {#if importErr}<span class="imp-err" transition:fade>{importErr}</span>{/if}
    <div class="bar-right">
      {#if view === "edit" && meta.linked && !meta.officer}
        <span class="ro-note" title="Only officers can edit Fuse Triggers"
          >Fuse Triggers are read-only</span
        >
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

      <div
        class="spk"
        class:muted={audioMuted}
        on:mouseenter={volEnter}
        on:mouseleave={volLeave}
      >
        <button
          class="spk-btn"
          on:click={toggleMute}
          title={audioMuted
            ? "Audio muted — click to unmute"
            : "Mute all trigger audio (TTS + sounds)"}
          aria-label={audioMuted ? "Unmute audio" : "Mute audio"}
        >
          <svg
            viewBox="0 0 24 24"
            width="16"
            height="16"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M11 5 6 9H2v6h4l5 4V5z" />
            {#if audioMuted}
              <line x1="23" y1="9" x2="17" y2="15" />
              <line x1="17" y1="9" x2="23" y2="15" />
            {:else}
              <path d="M15.5 8.5a5 5 0 0 1 0 7" />
              <path d="M19 5a9 9 0 0 1 0 14" />
            {/if}
          </svg>
        </button>
        {#if volOpen}
          <div class="vol-pop" transition:fade={{ duration: 100 }}>
            <span class="vol-val">{audioMuted ? "muted" : audioVol}</span>
            <div class="vol-wrap">
              <input
                class="vol-range"
                type="range"
                min="0"
                max="100"
                bind:value={audioVol}
                on:input={onVolInput}
                aria-label="Audio volume"
              />
            </div>
          </div>
        {/if}
      </div>
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

  <div class="main">
    {#if view === "live"}
      {#if cats.length === 0}
        <div class="hint">
          No active timers — countdown bars appear here when a trigger fires.
        </div>
      {/if}
      {#each cats as c (c.name)}
        <div class="cat" transition:slide|local={{ duration: 150 }}>
          <div class="cat-head">
            <span class="cat-dot" style="background:{c.color}"></span>
            <span class="cat-name">{c.name}</span>
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
      {/each}
    {:else if view === "overlays"}
      <div class="ov-intro">
        Pop out any category as an overlay you can position over the game.
        Position and size are saved per character — a new character inherits the
        layout of one you've already set up in the same class. Colors and fonts
        belong to the category itself, so they're the same on every character.
        Click a category to see its triggers; right-click to edit or delete it.
      </div>

      <div class="ov-controls">
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
        <label
          class="ov-titles"
          title="When overlay title bars are shown. Hidden bars keep their height so timer bars don't shift."
        >
          <span>Overlay Titles</span>
          <select
            class="in"
            bind:value={titleMode}
            on:change={changeTitleMode}
          >
            <option value="always">Always Visible</option>
            <option value="locked">Hide When Locked</option>
            <option value="zero">Hide When 0 Triggers</option>
          </select>
        </label>
      </div>

      <!-- Special Overlays: the map + live raid-card sections. They keep their
           in-app styling; background color/opacity is set from each overlay's
           own ⚙ settings panel. Guild-only: the raid overlays render server
           raid data, which unlinked users don't receive, so the whole section
           is hidden until this client is Discord-linked. -->
      {#if meta.linked}
        <div class="ov-sec-head">
          <span class="ov-sec-title">Special Overlays</span>
        </div>
        <div class="ov-list">
          {#each SPECIAL_OVERLAYS as so (so.kind)}
            <div class="ov-card sp">
              <div class="sp-txt">
                <span class="ov-name sp-name" title={so.name}>{so.name}</span>
                <span class="sp-desc">{so.desc}</span>
              </div>
              <button
                class="btn ov-pop"
                title="Pop out “{so.name}” as an overlay"
                on:click={() => OpenPopout(so.kind, "")}>Pop out</button
              >
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
              <button
                class="btn ov-pop"
                title="Pop out “{c.name}” as an overlay"
                on:click|stopPropagation={() => OpenPopout(sec.kind, c.name)}
                >Pop out</button
              >
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
            Your Fuse Trigger changes (based on v{fuseRootNode.version}) are
            not published yet — only you can see them.
            {#if fuseRootNode.server_version > fuseRootNode.version}
              <span class="pub-warn"
                >v{fuseRootNode.server_version} was published by another
                officer in the meantime; publishing now will overwrite it.</span
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
        {#if editChar && editChar.toLowerCase() !== (state.character || "").toLowerCase()}
          <span class="char-note"
            >Editing another character — changes apply to {editChar} only.</span
          >
        {/if}
      </div>
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

  <!-- locked activity feed -->
  <div class="activity">
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
    {:else if menu.kind === "trigger"}
      <button class="ctx-item" on:click={menuEditTrigger}>Edit</button>
      <button class="ctx-item danger" on:click={menuDeleteTrigger}
        >Delete</button
      >
    {:else}
      <button class="ctx-item" on:click={menuRenameGroup}>Edit Name</button>
      <button class="ctx-item" on:click={menuNewTrigger}>New Trigger</button>
      <button class="ctx-item" on:click={menuNewGroup}>New Group</button>
      <button class="ctx-item danger" on:click={menuDeleteGroup}>Delete</button>
    {/if}
  </div>
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
      <select id="tf-cat" class="in" bind:value={form.category}>
        {#each catOptions as c (c)}
          <option value={c}>{c}</option>
        {/each}
      </select>

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
  /* Speaker / volume control */
  .spk {
    position: relative;
    display: flex;
    align-items: center;
  }
  .spk-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    color: var(--text-secondary);
    cursor: pointer;
    padding: 2px;
    border-radius: 3px;
  }
  .spk-btn:hover {
    color: var(--accent);
  }
  .spk.muted .spk-btn {
    color: #b23b3b;
  }
  .spk.muted .spk-btn:hover {
    color: #d24a4a;
  }
  .vol-pop {
    position: absolute;
    top: 100%;
    right: 0;
    margin-top: 4px;
    z-index: 40;
  }
  /* Transparent bridge across the gap between the speaker and the popover, so
     the mouse never leaves the control's hover area on the way to the slider. */
  .vol-pop::before {
    content: "";
    position: absolute;
    top: -6px;
    left: 0;
    right: 0;
    height: 6px;
  }
  .vol-pop {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 8px 6px 6px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
  }
  .vol-val {
    color: var(--text-muted);
    font-size: 10px;
    min-height: 12px;
  }
  .vol-wrap {
    height: 92px;
    width: 22px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  /* Rotate a horizontal range into a vertical slider (min at bottom). */
  .vol-range {
    width: 84px;
    transform: rotate(-90deg);
    accent-color: var(--accent);
    cursor: pointer;
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
  .ov-controls {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 14px;
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
    margin-left: auto;
    flex-shrink: 0;
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

  /* activity feed (locked bottom) */
  .activity {
    flex-shrink: 0;
    height: 150px;
    display: flex;
    flex-direction: column;
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
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
