<script>
  import { onMount, onDestroy, tick } from "svelte";
  import { Window, Events } from "@wailsio/runtime";
  import {
    SavePopoutState,
    GetPlayerPosition,
    ArePopoutsHidden,
    ArePopoutsLocked,
    GetPopoutProfile,
    GetOverlayTitleMode,
    GetSnapToGrid,
    GetCurrentZone,
    GetCategoryStyle,
    SaveTriggerCategory,
  } from "../bindings/FuseBridge/app.js";
  import MapTab from "./tabs/MapTab.svelte";
  import PopoutTimers from "./lib/PopoutTimers.svelte";
  import PopoutAlerts from "./lib/PopoutAlerts.svelte";
  import PopoutRaidSection from "./lib/PopoutRaidSection.svelte";
  import OtherTimers from "./lib/OtherTimers.svelte";
  import PopoutVoiceSpeakers from "./lib/PopoutVoiceSpeakers.svelte";
  import PopoutRandoms from "./lib/PopoutRandoms.svelte";
  import PopoutThreat from "./lib/PopoutThreat.svelte";
  import RaidDPS from "./lib/RaidDPS.svelte";

  export let kind = "map"; // "map" | "timers" | "alerts" | special raid kinds
  export let category = "";
  // True when this window was just popped out by a user click (Go appends
  // flash=1 to the hash): flash the title bar on mount so it's easy to locate.
  export let flash = false;

  // Special Overlays: app-wide raid-card sections (like the map, not per
  // character). They lock with the trigger overlays but keep shared settings.
  const SPECIAL_TITLES = {
    raidassign: "Raid Assignments",
    raiddebuffs: "Raid Debuffs",
    raidclerics: "Raid Clerics",
    othertimers: "Raid Specific Timers",
    voicespeakers: "Voice Speakers",
    randoms: "Randoms",
    threat: "Threat Meter",
    raiddps: "Raid DPS",
  };
  const isSpecial = kind in SPECIAL_TITLES;

  // This window's Go-side name (mirrors popoutIdent), so targeted
  // "popout-flash" events — sent when the user pops out something that's
  // already open — can be matched to this overlay.
  const WINDOW_NAME =
    kind === "map"
      ? "popout-map"
      : isSpecial
        ? `popout-${kind}`
        : `popout-${kind}-${category}`;

  // Everything except the map participates in the global overlay lock.
  $: lockable = kind !== "map";
  $: title =
    kind === "map" ? "Map" : SPECIAL_TITLES[kind] || category || "Timers";
  // Trigger AND special overlay look settings are per character; only the
  // map's are app-wide. The character suffix is resolved at mount — these
  // overlays are closed and reopened on a swap, so a fresh mount always
  // reflects the incoming character.
  const BASEKEY = `fuse.popout.${kind === "map" || isSpecial ? kind : kind + ":" + category}`;
  let KEY = BASEKEY;
  const minW = kind === "map" ? 260 : kind === "alerts" ? 220 : 200;
  const minH = kind === "map" ? 220 : kind === "alerts" ? 70 : 120;

  let showSettings = false;
  // Per-popout overlay settings, persisted in localStorage alongside geometry.
  // Trigger overlays default fully transparent (only the bars/alerts show over
  // the game); the map and the raid-section overlays default to a mostly-opaque
  // dark backdrop so their content reads well. All default always-on-top.
  // `fit` (special overlays only, default on) shrinks the window height to the
  // rendered content — see applyFit.
  let settings =
    kind === "map"
      ? { bg: "#0f1117", opacity: 0.85, aot: true, autohide: true }
      : isSpecial
        ? { bg: "#0f1117", opacity: 0.85, aot: true, fit: true }
        : { bg: "#0f1117", opacity: 0, aot: true };

  // Lock (click-through + frozen drag/resize) is controlled globally from the
  // Timers window; here it's driven only by the popouts-locked/unlocked events
  // (not a per-overlay setting, not persisted).
  let locked = false;

  // Overlay title-bar visibility mode (app-wide, from Manage Overlays):
  // "always" | "locked" (hide while locked) | "zero" (hide when idle). Only
  // trigger overlays honor it — the map keeps its title (+ its own auto-hide).
  // hasContent is pushed up from the timer/alert child. When hidden, the title
  // keeps its height (visibility, not display) so bars don't shift.
  let titleMode = "always";
  let hasContent = false;

  // ── setup mode ──────────────────────────────────────────────────────────────
  // A raid overlay with no raid running renders nothing, which makes it
  // impossible to position the first time you pop one out. So an overlay the
  // user just opened by hand stays visible regardless of content until they've
  // clearly finished placing it — signalled by either locking the overlays or
  // zoning. `flash` is exactly "user-initiated pop-out": Go only sets it for a
  // click, never for a startup or character-swap restore, so a restored overlay
  // is never held open by this.
  // True while this overlay is part of the DEFAULT layout being arranged, not
  // any character's. Resolved once at mount from the popout profile — the mode
  // closes and reopens every overlay, so a fresh mount is always current.
  let editingDefaults = false;
  let setupMode = isSpecial && flash;
  let setupZone = null; // first known zone after setup began, the baseline
  let setupTimer;
  let setupCap;
  // Setup mode is a placement aid, so it also expires on its own. Zoning and
  // locking were the only two exits, and neither is guaranteed to happen: pop
  // out an overlay while standing in the raid zone, never lock, and it stayed
  // in setup mode for the rest of the night — visible, title bar and all, with
  // nothing in it. Randoms and Raid Specific Timers showed it first because
  // they're the two that are usually empty.
  const SETUP_MAX_MS = 90000;
  function beginSetup() {
    setupMode = true;
    setupZone = null;
    clearTimeout(setupCap);
    setupCap = setTimeout(endSetup, SETUP_MAX_MS);
    clearInterval(setupTimer);
    setupTimer = setInterval(setupWatch, 2000);
    setupWatch();
    scheduleFit();
  }
  function endSetup() {
    if (!setupMode) return;
    setupMode = false;
    clearInterval(setupTimer);
    clearTimeout(setupCap);
    scheduleFit(); // the content may collapse away now
  }
  async function setupWatch() {
    if (!setupMode) return;
    try {
      const z = (await GetCurrentZone()) || "";
      if (!z) return; // zone not known yet — not a zone CHANGE
      if (setupZone === null) {
        setupZone = z;
        return;
      }
      if (z.toLowerCase() !== setupZone.toLowerCase()) endSetup();
    } catch {
      /* keep waiting */
    }
  }

  // A flashing title bar is always shown, whatever the visibility mode — the
  // flash exists to be seen. Setup mode likewise keeps the title (and so the
  // drag handle) available while the overlay is being placed.
  // Arranging defaults needs every overlay grabbable and badged, so the
  // hide-the-title-bar modes are suspended for the duration.
  $: titleHidden =
    lockable &&
    !flashing &&
    !setupMode &&
    !editingDefaults &&
    ((titleMode === "locked" && locked) ||
      (titleMode === "zero" && !hasContent));

  // ── locate flash: the title bar turns red and fades back to normal over
  // 10s (FLASH_MS; keep in sync with the .tb-flash.fade transition) so a
  // freshly popped — or re-popped, already-open — overlay is easy to spot. ────
  const FLASH_MS = 10000;
  let flashing = false; // red layer mounted
  let flashFade = false; // fade running (opacity 1 → 0 via CSS transition)
  let flashTimer;
  function startFlash() {
    clearTimeout(flashTimer);
    // Drop and remount the layer so a re-flash restarts at full red even if a
    // fade is mid-flight.
    flashing = false;
    flashFade = false;
    requestAnimationFrame(() => {
      flashing = true;
      // Second frame: the layer has painted at opacity 1, now start the fade.
      requestAnimationFrame(() => (flashFade = true));
    });
    flashTimer = setTimeout(() => (flashing = false), FLASH_MS + 1000);
  }

  // Snap-to-grid (app-wide, from Manage Overlays): resize snaps in the grip
  // handler below; native window moves snap once the drag settles (snapTimer).
  const GRID = 10;
  let snapEnabled = false;
  const snap = (v) => Math.round(v / GRID) * GRID;

  function readStore() {
    try {
      return JSON.parse(localStorage.getItem(KEY) || "{}");
    } catch {
      return {};
    }
  }
  function writeStore(patch) {
    try {
      localStorage.setItem(KEY, JSON.stringify({ ...readStore(), ...patch }));
    } catch {
      /* ignore quota errors */
    }
  }
  function saveSettings() {
    writeStore({ settings });
  }

  function hexToRgba(hex, a) {
    const m = /^#?([0-9a-f]{6})$/i.exec(hex || "");
    if (!m) return `rgba(15,17,23,${a})`;
    const n = parseInt(m[1], 16);
    return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
  }
  $: bgRgba = hexToRgba(settings.bg, settings.opacity);
  // Title bar stays a visible handle even when the content is fully transparent,
  // so there's always something to grab and drag by.
  $: titleBg = hexToRgba(
    settings.bg,
    Math.min(1, Math.max(0.55, settings.opacity + 0.15)),
  );
  // The map paints its own background on the canvas (bgFill), so the shell
  // behind it stays fully transparent — otherwise the alpha stacks twice.
  $: shellBg = kind === "map" ? "transparent" : bgRgba;

  function setAOT(v) {
    settings.aot = v;
    saveSettings();
    Window.SetAlwaysOnTop(v).catch(() => {});
  }

  // ── geometry: reported to Go so it can recreate this overlay at the same
  // spot on next launch. Snapshotted periodically because window dragging is
  // native — there's no reliable "drag ended" event to hook. ──────────────────
  let geomTimer;
  let lastGeom = "";
  async function saveGeometry() {
    if (collapsed) return; // don't snapshot the collapsed (title-bar) height
    try {
      const s = await Window.Size();
      // Absolute screen coordinates, matching the Go side's WindowXY placement
      // on reopen (openPopoutWindow passes no Screen, so X/Y are absolute).
      // The work-area-relative form coincides with this on a single monitor but
      // carries no screen identity, so an overlay parked on a second monitor
      // came back on the primary.
      const p = await Window.Position();
      const key = `${s.width}x${s.height}@${p.x},${p.y}`;
      if (key === lastGeom) return; // nothing moved since last snapshot
      lastGeom = key;
      await SavePopoutState(kind, category, p.x, p.y, s.width, s.height);
    } catch {
      /* window may be mid-close */
    }
  }

  // ── resize grip (lower right) — frameless windows have no OS resize border,
  // so drag the grip and drive Window.SetSize from screen-coordinate deltas ───
  let resizing = null;
  let rafPending = false;
  let targetW = 0;
  let targetH = 0;
  async function gripDown(e) {
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    try {
      const s = await Window.Size();
      resizing = { w: s.width, h: s.height, x: e.screenX, y: e.screenY };
    } catch {
      resizing = null;
    }
  }
  function gripMove(e) {
    if (!resizing) return;
    targetW = Math.round(resizing.w + (e.screenX - resizing.x));
    targetH = Math.round(resizing.h + (e.screenY - resizing.y));
    if (snapEnabled) {
      targetW = snap(targetW);
      targetH = snap(targetH);
    }
    targetW = Math.max(minW, targetW);
    // With fit on the content owns the height — dragging it would just snap
    // back on the next measurement, so the grip resizes width only.
    targetH = fitActive ? resizing.h : Math.max(minH, targetH);
    if (!rafPending) {
      rafPending = true;
      requestAnimationFrame(() => {
        rafPending = false;
        if (resizing) Window.SetSize(targetW, targetH).catch(() => {});
      });
    }
  }
  function gripUp() {
    if (!resizing) return;
    resizing = null;
    // A width change can re-wrap the content, so re-measure before snapshotting.
    scheduleFit();
    saveGeometry();
  }

  // Native window drags have no JS "drag ended" event, so snap once the position
  // has settled (unchanged since the previous tick — avoids fighting a drag in
  // progress). Resizes snap live in gripMove; this also aligns size if snap was
  // turned on after an overlay was already an off-grid size.
  let snapTimer;
  let lastSnapKey = "";
  async function snapCheck() {
    if (!snapEnabled || resizing || collapsed || collapseAnim) {
      lastSnapKey = "";
      return;
    }
    try {
      // Absolute, like saveGeometry — a grid anchored to each monitor's work
      // area would snap the same overlay to different screen positions.
      const p = await Window.Position();
      const s = await Window.Size();
      const key = `${p.x},${p.y},${s.width},${s.height}`;
      if (key === lastSnapKey) {
        const nx = snap(p.x);
        const ny = snap(p.y);
        const nw = Math.max(minW, snap(s.width));
        // Fit owns the height: grid-rounding it (and re-imposing minH) would
        // undo the measurement on every tick.
        const nh = fitActive ? s.height : Math.max(minH, snap(s.height));
        if (nx !== p.x || ny !== p.y)
          Window.SetPosition(nx, ny).catch(() => {});
        if (nw !== s.width || nh !== s.height)
          Window.SetSize(nw, nh).catch(() => {});
      }
      lastSnapKey = key;
    } catch {
      /* window may be mid-close */
    }
  }

  function close() {
    saveGeometry();
    Window.Close();
  }

  // ── auto-collapse (map overlay): after a minute with no fresh /loc, slide the
  // window up so only the title bar shows; slide back down the moment a /loc
  // arrives. "Fresh" is measured from when this overlay started watching, so a
  // manual open isn't collapsed instantly when the last /loc happens to be
  // stale. Skipped while the overlays are globally hidden (the footer/camp-out
  // hide is driven by Go — we don't fight it). ────────────────────────────────
  const AUTOHIDE_MS = 60000;
  let autoTimer;
  let collapsed = false;
  let fullHeight = 0; // remembered expanded height, restored on slide-down
  let collapseAnim = null;
  let lastLocTime = 0; // last seen PlayerPosition.time (ms)
  let lastFreshAt = 0; // wall-clock when we last saw a NEW /loc

  function titleBarHeight() {
    const tb = document.querySelector(".titlebar");
    // Distance from the window top to the title bar's bottom edge = the exact
    // collapsed height, so the map bar/content below it are fully hidden.
    return tb ? Math.ceil(tb.getBoundingClientRect().bottom) : 26;
  }

  // After sliding open, nudge the map (MapTab) to re-sync its canvas to the full
  // window size and redraw — fixes an occasional blank map after expand.
  function forceMapRedraw() {
    requestAnimationFrame(() => window.dispatchEvent(new Event("resize")));
    setTimeout(() => window.dispatchEvent(new Event("resize")), 150);
  }

  // Animate the window height for a quick slide (native resize can't tween, so
  // step it over a few frames).
  function animateHeight(width, from, to, done) {
    if (collapseAnim) {
      clearInterval(collapseAnim);
      collapseAnim = null;
    }
    const steps = 6;
    let i = 0;
    collapseAnim = setInterval(() => {
      i++;
      const h = Math.round(from + (to - from) * (i / steps));
      Window.SetSize(width, h).catch(() => {});
      if (i >= steps) {
        clearInterval(collapseAnim);
        collapseAnim = null;
        if (done) done();
      }
    }, 22);
  }

  async function setCollapsed(want) {
    if (want === collapsed) return;
    try {
      const s = await Window.Size();
      if (want) {
        fullHeight = s.height;
        collapsed = true;
        const h = titleBarHeight();
        // The window's MinHeight would otherwise stop it well short of the title
        // bar — lower the minimum so it can collapse all the way.
        await Window.SetMinSize(minW, h);
        animateHeight(s.width, s.height, h);
      } else {
        collapsed = false;
        // Keep the min low through the slide-down, then restore it and force the
        // map to redraw at full size.
        if (fullHeight > 0) {
          animateHeight(s.width, s.height, fullHeight, () => {
            Window.SetMinSize(minW, minH).catch(() => {});
            forceMapRedraw();
          });
        } else {
          await Window.SetMinSize(minW, minH);
        }
      }
    } catch {
      /* window may be mid-close */
    }
  }

  // ── settings panel sizing ───────────────────────────────────────────────────
  // The panel is drawn INSIDE this window, and a webview cannot paint outside
  // its OS window — so on an overlay smaller than the panel it was clipped,
  // with the Done button typically off-screen. Grow the window to fit while
  // the panel is open, then put it back exactly as it was.
  let panelEl;
  let settingsGrew = null; // {w,h,setW,setH} while temporarily enlarged

  // ── category style (timer/alert overlays) ───────────────────────────────────
  // The gear panel mirrors the app-side category edit dialog (minus rename):
  // bar colors, background, font, auto-pause — not just the shell settings.
  // Edits save through the same SaveTriggerCategory the dialog uses; the
  // bars/alerts re-read their style within a second, so changes show live.
  const isCatOverlay = kind === "timers" || kind === "alerts";
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
  let cat = null; // resolved CategoryStyle being edited (null until loaded)
  let catSaveTimer;
  async function loadCatStyle() {
    if (!isCatOverlay) return;
    try {
      cat = await GetCategoryStyle(kind, category);
    } catch {
      cat = null; // section simply doesn't render
    }
  }
  // Debounced save: sliders fire per pixel, and every change is a disk write
  // plus a triggers-changed broadcast on the Go side.
  function catChanged() {
    if (!cat) return;
    clearTimeout(catSaveTimer);
    catSaveTimer = setTimeout(async () => {
      const s = {
        ...cat,
        font_size: Math.max(
          6,
          Math.min(48, Math.round(Number(cat.font_size) || 12)),
        ),
      };
      try {
        await SaveTriggerCategory(s.name, s);
      } catch {
        /* next edit retries */
      }
    }, 250);
  }

  async function toggleSettings() {
    if (showSettings) {
      closeSettings();
      return;
    }
    // Load the category style BEFORE showing, so the panel measurement below
    // sees the full height including the style rows.
    await loadCatStyle();
    showSettings = true;
    await tick(); // panel must be in the DOM to measure
    if (!panelEl) return;
    try {
      const s = await Window.Size();
      // The panel sits at top:26 left:6; leave the same margin right and below.
      // scrollWidth/Height, not offset*: the panel's max-width/height cap it to
      // the current window, so measuring the visible box would only ever grow
      // to the size it is already clipped at.
      const needW =
        Math.ceil(Math.max(panelEl.offsetWidth, panelEl.scrollWidth)) + 14;
      const needH =
        26 + Math.ceil(Math.max(panelEl.offsetHeight, panelEl.scrollHeight)) + 8;
      const w = Math.max(s.width, needW);
      const h = Math.max(s.height, needH);
      if (w === s.width && h === s.height) return; // already big enough
      settingsGrew = { w: s.width, h: s.height, setW: w, setH: h };
      await Window.SetSize(w, h);
    } catch {
      /* window may be mid-close — the panel still scrolls, see .set-panel */
    }
  }

  async function closeSettings() {
    showSettings = false;
    const g = settingsGrew;
    settingsGrew = null;
    if (!g) return;
    try {
      const s = await Window.Size();
      // Only undo OUR growth. If the overlay was resized while the panel was
      // open, that size is the user's and shrinking it back would fight them.
      if (Math.abs(s.width - g.setW) < 3 && Math.abs(s.height - g.setH) < 3) {
        await Window.SetSize(g.w, g.h);
      }
    } catch {
      /* window may be mid-close */
    }
    scheduleFit(); // shrink-to-content resumes from the restored size
  }

  async function checkAutohide() {
    if (kind !== "map") return;
    // Don't collapse out from under an open settings panel.
    if (showSettings) return;
    // While globally hidden (footer "Hide Windows" or camp-out/idle), Go fully
    // hides the window; leave its height alone so restore comes back full-size.
    let globalHidden = false;
    try {
      globalHidden = await ArePopoutsHidden();
    } catch {
      /* treat as visible */
    }
    if (globalHidden) return;
    try {
      const p = await GetPlayerPosition();
      if (p && p.time && p.time !== lastLocTime) {
        lastLocTime = p.time;
        lastFreshAt = Date.now();
      }
    } catch {
      /* keep last */
    }
    const stale = settings.autohide && Date.now() - lastFreshAt > AUTOHIDE_MS;
    setCollapsed(stale);
  }

  // ── shrink to content (special overlays) ────────────────────────────────────
  // These overlays get sized once for a worst case — a 15-cleric chain, a full
  // debuff list — and then sit two thirds empty on a smaller raid. With `fit`
  // on, the window height follows the rendered content instead.
  //
  // The section components are `flex: 1` (flex-basis 0) so they fill a
  // fixed-height window; the .fit CSS below switches them to auto height, which
  // makes .content report its NATURAL height. That height depends only on the
  // width, never on the window height, so driving the window from it can't feed
  // back into itself. Width stays entirely the user's.
  let contentEl;
  let fitObserver;
  let lastFitH = 0;
  $: fitActive = isSpecial && !!settings.fit && !collapsed;
  // Re-fit when the toggle flips, and when content (or setup mode) appears or
  // disappears — those swap .content in and out of display:none, and relying on
  // the observer alone to notice that is fragile.
  $: fitActive, hasContent, setupMode, editingDefaults, scheduleFit();

  // Tallest we'll auto-grow to; beyond this the content is clipped rather than
  // running off the monitor, and the user can turn fit off for a scrollable box.
  function fitMaxHeight() {
    const avail = (window.screen && window.screen.availHeight) || 1080;
    return Math.max(200, Math.round(avail * 0.9));
  }

  // CSS pixels → whatever unit Window.SetSize expects, measured rather than
  // assumed. The app is PerMonitorV2 DPI-aware (build/windows/app.manifest), so
  // on a display scaled above 100% a window dimension and a CSS pixel are NOT
  // the same length: asking for `want` produced a window ~1/scale as tall as
  // the content needed, and .content's `overflow: hidden` clipped the overflow
  // — the bottom row rendered cut in half. It was invisible at 100% scaling,
  // where the two units coincide.
  //
  // Deriving the ratio from the live window instead of reaching for
  // devicePixelRatio keeps this honest either way: if Wails is already handing
  // us CSS pixels the ratio is 1 and nothing changes for anyone.
  function windowUnitsPerCssPx(winH) {
    const css = window.innerHeight;
    if (!(winH > 0) || !(css > 0)) return 1;
    const r = winH / css;
    // Real scale factors live in 1…3 (100%–300%). Anything outside that is a
    // mid-resize or mid-collapse reading, and 1 is the safe interpretation.
    return r >= 0.9 && r <= 3.5 ? r : 1;
  }

  async function applyFit() {
    // showSettings: shrink-to-content would immediately re-clip the panel we
    // just grew the window for.
    if (!fitActive || !contentEl || resizing || collapseAnim || showSettings)
      return;
    const tb = titleBarHeight();
    const want = Math.max(
      tb,
      Math.min(tb + Math.ceil(contentEl.offsetHeight), fitMaxHeight()),
    );
    try {
      const s = await Window.Size();
      // Compare in window units — `want` is CSS px, s.height is not necessarily.
      const wantWin = Math.ceil(want * windowUnitsPerCssPx(s.height));
      // Cache the resolved height, not the CSS one, and check it against the
      // window we actually have. The old guard short-circuited on `want` alone,
      // so once a window was the wrong height for any reason it stayed wrong
      // until the content changed size.
      if (Math.abs(s.height - wantWin) < 2) {
        lastFitH = wantWin;
        return;
      }
      lastFitH = wantWin;
      await Window.SetSize(s.width, wantWin);
    } catch {
      /* window may be mid-close */
    }
  }

  // Coalesce to the next frame: a content swap can fire several mutations, and
  // measuring before layout settles reads a stale height.
  let fitFrame = 0;
  function scheduleFit() {
    if (fitFrame) return;
    fitFrame = requestAnimationFrame(() => {
      fitFrame = 0;
      applyFit();
    });
  }

  // While fitting, the window must be allowed below the normal minimum — a
  // 2-line debuff list is shorter than minH. Restored when fit is turned off.
  async function applyFitMinSize() {
    try {
      await Window.SetMinSize(minW, fitActive ? titleBarHeight() : minH);
    } catch {
      /* window may be mid-close */
    }
  }
  $: fitActive, applyFitMinSize();

  onMount(async () => {
    // The window composites transparently — the global stylesheet's opaque
    // body background would block the game showing through.
    document.documentElement.style.background = "transparent";
    document.body.style.background = "transparent";
    if (kind !== "map") {
      try {
        const p = await GetPopoutProfile();
        if (p && p.char) {
          KEY = `${BASEKEY}@${p.char}`;
          editingDefaults = p.editing === "1";
          // First time on this character: seed the look from somewhere better
          // than raw defaults. The order mirrors the Go side's geometry seeding
          // (seedLayoutForLocked) so an overlay's position and its appearance
          // can't come from two different places: an authored default layout
          // first, then a configured same-class character, then — for the
          // special overlays — the legacy shared settings the per-character
          // split migrated from.
          if (!localStorage.getItem(KEY)) {
            const d =
              (p.defaults &&
                localStorage.getItem(`${BASEKEY}@${p.defaults}`)) ||
              (p.donor && localStorage.getItem(`${BASEKEY}@${p.donor}`)) ||
              (isSpecial && localStorage.getItem(BASEKEY)) ||
              "";
            if (d) localStorage.setItem(KEY, d);
          }
        }
      } catch {
        /* no character yet — fall back to the shared key */
      }
    }
    const stored = readStore();
    if (stored.settings) settings = { ...settings, ...stored.settings };
    // Window is created always-on-top; apply a stored opt-out.
    if (!settings.aot) Window.SetAlwaysOnTop(false).catch(() => {});
    // Lock (click-through + frozen drag/resize) applies to every overlay except
    // the map, which must stay interactive (pan/zoom/buttons). Reflect the
    // global lock state and pick it up if this overlay opened while already
    // locked.
    if (lockable) {
      ArePopoutsLocked()
        .then((v) => (locked = v))
        .catch(() => {});
      Events.On("popouts-locked", () => {
        locked = true;
        endSetup(); // locking means "I'm done arranging these"
      });
      Events.On("popouts-unlocked", () => (locked = false));
      // Title-bar visibility mode (live-updated from Manage Overlays).
      const loadTitleMode = () =>
        GetOverlayTitleMode()
          .then((m) => (titleMode = m))
          .catch(() => {});
      loadTitleMode();
      Events.On("overlay-titles", loadTitleMode);
    }
    // Locate flash: on mount for a fresh user-initiated pop-out, and on the
    // targeted event Go sends when the user pops out an overlay that is
    // already open (no duplicate is created — this one flashes instead).
    if (flash) startFlash();
    Events.On("popout-flash", (ev) => {
      const name = ev && ev.data != null ? ev.data : ev;
      if (name !== WINDOW_NAME) return;
      startFlash();
      // Pressing Pop out on an overlay that's already open is how you get hold
      // of an empty one again — with nothing to show it renders no box, so
      // there is nothing to grab. Re-entering setup mode gives a full
      // placement window instead of the flash's ten seconds.
      if (isSpecial) beginSetup();
    });
    // Snap-to-grid applies to every overlay (map + trigger); live-updated.
    const loadSnap = () =>
      GetSnapToGrid()
        .then((v) => (snapEnabled = v))
        .catch(() => {});
    loadSnap();
    Events.On("snap-grid", loadSnap);
    snapTimer = setInterval(snapCheck, 150);
    // Hold a freshly popped-out raid overlay visible until it's been placed —
    // until a zone change, a lock, or SETUP_MAX_MS, whichever comes first.
    if (setupMode) beginSetup();
    // Content height changes as the raid card gains/loses rows — re-fit on each.
    if (isSpecial && contentEl && typeof ResizeObserver !== "undefined") {
      fitObserver = new ResizeObserver(scheduleFit);
      fitObserver.observe(contentEl);
    }
    // Geometry is restored by Go at window-creation time (flash-free); we only
    // report changes back so the next launch has the latest position/size.
    geomTimer = setInterval(saveGeometry, 2000);
    if (kind === "map") {
      lastFreshAt = Date.now(); // grace period: stay full-size for the first minute
      autoTimer = setInterval(checkAutohide, 2000);
    }
  });
  onDestroy(() => {
    clearInterval(geomTimer);
    clearInterval(autoTimer);
    clearInterval(snapTimer);
    clearInterval(setupTimer);
    clearTimeout(setupCap);
    clearTimeout(flashTimer);
    clearTimeout(catSaveTimer);
    if (fitFrame) cancelAnimationFrame(fitFrame);
    if (fitObserver) fitObserver.disconnect();
    if (collapseAnim) clearInterval(collapseAnim);
  });
