<script>
  import { onMount, onDestroy } from "svelte";
  import { Window, Events } from "@wailsio/runtime";
  import {
    SavePopoutState,
    GetPlayerPosition,
    ArePopoutsHidden,
    ArePopoutsLocked,
    GetPopoutProfile,
  } from "../bindings/FuseBridge/app.js";
  import MapTab from "./tabs/MapTab.svelte";
  import PopoutTimers from "./lib/PopoutTimers.svelte";

  export let kind = "map"; // "map" | "timers"
  export let category = "";

  $: title = kind === "map" ? "Map (/loc to view)" : category || "Timers";
  // Timer overlay look settings are per character; the map's are app-wide. The
  // character suffix is resolved at mount — timer overlays are closed and
  // reopened on a swap, so a fresh mount always reflects the incoming character.
  const BASEKEY = `fuse.popout.${kind === "map" ? "map" : "timers:" + category}`;
  let KEY = BASEKEY;
  const minW = kind === "map" ? 260 : 200;
  const minH = kind === "map" ? 220 : 120;

  let showSettings = false;
  // Per-popout overlay settings, persisted in localStorage alongside geometry.
  // Timers default fully transparent (only the running bars show over the game);
  // the map defaults to a mostly-opaque dark backdrop so its lines read well.
  // Both default always-on-top.
  let settings =
    kind === "timers"
      ? { bg: "#0f1117", opacity: 0, aot: true }
      : { bg: "#0f1117", opacity: 0.85, aot: true, autohide: true };

  // Lock (click-through + frozen drag/resize) is controlled globally from the
  // Timers window; here it's driven only by the popouts-locked/unlocked events
  // (not a per-overlay setting, not persisted).
  let locked = false;

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
      // Work-area-relative so it round-trips with the Go side's WindowXY
      // placement on reopen (avoids drift across restarts).
      const p = await Window.RelativePosition();
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
    targetW = Math.max(minW, Math.round(resizing.w + (e.screenX - resizing.x)));
    targetH = Math.max(minH, Math.round(resizing.h + (e.screenY - resizing.y)));
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
    saveGeometry();
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

  async function checkAutohide() {
    if (kind !== "map") return;
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

  onMount(async () => {
    // The window composites transparently — the global stylesheet's opaque
    // body background would block the game showing through.
    document.documentElement.style.background = "transparent";
    document.body.style.background = "transparent";
    if (kind === "timers") {
      try {
        const p = await GetPopoutProfile();
        if (p && p.char) {
          KEY = `${BASEKEY}@${p.char}`;
          // First time on this character: seed from a configured same-class
          // character so an alt starts with your setup, not raw defaults.
          if (!localStorage.getItem(KEY) && p.donor) {
            const d = localStorage.getItem(`${BASEKEY}@${p.donor}`);
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
    // Lock (click-through + frozen drag/resize) applies to timer overlays only —
    // the map must stay interactive (pan/zoom/buttons). Reflect the global lock
    // state and pick it up if this overlay opened while already locked.
    if (kind === "timers") {
      ArePopoutsLocked()
        .then((v) => (locked = v))
        .catch(() => {});
      Events.On("popouts-locked", () => (locked = true));
      Events.On("popouts-unlocked", () => (locked = false));
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
    if (collapseAnim) clearInterval(collapseAnim);
  });
</script>

<div class="popout" style="background:{shellBg}">
  <!-- title bar: the draggable region (frozen when locked); buttons opt out -->
  <div
    class="titlebar"
    style="--wails-draggable: {locked
      ? 'no-drag'
      : 'drag'}; background:{titleBg}"
  >
    <button
      class="tb-btn"
      style="--wails-draggable: no-drag"
      title="Overlay settings"
      on:click={() => (showSettings = !showSettings)}
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
    <span class="tb-title">{title}</span>
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
  <div class="content" class:collapsed>
    {#if kind === "map"}
      <MapTab popout={true} bgFill={bgRgba} />
    {:else}
      <PopoutTimers {category} />
    {/if}
  </div>

  <!-- resize grip (hidden while locked or collapsed) -->
  <div
    class="grip"
    class:hidden={locked || collapsed}
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
    <div class="set-panel">
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
      <div class="set-actions">
        <button class="set-done" on:click={() => (showSettings = false)}
          >Done</button
        >
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
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 6px;
    user-select: none;
    border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  }
  .tb-title {
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
  .set-actions {
    display: flex;
    justify-content: flex-end;
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
</style>