</script>

<!-- The shell itself paints nothing: the title bar and the content each carry
     their own background, so an overlay with no content leaves no translucent
     rectangle floating over the game. -->
<div class="popout">
  <!-- title bar: the draggable region (frozen when locked); buttons opt out.
       Hidden via visibility (not display) so its height is kept and the bars
       below don't shift. -->
  <div
    class="titlebar"
    class:titlehidden={titleHidden}
    style="--wails-draggable: {locked
      ? 'no-drag'
      : 'drag'}; background:{titleBg}"
  >
    {#if flashing}
      <div class="tb-flash" class:fade={flashFade}></div>
    {/if}
    <button
      class="tb-btn"
      style="--wails-draggable: no-drag"
      title="Overlay settings"
      on:click={toggleSettings}
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
          d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"
        />
        <circle cx="12" cy="12" r="3" />
      </svg>
    </button>
    <span class="tb-title" class:defaults={editingDefaults}>{title}</span>
    {#if editingDefaults}
      <span
        class="tb-defaults"
        title="You are arranging the DEFAULT overlay layout, not this character's. Every character without a layout of their own starts from this."
        >DEFAULT</span
      >
    {/if}
    <button
      class="tb-btn"
      style="--wails-draggable: no-drag"
      title="Close overlay"
      on:click={close}
    >
      <svg
        viewBox="0 0 24 24"
        width="12"
        height="12"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
      >
        <path d="M6 6l12 12M18 6 6 18" />
      </svg>
    </button>
  </div>

  <!-- content is hidden while collapsed so the map bar can't peek below the
       title bar (Windows won't let the window shrink to exactly the title bar) -->
  <div
    class="content"
    class:collapsed
    class:fit={fitActive}
    class:empty={isSpecial && !hasContent && !setupMode && !editingDefaults}
    style="background:{shellBg}"
    bind:this={contentEl}
  >
    {#if kind === "map"}
      <MapTab popout={true} bgFill={bgRgba} />
    {:else if kind === "raidassign"}
      <PopoutRaidSection section="assign" bind:hasContent />
    {:else if kind === "raiddebuffs"}
      <PopoutRaidSection section="debuffs" bind:hasContent />
    {:else if kind === "raidclerics"}
      <PopoutRaidSection section="clerics" bind:hasContent />
    {:else if kind === "othertimers"}
      <OtherTimers showLabel={false} bind:hasAny={hasContent} />
    {:else if kind === "voicespeakers"}
      <PopoutVoiceSpeakers bind:hasContent />
    {:else if kind === "randoms"}
      <PopoutRandoms bind:hasContent />
    {:else if kind === "threat"}
      <PopoutThreat bind:hasContent />
    {:else if kind === "raiddps"}
      <RaidDPS showLabel={false} bind:hasAny={hasContent} />
    {:else if kind === "alerts"}
      <PopoutAlerts {category} bind:hasContent />
    {:else}
      <PopoutTimers {category} bind:hasContent />
    {/if}
  </div>

  <!-- Width-only resize: while the content owns the height, a corner grip would
       advertise a height drag we then ignore, so it's swapped for a strip down
       the right edge. Same pointer handlers — gripMove already pins the height
       when fit is active. -->
  <div
    class="wgrip"
    class:hidden={!fitActive || locked || collapsed}
    title="Drag to set width — height follows the content"
    on:pointerdown={gripDown}
    on:pointermove={gripMove}
    on:pointerup={gripUp}
    on:pointercancel={gripUp}
  ></div>

  <!-- corner resize grip (hidden while locked, collapsed, or fitting) -->
  <div
    class="grip"
    class:hidden={locked || collapsed || fitActive}
    on:pointerdown={gripDown}
    on:pointermove={gripMove}
    on:pointerup={gripUp}
    on:pointercancel={gripUp}
  >
    <svg
      viewBox="0 0 12 12"
      width="12"
      height="12"
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
      stroke-linecap="round"
    >
      <path d="M10.5 5 5 10.5M10.5 9 9 10.5" />
    </svg>
  </div>

  {#if showSettings}
    <div class="set-panel" bind:this={panelEl}>
      <div class="set-title">Overlay Settings</div>
      <label class="set-row">
        <span>Background</span>
        <input
          type="color"
          value={settings.bg}
          on:input={(e) => {
            settings.bg = e.target.value;
            saveSettings();
          }}
        />
      </label>
      <label class="set-row">
        <span>Opacity</span>
        <input
          type="range"
          min="0"
          max="100"
          value={Math.round(settings.opacity * 100)}
          on:input={(e) => {
            settings.opacity = e.target.value / 100;
            saveSettings();
          }}
        />
        <span class="set-val">{Math.round(settings.opacity * 100)}%</span>
      </label>
      <label class="set-row chk">
        <input
          type="checkbox"
          checked={settings.aot}
          on:change={(e) => setAOT(e.target.checked)}
        />
        <span>Always on top</span>
      </label>
      {#if isSpecial}
        <label
          class="set-row chk"
          title="Size this overlay's height to whatever it's currently showing, so a short cleric chain or debuff list doesn't leave the rest of the window empty. Width stays where you put it."
        >
          <input
            type="checkbox"
            checked={settings.fit}
            on:change={(e) => {
              settings.fit = e.target.checked;
              saveSettings();
            }}
          />
          <span>Shrink to content</span>
        </label>
      {/if}
      {#if kind === "map"}
        <label
          class="set-row chk"
          title="Slide the map up to just its title bar after a minute with no /loc, then slide it back down when a new /loc is seen."
        >
          <input
            type="checkbox"
            checked={settings.autohide}
            on:change={(e) => {
              settings.autohide = e.target.checked;
              saveSettings();
              checkAutohide();
            }}
          />
          <span>Auto-hide</span>
        </label>
      {/if}

      <!-- Category style: same options as the app's category edit dialog
           (minus rename). Applies to the CATEGORY, so it changes this
           category's look in every overlay and on every character. -->
      {#if cat}
        <div class="set-title set-title2">
          {kind === "alerts" ? "Alert Style" : "Bar Style"} — {category}
        </div>
        {#if kind === "timers"}
          <label class="set-row">
            <span>Bar color</span>
            <input type="color" bind:value={cat.bar_color} on:input={catChanged} />
          </label>
          <label class="set-row">
            <span>Bar opacity</span>
            <input
              type="range"
              min="0"
              max="100"
              value={Math.round(cat.bar_opacity * 100)}
              on:input={(e) => {
                cat.bar_opacity = e.target.value / 100;
                catChanged();
              }}
            />
            <span class="set-val">{Math.round(cat.bar_opacity * 100)}%</span>
          </label>
          <label class="set-row">
            <span>Bar background</span>
            <input type="color" bind:value={cat.bg_color} on:input={catChanged} />
          </label>
          <label class="set-row">
            <span>Track opacity</span>
            <input
              type="range"
              min="0"
              max="100"
              value={Math.round(cat.bg_opacity * 100)}
              on:input={(e) => {
                cat.bg_opacity = e.target.value / 100;
                catChanged();
              }}
            />
            <span class="set-val">{Math.round(cat.bg_opacity * 100)}%</span>
          </label>
        {:else}
          <label class="set-row">
            <span>Text background</span>
            <input type="color" bind:value={cat.bg_color} on:input={catChanged} />
          </label>
          <label class="set-row">
            <span>Bg opacity</span>
            <input
              type="range"
              min="0"
              max="100"
              value={Math.round(cat.bg_opacity * 100)}
              on:input={(e) => {
                cat.bg_opacity = e.target.value / 100;
                catChanged();
              }}
            />
            <span class="set-val">{Math.round(cat.bg_opacity * 100)}%</span>
          </label>
        {/if}
        <label class="set-row">
          <span>Font</span>
          <select class="set-sel" bind:value={cat.font_family} on:change={catChanged}>
            {#each FONTS as f (f.v)}
              <option value={f.v}>{f.label}</option>
            {/each}
          </select>
        </label>
        <label class="set-row">
          <span>Font color</span>
          <input type="color" bind:value={cat.font_color} on:input={catChanged} />
        </label>
        <label class="set-row">
          <span>Font size</span>
          <input
            class="set-num"
            type="number"
            min="6"
            max="48"
            bind:value={cat.font_size}
            on:input={catChanged}
          />
        </label>
        {#if kind === "timers"}
          <label
            class="set-row chk"
            title="Pause timers for this category when the character is out of game to ensure accurate timer retention across play sessions."
          >
            <input
              type="checkbox"
              bind:checked={cat.auto_pause}
              on:change={catChanged}
            />
            <span>Auto pause timers</span>
          </label>
        {/if}
      {/if}

      <!-- Done sits bottom-LEFT: on a very narrow overlay the panel clips at
           the right edge first, and the way out must never be what's lost. -->
      <div class="set-actions">
        <button class="set-done" on:click={closeSettings}>Done</button>
      </div>
    </div>
  {/if}
</div>

<style>
  .popout {
    position: relative;
    display: flex;
    flex-direction: column;
    height: 100vh;
    border-radius: 8px;
    overflow: hidden;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
      sans-serif;
  }
  .titlebar {
    position: relative;
    /* Above .wgrip, which spans the full right edge — the close button and the
       drag region must keep receiving pointer events in this band. */
    z-index: 25;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 6px;
    user-select: none;
    border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  }
  /* Hidden but height-preserving: the bars below keep their position. */
  .titlebar.titlehidden {
    visibility: hidden;
  }
  /* Locate flash: solid red layer over the title bar that fades out. The 10s
     duration matches FLASH_MS in the script. Title/buttons sit above it
     (position: relative wins the paint order). */
  .tb-flash {
    position: absolute;
    inset: 0;
    background: #dc2626;
    opacity: 1;
    pointer-events: none;
  }
  .tb-flash.fade {
    opacity: 0;
    transition: opacity 10s linear;
  }
  /* Arranging the default layout is a mode you can forget you're in, and the
     cost of forgetting is editing the wrong thing — so every overlay says so. */
  .tb-defaults {
    position: relative;
    flex: 0 0 auto;
    margin-right: 4px;
    padding: 1px 5px;
    border-radius: 3px;
    background: #b3541e;
    color: #fff;
    font-size: 8.5px;
    font-weight: 800;
    letter-spacing: 0.09em;
  }
  .tb-title.defaults {
    color: #ffb37a;
  }
  .tb-title {
    position: relative;
    flex: 1;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--accent);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.7);
  }
  .tb-btn {
    position: relative;
    background: none;
    border: none;
    color: var(--text-secondary);
    cursor: pointer;
    padding: 2px 3px;
    border-radius: 3px;
    display: inline-flex;
    align-items: center;
  }
  .tb-btn:hover {
    color: var(--text-primary);
    background: rgba(255, 255, 255, 0.1);
  }
  .content {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .content.collapsed {
    display: none;
  }
  /* A special overlay with nothing to show (raid over, or you've left the raid
     zone) renders no box at all — otherwise its padding alone left a visible
     translucent strip at any opacity above zero. The component stays mounted
     and polling behind this, so it reappears the moment a raid starts. */
  .content.empty {
    display: none;
  }
  /* Shrink-to-content. The shell stops stretching the content, and the section
     component inside it — which is `flex: 1`, i.e. flex-basis 0 — is switched to
     auto height. Without that override an auto-sized parent would resolve it to
     zero. .content then reports its true content height, which applyFit copies
     onto the window. */
  .content.fit {
    flex: 0 0 auto;
  }
  .content.fit > :global(*) {
    flex: 0 0 auto;
    min-height: 0;
  }
  .grip {
    position: absolute;
    right: 0;
    bottom: 0;
    width: 18px;
    height: 18px;
    z-index: 20;
    display: flex;
    align-items: flex-end;
    justify-content: flex-end;
    padding: 2px;
    color: var(--text-muted);
    cursor: nwse-resize;
    touch-action: none;
  }
  .grip:hover {
    color: var(--text-primary);
  }
  .grip.hidden {
    display: none;
  }
  /* Width-only handle: a thin strip down the right edge. It spans the full
     height but sits BELOW the title bar in stacking order, so the close button
     and the drag region still win inside that band. */
  .wgrip {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    width: 6px;
    z-index: 15;
    cursor: ew-resize;
    touch-action: none;
  }
  .wgrip:hover {
    background: rgba(255, 255, 255, 0.16);
  }
  .wgrip.hidden {
    display: none;
  }

  .set-panel {
    position: absolute;
    top: 26px;
    left: 6px;
    z-index: 30;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 190px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
    /* Belt and braces: the window is grown to fit when the panel opens, but if
       that is refused (min/max size, screen edge) the panel stays inside the
       window and scrolls rather than putting Done out of reach. Measurement
       uses scrollWidth/Height so the cap here can't shrink what we grow to. */
    max-width: calc(100vw - 12px);
    max-height: calc(100vh - 32px);
    overflow-y: auto;
  }
  .set-title {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .set-row {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .set-row > span:first-child {
    flex-shrink: 0;
  }
  .set-row input[type="color"] {
    margin-left: auto;
    width: 34px;
    height: 20px;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 3px;
    background: none;
    cursor: pointer;
  }
  .set-row input[type="range"] {
    flex: 1;
    min-width: 0;
    accent-color: var(--accent);
  }
  .set-row input[type="checkbox"] {
    accent-color: var(--accent);
  }
  .set-val {
    font-size: 11px;
    color: var(--text-muted);
    font-family: var(--font-mono);
    width: 32px;
    text-align: right;
  }
  /* Second section header (category style), separated from the rows above. */
  .set-title2 {
    margin-top: 4px;
    padding-top: 8px;
    border-top: 1px solid var(--border);
  }
  .set-sel,
  .set-num {
    margin-left: auto;
    background: var(--bg-input, rgba(0, 0, 0, 0.25));
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-primary);
    font-size: 11px;
    padding: 2px 4px;
    min-width: 0;
  }
  .set-sel {
    max-width: 130px;
  }
  .set-num {
    width: 52px;
  }
  /* Done bottom-LEFT: if the panel ever clips at the window's right edge, the
     way out is the last thing lost. */
  .set-actions {
    display: flex;
    justify-content: flex-start;
  }
  .set-done {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 10px;
  }
  .set-done:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  /* Very narrow overlay window: each row collapses into a stacked column —
     label on its own line, control full-width beneath — so nothing pushes
     past the window edge. (The panel is inside the OS window and cannot
     paint beyond it; wrapping beats clipping.) */
  @media (max-width: 300px) {
    .set-row {
      flex-wrap: wrap;
      row-gap: 3px;
    }
    .set-row > span:first-child {
      flex-basis: 100%;
    }
    /* Checkbox rows stay inline — the box belongs beside its words. */
    .set-row.chk > span {
      flex-basis: auto;
    }
    .set-row input[type="color"],
    .set-sel,
    .set-num {
      margin-left: 0;
    }
    .set-sel {
      max-width: 100%;
      flex: 1 1 auto;
    }
  }
</style>
