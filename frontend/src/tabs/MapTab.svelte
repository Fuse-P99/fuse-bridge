<script context="module">
  // State that must survive leaving/re-entering the Map tab. The component
  // unmounts when you switch tabs; module-level state persists. Only one MapTab
  // instance exists at a time.
  let mFocusLevel = false;
  let mTrail = []; // local player's trail: [{bx,by}] in player-screen space
  let mTrailZone = ""; // the PLAYER's zone (trail/temp-POI owner); updated by poll
  let mManualZone = ""; // zone the user picked to browse ("" = follow the player)
  let mTempPOIs = []; // temporary waypoints [{name,x,y,z}]; cleared on zone change
  let mShowTrail = false; // "Show Trail" toggle, default off
  let mTrackClass = ""; // "Show Track Range" class ("" | "Bard" | "Druid" | "Ranger")
  // Movement tracking for leading direction lines. key '' = local player, else a
  // guildmate name. value: {x,y,dirx,diry,movedAt} where dir is a screen-space unit.
  let mMove = {};

  const LEAD_GRACE = 2500; // ms after the last move to keep showing the leading line
</script>

<script>
  import { onMount, onDestroy, tick } from "svelte";
  import {
    GetCurrentZone,
    GetPlayerPosition,
    GetGuildMapPositions,
    GetCharacterName,
    GetZoneInfo,
    IsOfficer,
    StartMapStrobe,
    OpenPopout,
    GetFuseMarkers,
    SaveFuseMarker,
    DeleteFuseMarker,
    ShareMarkers,
  } from "../../bindings/FuseBridge/app.js";
  import ShareDialog from "../lib/ShareDialog.svelte";
  import { resolveMapBase, normalizeZone } from "../lib/zoneMaps.js";

  // When rendered inside an overlay popout: draw the canvas over a caller-chosen
  // (possibly translucent) background instead of the opaque tab background, and
  // hide the pop-out button (you can't pop out of a popout).
  export let popout = false;
  export let bgFill = "";

  let canvas, wrap;
  let ctx;
  let manifest = {}; // lowercaseKey -> { base, layers:[...] }
  let manifestBases = new Set(); // lowercase keys
  let zoneToBase = {}; // lower(zone display name) -> manifest base key

  let zoneName = ""; // zone the map is DISPLAYING (player's, or a browsed one)
  let mapBase = null; // resolved manifest base, or null

  // ── zone picker: browse any zone's map without leaving your own ────────────
  // The map follows the player's zone by default; picking a zone pins the view
  // there, and an actual player zone change snaps it back to following.
  let manualZone = mManualZone;
  let zonePickOpen = false;
  let zoneQuery = "";
  let zoneList = []; // [{name, nicks}] from GetZoneInfo, for the dropdown
  let zoneSearchEl;

  // Scrollable full list until 3+ characters are typed, then filter by zone
  // names and nicknames.
  $: zoneOptions = (() => {
    const q = zoneQuery.trim().toLowerCase();
    let list = zoneList;
    if (q.length >= 3) {
      list = zoneList.filter(
        (z) =>
          z.name.toLowerCase().includes(q) ||
          (z.nicks || []).some((n) => (n || "").toLowerCase().includes(q)),
      );
    }
    return [...list].sort((a, b) => a.name.localeCompare(b.name));
  })();

  async function toggleZonePick() {
    zonePickOpen = !zonePickOpen;
    zoneQuery = "";
    if (zonePickOpen) {
      await tick();
      zoneSearchEl && zoneSearchEl.focus();
    }
  }

  // Pick a zone to browse; "" returns to following the player's zone.
  async function pickZone(name) {
    zonePickOpen = false;
    zoneQuery = "";
    manualZone = name;
    mManualZone = name;
    const eff = name || mTrailZone;
    if (eff && eff !== zoneName) await loadZone(eff);
  }
  let layers = []; // [{ z, lines:[{x1,y1,x2,y2,color}], points:[{x,y,color,label}] }]
  let bounds = null; // { minX,maxX,minY,maxY }
  let status = "Waiting for new zone or /who output...";

  let pos = null; // {x,y,z,heading,zone,time}
  let havePos = false;
  let others = []; // guildmates [{name,x,y,z,heading}]
  let charName = "";
  let showTrail = mShowTrail; // local copy of the persisted toggle
  let focusLevel = mFocusLevel;
  let trackClass = mTrackClass; // selected class for the track-range ring, "" = off
  let trackMenuOpen = false;

  // viewport: screen = base*scale + offset
  let scale = 1,
    offsetX = 0,
    offsetY = 0;
  let follow = true;
  let reset = false;
  let view0 = false; // whether an initial fit has been done for this zone

  let dragging = false,
    lastMX = 0,
    lastMY = 0;
  // Where the press started, to tell a click (place marker) from a drag (pan).
  let downMX = 0,
    downMY = 0;
  let justLoaded = false;
  let pollTimer, drawReq;
  let popoutInit = false; // one-time player-centered zoom done (overlay only)

  // The map geometry and the player's /loc data are not always expressed with the
  // same Y-axis convention, so keep the geometry transform separate from the
  // player marker transform.
  const mapX = (x) => x;
  const mapY = (y) => y;
  const playerX = (x) => -x;
  const playerY = (y) => -y;

  // Record movement for an entity (key '' = local player) and derive a screen-space
  // heading from the last two *distinct* locs. The dot is plotted with playerX/playerY
  // (which negate), so the on-screen movement vector is normalize(-dx, -dy).
  function setMove(key, x, y) {
    const prev = mMove[key];
    if (prev && (prev.x !== x || prev.y !== y)) {
      const dx = -(x - prev.x),
        dy = -(y - prev.y);
      const len = Math.hypot(dx, dy);
      if (len > 0) {
        mMove[key] = {
          x,
          y,
          dirx: dx / len,
          diry: dy / len,
          movedAt: Date.now(),
        };
        return;
      }
    }
    // First sighting or no movement: keep prior direction, refresh position only.
    mMove[key] = {
      x,
      y,
      dirx: prev?.dirx ?? 0,
      diry: prev?.diry ?? 0,
      movedAt: prev?.movedAt ?? 0,
    };
  }

  // Draw a leading direction line if the entity moved within LEAD_GRACE. A
  // stationary entity (last two locs identical) shows no leading line.
  function drawLead(px, py, key, color) {
    const m = mMove[key];
    if (!m || !m.movedAt || (m.dirx === 0 && m.diry === 0)) return;
    if (Date.now() - m.movedAt > LEAD_GRACE) return;
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.moveTo(px, py);
    ctx.lineTo(px + m.dirx * 14, py + m.diry * 14);
    ctx.stroke();
  }

  function toggleTrail(e) {
    showTrail = e.target.checked;
    mShowTrail = showTrail;
    requestDraw();
  }

  function toggleFocusLevel(e) {
    focusLevel = e.target.checked;
    mFocusLevel = focusLevel;
    requestDraw();
  }
  function resetTrail() {
    mTrail = [];
    requestDraw();
  }

  // ── track range ring ────────────────────────────────────────────────────────
  // A class-based /who-track radius drawn as a ring centered on the local
  // player, refreshed each /loc. Radii are in map (loc) units.
  const TRACK_CLASSES = [
    { name: "Bard", radius: 100 * 7 },
    { name: "Druid", radius: 125 * 10 },
    { name: "Ranger", radius: 200 * 12 },
  ];
  const TRACK_RADII = { Bard: 100 * 7, Druid: 125 * 10, Ranger: 200 * 12 };
  let accentColor = "#4f8cff"; // resolved from --accent on mount for canvas use

  function toggleTrackMenu() {
    trackMenuOpen = !trackMenuOpen;
    if (trackMenuOpen) {
      // Only one popover open at a time.
      poiMenuOpen = false;
      poiMode = "";
    }
  }
  function selectTrackClass(name) {
    trackClass = name;
    mTrackClass = name;
    trackMenuOpen = false;
    requestDraw();
  }

  // ── points of interest ──────────────────────────────────────────────────────
  // Temporary waypoints live in module state (survive tab switches, cleared on
  // zone change). Permanent waypoints persist in localStorage keyed by zone.
  const POI_KEY = "fuse.mapPOIs";
  let poiMenuOpen = false;
  let poiMode = ""; // "" | "add" | "delete" | "share"
  let poiName = "";
  let poiX = "";
  let poiY = "";
  let poiZ = "";
  let poiPerm = false;
  let poiFuse = false; // officer: save as a Fuse Shared Marker instead
  let poiErr = "";
  let tempPOIs = mTempPOIs; // component mirror of mTempPOIs (Svelte reactivity)
  let permPOIs = []; // loaded per zone in loadZone
  // Fuse Shared Markers: officer-curated, server-stored, drawn for everyone as
  // gold flags with a static glow. Loaded per zone; refreshed on a slow cadence.
  let fuseMarkers = [];
  let fusePollCount = 0;
  // Marker sharing (send your saved markers to another player).
  let shareSel = "all"; // "all" or an index into permPOIs
  let shareDlgOpen = false;

  function loadPermPOIs(zone) {
    try {
      const all = JSON.parse(localStorage.getItem(POI_KEY) || "{}");
      return all[zone.toLowerCase()] || [];
    } catch {
      return [];
    }
  }
  function savePermPOIs(zone, list) {
    try {
      const all = JSON.parse(localStorage.getItem(POI_KEY) || "{}");
      if (list.length) all[zone.toLowerCase()] = list;
      else delete all[zone.toLowerCase()];
      localStorage.setItem(POI_KEY, JSON.stringify(all));
    } catch {
      /* ignore quota/parse errors */
    }
  }

  // Combined list for the delete panel; idx points back into its source array.
  // Fuse markers are deletable by officers only (server enforces too).
  $: allPOIs = [
    ...(isOfficer ? fuseMarkers.map((p) => ({ ...p, fuse: true })) : []),
    ...permPOIs.map((p, i) => ({ ...p, perm: true, idx: i })),
    ...tempPOIs.map((p, i) => ({ ...p, perm: false, idx: i })),
  ];

  function togglePOIMenu() {
    if (poiMenuOpen || poiMode) {
      poiMenuOpen = false;
      poiMode = "";
    } else {
      poiMenuOpen = true;
      trackMenuOpen = false;
    }
  }

  function openPOIAdd() {
    poiMenuOpen = false;
    poiMode = "add";
    poiName = "";
    // Temp waypoints belong to the player's own zone — a marker added while
    // browsing another zone must be saved (to the browsed zone) to be visible.
    poiPerm = !!manualZone;
    poiFuse = false;
    poiErr = "";
    // Default the coordinates to the player's last recorded /loc.
    poiX = havePos && pos ? String(Math.round(pos.x)) : "";
    poiY = havePos && pos ? String(Math.round(pos.y)) : "";
    poiZ = havePos && pos ? String(Math.round(pos.z)) : "";
  }

  function openPOIDelete() {
    poiMenuOpen = false;
    poiMode = "delete";
    poiErr = "";
  }

  function openPOIShare() {
    poiMenuOpen = false;
    poiMode = "share";
    shareSel = "all";
  }

  async function refreshFuseMarkers() {
    try {
      const m = (await GetFuseMarkers(zoneName)) || [];
      fuseMarkers = m;
    } catch {
      /* keep last */
    }
    requestDraw();
  }

  async function addPOI() {
    const name = poiName.trim();
    const x = parseFloat(poiX);
    const y = parseFloat(poiY);
    const z = parseFloat(poiZ);
    if (!name || isNaN(x) || isNaN(y)) return;
    poiErr = "";
    const p = { name, x, y, z: isNaN(z) ? 0 : z };
    if (poiFuse && isOfficer) {
      try {
        await SaveFuseMarker(zoneName, p.name, p.x, p.y, p.z);
        await refreshFuseMarkers();
      } catch (e) {
        poiErr = String(e);
        return;
      }
    } else if (poiPerm) {
      permPOIs = [...permPOIs, p];
      savePermPOIs(zoneName, permPOIs);
    } else {
      tempPOIs = [...tempPOIs, p];
      mTempPOIs = tempPOIs;
    }
    poiMode = "";
    requestDraw();
  }

  async function deletePOI(p) {
    if (p.fuse) {
      try {
        await DeleteFuseMarker(p.id, zoneName);
        await refreshFuseMarkers();
      } catch (e) {
        poiErr = String(e);
      }
      return;
    }
    if (p.perm) {
      permPOIs = permPOIs.filter((_, i) => i !== p.idx);
      savePermPOIs(zoneName, permPOIs);
    } else {
      tempPOIs = tempPOIs.filter((_, i) => i !== p.idx);
      mTempPOIs = tempPOIs;
    }
    requestDraw();
  }

  // The markers going into an outgoing share (all saved, or one).
  $: shareList =
    shareSel === "all"
      ? permPOIs
      : permPOIs[shareSel]
        ? [permPOIs[shareSel]]
        : [];

  // ── officer position strobe ─────────────────────────────────────────────────
  // An officer can flash their position on everyone's map for 10 seconds. The
  // flag rides on the /maplocs poll (others' dots strobe via o.strobe); the
  // officer's own dot strobes from the local timer since self isn't drawn from
  // the poll. While any strobe is live, a fast timer drives canvas redraws.
  let isOfficer = false;
  let strobeSelfUntil = 0; // ms epoch; own dot flashes until then
  let strobeTick = 0; // updated by the anim timer; drives the notice reactivity
  let strobeAnimTimer = 0;

  function anyStrobeActive() {
    return strobeSelfUntil > Date.now() || others.some((o) => o.strobe);
  }

  function startStrobeAnim() {
    if (strobeAnimTimer) return;
    strobeAnimTimer = setInterval(() => {
      strobeTick = Date.now();
      if (!anyStrobeActive()) {
        clearInterval(strobeAnimTimer);
        strobeAnimTimer = 0;
      }
      requestDraw();
    }, 150);
  }

  async function startStrobe() {
    try {
      await StartMapStrobe();
      strobeSelfUntil = Date.now() + 10000;
      strobeTick = Date.now();
      startStrobeAnim();
      requestDraw();
    } catch {
      /* server refused (not officer / offline) — nothing to flash */
    }
  }

  // Alternating red/white expanding ring under a strobing dot.
  function drawStrobe(x, y) {
    const now = Date.now();
    const phase = Math.floor(now / 200) % 2;
    const ringR = 8 + ((now % 600) / 600) * 14;
    ctx.strokeStyle = phase ? "#ff3b3b" : "#ffffff";
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(x, y, ringR, 0, Math.PI * 2);
    ctx.stroke();
    ctx.fillStyle = phase ? "rgba(255,59,59,0.35)" : "rgba(255,255,255,0.35)";
    ctx.beginPath();
    ctx.arc(x, y, 9, 0, Math.PI * 2);
    ctx.fill();
  }

  function colorOf(r, g, b) {
    const isBlack = r === 0 && g === 0 && b === 0;
    return isBlack ? "white" : `rgb(${r},${g},${b})`;
  }

  function parseMap(text) {
    const lines = [],
      points = [];
    let zsum = 0,
      zn = 0;
    for (const raw of text.split(/\r?\n/)) {
      const line = raw.trim();
      if (!line) continue;
      const kind = line[0];
      const rest = line.slice(1).trim();
      const f = rest.split(",");
      if (kind === "L" && f.length >= 9) {
        const x1 = +f[0],
          y1 = +f[1],
          z1 = +f[2],
          x2 = +f[3],
          y2 = +f[4],
          z2 = +f[5];
        const r = +f[6],
          g = +f[7],
          b = +f[8];
        lines.push({
          x1: mapX(x1),
          y1: mapY(y1),
          x2: mapX(x2),
          y2: mapY(y2),
          z: (z1 + z2) / 2, // line elevation, for player-Z opacity fade
          color: colorOf(r, g, b),
        });
        zsum += z1 + z2;
        zn += 2;
      } else if (kind === "P" && f.length >= 8) {
        const x = +f[0],
          y = +f[1],
          r = +f[3],
          g = +f[4],
          b = +f[5];
        const label = f.slice(7).join(",").trim().replace(/_/g, " ");
        points.push({ x: mapX(x), y: mapY(y), color: colorOf(r, g, b), label });
      }
    }
    return { z: zn ? zsum / zn : 0, lines, points };
  }

  async function loadManifest() {
    try {
      const res = await fetch("/maps/manifest.json");
      manifest = await res.json();
      manifestBases = new Set(Object.keys(manifest));
    } catch {
      manifest = {};
      manifestBases = new Set();
    }
  }

  async function fetchMapText(fileBase) {
    const clean = (fileBase || "").replace(/\.txt$/i, "");
    const variants = [clean, clean.toLowerCase()];
    for (const v of variants) {
      const fileName = `${v}.txt`;
      try {
        const res = await fetch(`/maps/${fileName}`);
        if (res.ok) return await res.text();
      } catch {
        /* skip */
      }
    }
    return null;
  }

  // Build a zone-display-name -> map-base lookup from the server's eqzones data.
  // For each zone, find the bundled map base among its name+nicknames, then key
  // that base under EVERY name/nickname so any in-game spelling resolves locally.
  async function loadZoneIndex() {
    try {
      const zones = (await GetZoneInfo()) || [];
      zoneList = zones.filter((z) => z && z.name);
      const idx = {};
      for (const z of zones) {
        if (!z || !z.name) continue;
        const cands = [z.name, ...(z.nicks || [])];
        let base = null;
        for (const c of cands) {
          const n = normalizeZone(c);
          if (manifestBases.has(n)) {
            base = n;
            break;
          }
        }
        if (!base) continue;
        // Key by the normalized form of every name/nickname so any in-game
        // spelling/spacing resolves (e.g. "Kael Drakkal" and "kael drakkel"
        // both normalize to "kaeldrakkel"/"kaeldrakkal" forms consistently).
        for (const c of cands) {
          const nk = normalizeZone(c);
          if (nk) idx[nk] = base;
        }
      }
      zoneToBase = idx;
    } catch {
      zoneToBase = {};
    }
  }

  async function loadZone(zone) {
    zoneName = zone;
    // Trail/temp-waypoint resets happen in poll() when the PLAYER's zone
    // changes — never here, so browsing another zone can't wipe them.
    tempPOIs = mTempPOIs;
    permPOIs = loadPermPOIs(zone);
    // Fuse Shared Markers for the new zone (async — the map draws without them
    // until they land; errors just leave the list empty).
    fuseMarkers = [];
    GetFuseMarkers(zone)
      .then((m) => {
        if (zone === zoneName) {
          fuseMarkers = m || [];
          requestDraw();
        }
      })
      .catch(() => {});
    layers = [];
    bounds = null;
    mapBase = null;
    view0 = false;
    let key =
      zoneToBase[normalizeZone(zone)] || resolveMapBase(zone, manifestBases);
    if (!key || !manifest[key]) {
      // Not in the local lookup — re-check the server's eqzones (picks up newly
      // added zone names/nicknames without a client restart), then retry.
      await loadZoneIndex();
      key =
        zoneToBase[normalizeZone(zone)] || resolveMapBase(zone, manifestBases);
    }
    if (!key || !manifest[key]) {
      status = `No map bundled for "${zone}"`;
      draw();
      return;
    }
    mapBase = key;
    const fileBase = manifest[key].base;
    const layerNums =
      manifest[key].layers && manifest[key].layers.length
        ? manifest[key].layers
        : [1];
    const loaded = [];

    // The bundled maps use the unnumbered .txt file for the base geometry and
    // numbered files such as _1.txt for POI/overlay layers.
    const baseText = await fetchMapText(fileBase);
    if (baseText) loaded.push(parseMap(baseText));

    for (const n of layerNums) {
      const layerText = await fetchMapText(`${fileBase}_${n}`);
      if (layerText) loaded.push(parseMap(layerText));
    }
    loaded.sort((a, b) => a.z - b.z);
    layers = loaded;
    if (!layers.length) {
      status = `No map data for "${zone}"`;
      draw();
      return;
    }
    // bounds across all layers
    let minX = Infinity,
      maxX = -Infinity,
      minY = Infinity,
      maxY = -Infinity;
    for (const L of layers)
      for (const ln of L.lines) {
        minX = Math.min(minX, ln.x1, ln.x2);
        maxX = Math.max(maxX, ln.x1, ln.x2);
        minY = Math.min(minY, ln.y1, ln.y2);
        maxY = Math.max(maxY, ln.y1, ln.y2);
      }
    bounds = { minX, maxX, minY, maxY };
    status = "";
    // An overlay is locked on the player, so a new zone re-follows instead of
    // fitting the whole zone; the fit below is the fallback until the first fix.
    follow = popout;
    reset = false;
    view0 = false;
    justLoaded = true;
    fitView();
    draw();
  }

  function fitView() {
    if (!bounds || !canvas) return;
    const W = Math.max(1, canvas.width || wrap?.clientWidth || 1);
    const H = Math.max(1, canvas.height || wrap?.clientHeight || 1);
    const spanX = Math.max(1, bounds.maxX - bounds.minX);
    const spanY = Math.max(1, bounds.maxY - bounds.minY);
    scale = 0.9 * Math.min(W / spanX, H / spanY);
    offsetX = W / 2 - ((bounds.minX + bounds.maxX) / 2) * scale;
    offsetY = H / 2 - ((bounds.minY + bounds.maxY) / 2) * scale;
    view0 = true;
  }

  // Per-line opacity from player elevation. Each "L" record carries a Z for both
  // endpoints (the 3rd/6th numbers); we fade a line as the player's Z moves away
  // from it, so in multi-level zones only the floor you're on lights up. Beyond
  // Z_FADE units of Z difference a line is fully transparent. Falls back to fully
  // opaque until we have a player position, so the map is always visible.
  const Z_FADE = 300;
  function lineAlpha(lineZ) {
    if (!focusLevel) return 1;
    if (!havePos || !pos || typeof pos.z !== "number") return 1;
    const dz = Math.abs(pos.z - lineZ);
    if (dz > Z_FADE) return 0;
    // linear fade: return 1 - dz / Z_FADE;

    return 1 - dz / Z_FADE;
  }

  const sx = (bx) => bx * scale + offsetX;
  const sy = (by) => by * scale + offsetY;

  function draw() {
    if (!ctx || !canvas) return;
    const W = canvas.width,
      H = canvas.height;
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 1;
    ctx.clearRect(0, 0, W, H);
    // In a popout the window composites transparently; bgFill may be a
    // translucent rgba so the game shows through behind the map lines.
    ctx.fillStyle = popout ? bgFill || "transparent" : "#0d1117";
    ctx.fillRect(0, 0, W, H);

    if (!layers.length) return;

    // Overlays are always locked on the player (no manual pan).
    if ((follow || popout) && havePos) {
      offsetX = W / 2 - playerX(pos.x) * scale;
      offsetY = H / 2 - playerY(pos.y) * scale;
    }

    const showLabels = scale > 0.35;

    ctx.lineWidth = Math.max(1, Math.round(Math.min(2, scale * 2)));
    ctx.lineJoin = "round";
    ctx.lineCap = "round";
    for (let i = 0; i < layers.length; i++) {
      const L = layers[i];
      // Group strokes by color AND quantized elevation-alpha: batches draw calls
      // for speed while still fading each line by its own Z distance from you.
      const groups = new Map(); // key -> { color, alpha, lines:[] }
      for (const ln of L.lines) {
        const a = lineAlpha(ln.z);
        if (a <= 0.02) continue;
        const bucket = Math.round(a * 20) / 20; // 0.05 opacity steps
        const key = ln.color + "|" + bucket;
        let g = groups.get(key);
        if (!g) {
          g = { color: ln.color, alpha: bucket, lines: [] };
          groups.set(key, g);
        }
        g.lines.push(ln);
      }
      for (const g of groups.values()) {
        ctx.globalAlpha = g.alpha;
        ctx.strokeStyle = g.color;
        ctx.beginPath();
        for (const ln of g.lines) {
          ctx.moveTo(sx(ln.x1), sy(ln.y1));
          ctx.lineTo(sx(ln.x2), sy(ln.y2));
        }
        ctx.stroke();
      }
      if (showLabels) {
        ctx.globalAlpha = 1;
        ctx.font = "10px sans-serif";
        ctx.textAlign = "center";
        for (const p of L.points) {
          ctx.fillStyle = p.color;
          ctx.fillText(p.label, sx(p.x), sy(p.y));
        }
      }
    }
    ctx.globalAlpha = 1;

    // guildmates (exclude self)
    ctx.font = "10px sans-serif";
    ctx.textAlign = "center";
    for (const o of others) {
      if (charName && o.name && o.name.toLowerCase() === charName.toLowerCase())
        continue;
      const x = sx(playerX(o.x)),
        y = sy(playerY(o.y));
      if (o.strobe) drawStrobe(x, y);
      drawLead(x, y, o.name, "#33d6ff"); // leading direction line (no trail for network users)
      ctx.fillStyle = "#33d6ff";
      ctx.beginPath();
      ctx.arc(x, y, 4, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = "#bfefff";
      ctx.fillText(o.name || "", x, y - 7);
    }

    // player trail (local only; persists across tab switches until zone change/
    // reset). Never drawn onto a browsed zone's map — it belongs to the
    // player's own zone.
    if (showTrail && (!manualZone || zoneName === mTrailZone) && mTrail.length > 1) {
      ctx.strokeStyle = "rgba(255,210,90,0.5)";
      ctx.lineWidth = 2;
      ctx.beginPath();
      for (let i = 0; i < mTrail.length; i++) {
        const p = mTrail[i];
        const X = sx(p.bx),
          Y = sy(p.by);
        i ? ctx.lineTo(X, Y) : ctx.moveTo(X, Y);
      }
      ctx.stroke();
    }

    // points of interest — flag pole + pennant, labeled below (always visible).
    // Saved markers belong to the displayed zone; temp waypoints belong to the
    // player's zone, so they're hidden while browsing elsewhere.
    ctx.font = "10px sans-serif";
    ctx.textAlign = "center";
    const tempsHere = !manualZone || zoneName === mTrailZone ? tempPOIs : [];
    for (const p of permPOIs.concat(tempsHere)) {
      const x = sx(playerX(p.x)),
        y = sy(playerY(p.y));
      ctx.strokeStyle = "#ff8c5a";
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(x, y);
      ctx.lineTo(x, y - 12);
      ctx.stroke();
      ctx.fillStyle = "#ff8c5a";
      ctx.beginPath();
      ctx.moveTo(x, y - 12);
      ctx.lineTo(x + 8, y - 9);
      ctx.lineTo(x, y - 6);
      ctx.closePath();
      ctx.fill();
      ctx.fillStyle = "#ffc4a8";
      ctx.fillText(p.name, x, y + 10);
    }

    // Fuse Shared Markers — officer-curated, same flag shape in gold with a
    // static glow (constant shadowBlur, no animation) so they read as official.
    for (const p of fuseMarkers) {
      const x = sx(playerX(p.x)),
        y = sy(playerY(p.y));
      ctx.save();
      ctx.shadowColor = "rgba(255, 200, 40, 0.85)";
      ctx.shadowBlur = 7;
      ctx.strokeStyle = "#f5c518";
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(x, y);
      ctx.lineTo(x, y - 12);
      ctx.stroke();
      ctx.fillStyle = "#f5c518";
      ctx.beginPath();
      ctx.moveTo(x, y - 12);
      ctx.lineTo(x + 8, y - 9);
      ctx.lineTo(x, y - 6);
      ctx.closePath();
      ctx.fill();
      ctx.restore();
      ctx.fillStyle = "#ffe9a8";
      ctx.fillText(p.name, x, y + 10);
    }

    // track range ring — class-based radius (map units), centered on the player
    if (trackClass && havePos && (!manualZone || pos.zone === zoneName)) {
      const cxp = sx(playerX(pos.x)),
        cyp = sy(playerY(pos.y));
      const r = (TRACK_RADII[trackClass] || 0) * scale;
      if (r > 1) {
        ctx.save();
        ctx.beginPath();
        ctx.arc(cxp, cyp, r, 0, Math.PI * 2);
        ctx.globalAlpha = 0.08;
        ctx.fillStyle = accentColor;
        ctx.fill();
        ctx.globalAlpha = 0.9;
        ctx.strokeStyle = accentColor;
        ctx.lineWidth = 1.5;
        ctx.stroke();
        ctx.restore();
      }
    }

    // player dot + leading direction line + name (always labeled). While
    // browsing another zone, the dot only appears if the player is actually in
    // the displayed zone.
    if (havePos && (!manualZone || pos.zone === zoneName)) {
      const x = sx(playerX(pos.x)),
        y = sy(playerY(pos.y));
      if (strobeSelfUntil > Date.now()) drawStrobe(x, y);
      drawLead(x, y, "", "#ffd25a");
      ctx.fillStyle = "#ffd25a";
      ctx.beginPath();
      ctx.arc(x, y, 5, 0, Math.PI * 2);
      ctx.fill();
      ctx.strokeStyle = "#1a1200";
      ctx.lineWidth = 1;
      ctx.stroke();
      ctx.fillStyle = "#ffe9a8";
      ctx.font = "10px sans-serif";
      ctx.textAlign = "center";
      ctx.fillText(charName || "You", x, y - 9);
    }
  }

  function requestDraw() {
    if (drawReq) return;
    drawReq = requestAnimationFrame(() => {
      drawReq = 0;
      draw();
    });
  }

  // Redraw when the overlay background color/opacity changes at runtime.
  $: if (ctx && popout) {
    bgFill;
    requestDraw();
  }

  // ── interaction ────────────────────────────────────────────────────────────
  function onWheel(e) {
    e.preventDefault();
    const factor = e.deltaY < 0 ? 1.1 : 1 / 1.1;
    followZoomAuto = false; // hand-picked zoom: don't re-derive it on resize
    // In an overlay the map is locked on the player: zoom in/out around your
    // loc (the follow recenter in draw keeps you centered), never around the
    // cursor, and never pan.
    if (popout) {
      scale *= factor;
      requestDraw();
      return;
    }
    const rect = canvas.getBoundingClientRect();
    const cx = e.clientX - rect.left,
      cy = e.clientY - rect.top;
    const wx = (cx - offsetX) / scale,
      wy = (cy - offsetY) / scale;
    scale *= factor;
    offsetX = cx - wx * scale;
    offsetY = cy - wy * scale;
    requestDraw();
  }
  function onMouseDown(e) {
    if (popout) return; // overlay map isn't pannable — clicks do nothing
    dragging = true;
    lastMX = e.clientX;
    lastMY = e.clientY;
    downMX = e.clientX;
    downMY = e.clientY;
  }
  function onMouseMove(e) {
    if (popout || !dragging) return;
    follow = false;
    reset = false;
    offsetX += e.clientX - lastMX;
    offsetY += e.clientY - lastMY;
    lastMX = e.clientX;
    lastMY = e.clientY;
    requestDraw();
  }
  function onMouseUp(e) {
    const wasPress = dragging;
    dragging = false;
    // While the Add Marker panel is open, a plain click (not a drag — the
    // panel prefills from the latest /loc, this overrides it) places the
    // marker at the clicked spot. A 2D map click has no elevation, so Z is 0.
    if (
      wasPress &&
      poiMode === "add" &&
      e &&
      e.type === "mouseup" &&
      Math.hypot(e.clientX - downMX, e.clientY - downMY) < 5
    ) {
      const rect = canvas.getBoundingClientRect();
      // Invert the draw transform: screen = playerX(loc) * scale + offset,
      // and playerX/playerY negate.
      poiX = String(Math.round(-((e.clientX - rect.left - offsetX) / scale)));
      poiY = String(Math.round(-((e.clientY - rect.top - offsetY) / scale)));
      poiZ = "0";
    }
  }

  // Follow: center on the player and zoom so ~1000 loc units are visible in every
  // direction (the smaller canvas half-dimension maps to 1000 world units).
  const FOLLOW_RADIUS = 1000;
  // The follow zoom is derived from the canvas, so it only stays correct if it's
  // recomputed whenever the canvas changes size. An overlay opens at a default
  // size and gets its saved geometry a moment later — the zoom picked for that
  // first, larger canvas is far too tight once the window shrinks to its real
  // size (see resize()).
  let followZoomAuto = true; // false once the user zooms by hand while following
  function applyFollowScale() {
    if (!canvas) return;
    const half = Math.min(canvas.width, canvas.height) / 2;
    if (half > 0) scale = half / FOLLOW_RADIUS;
    followZoomAuto = true;
  }
  function recenter() {
    // Follow while browsing another zone means "take me back to my zone".
    if (manualZone) pickZone("");
    follow = true;
    if (havePos && canvas) {
      applyFollowScale();
    } else if (!havePos) {
      fitView();
    }
    requestDraw();
  }
  function resetmap() {
    follow = false;
    fitView();
  }

  // ── polling ──────────────────────────────────────────────────────────────
  async function poll() {
    try {
      // Re-fetch the character name every poll (not just when empty) so a toon
      // swap updates the map immediately. It used to be cached at mount, so the
      // previous character's name/dot lingered until the tab was re-entered. On a
      // real change, drop the old toon's position, trail, and movement heading.
      const cn = (await GetCharacterName().catch(() => "")) || "";
      if (cn && cn.toLowerCase() !== charName.toLowerCase()) {
        charName = cn;
        pos = null;
        havePos = false;
        mTrail = [];
        delete mMove[""];
      }
      const z = await GetCurrentZone();
      if (z && z !== mTrailZone) {
        // The player actually changed zones (entered one, or /who revealed
        // it): reset the trail + temp waypoints and snap back to following —
        // any browsed zone is abandoned in favor of where the player now is.
        mTrail = [];
        mTempPOIs = [];
        tempPOIs = [];
        mTrailZone = z;
        manualZone = "";
        mManualZone = "";
      }
      // Display the browsed zone when one is pinned, else the player's zone.
      const eff = manualZone || mTrailZone;
      if (eff && eff !== zoneName) await loadZone(eff);
      const p = await GetPlayerPosition();
      if (p && p.time) {
        const changed = !pos || p.time !== pos.time;
        pos = p;
        havePos = true;
        // First fix in an overlay: zoom to a player-centered follow view.
        if (popout && !popoutInit) {
          popoutInit = true;
          recenter();
        }
        if (p.zone === zoneName) {
          setMove("", p.x, p.y);
          if (changed) {
            mTrail.push({ bx: playerX(p.x), by: playerY(p.y) });
            if (mTrail.length > 200) mTrail.shift();
          }
        }
      }
      if (mapBase && zoneName) {
        others = (await GetGuildMapPositions(zoneName)) || [];
        const present = new Set();
        for (const o of others) {
          if (
            charName &&
            o.name &&
            o.name.toLowerCase() === charName.toLowerCase()
          )
            continue;
          present.add(o.name);
          setMove(o.name, o.x, o.y);
        }
        // Forget movement state for guildmates no longer in the zone.
        for (const k of Object.keys(mMove)) {
          if (k !== "" && !present.has(k)) delete mMove[k];
        }
        // A strobing guildmate needs fast redraws (the poll is only 1s).
        if (others.some((o) => o.strobe)) startStrobeAnim();
      }
      // Slow refresh of the Fuse Shared Markers so officer edits reach every
      // open map within ~30s (the Go side caches for 60s regardless).
      if (zoneName && ++fusePollCount % 30 === 0) refreshFuseMarkers();
      requestDraw();
    } catch {
      /* ignore poll errors */
    }
  }

  function resize() {
    if (!canvas || !wrap) return;
    const W = Math.max(1, wrap.clientWidth || 0);
    const H = Math.max(1, wrap.clientHeight || 0);
    // In an overlay, ignore the collapsed (title-bar-only) height so the canvas
    // isn't shrunk to nothing — it keeps its size and redraws intact on expand.
    if (popout && H < 120) return;
    const sizeChanged = canvas.width !== W || canvas.height !== H;
    canvas.width = W;
    canvas.height = H;
    let fitted = false;
    if (!view0 || justLoaded) {
      fitView();
      justLoaded = false;
      fitted = true;
    }
    // Re-derive the follow zoom from the current canvas so ~1000 loc units stay
    // visible in every direction at any window size, and so the whole-zone fit
    // above doesn't leave a following map zoomed out. Skipped once the user has
    // zoomed by hand — their zoom then survives resizes.
    if (follow && havePos && followZoomAuto && (sizeChanged || fitted)) {
      applyFollowScale();
    }
    draw();
  }

  // Reload saved markers when a share is accepted while a map is open: the
  // inbox dispatches "fuse-pois-changed" in this window, and the browser fires
  // "storage" in any OTHER window (covers the map popout overlay).
  function onPoisChanged(e) {
    const zk = e && e.detail && e.detail.zone;
    if (!zoneName || (zk && zk !== zoneName.toLowerCase())) return;
    permPOIs = loadPermPOIs(zoneName);
    requestDraw();
  }
  function onStorageChanged(e) {
    if (e.key !== POI_KEY || !zoneName) return;
    permPOIs = loadPermPOIs(zoneName);
    requestDraw();
  }

  onMount(async () => {
    ctx = canvas.getContext("2d");
    accentColor =
      getComputedStyle(document.documentElement)
        .getPropertyValue("--accent")
        .trim() || accentColor;
    charName = (await GetCharacterName().catch(() => "")) || "";
    IsOfficer()
      .then((v) => (isOfficer = v))
      .catch(() => {});
    await loadManifest();
    await loadZoneIndex();
    await tick();
    resize();
    window.addEventListener("resize", resize);
    window.addEventListener("fuse-pois-changed", onPoisChanged);
    window.addEventListener("storage", onStorageChanged);
    await poll();
    pollTimer = setInterval(poll, 1000);
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (strobeAnimTimer) clearInterval(strobeAnimTimer);
    window.removeEventListener("resize", resize);
    window.removeEventListener("fuse-pois-changed", onPoisChanged);
    window.removeEventListener("storage", onStorageChanged);
    if (drawReq) cancelAnimationFrame(drawReq);
  });
</script>

<div class="map" class:popout>
  <div class="bar" style={popout ? `background:${bgFill}` : ""}>
    {#if popout}
      <span class="zone">{zoneName || "—"}</span>
    {:else}
      <button
        class="zone zone-btn"
        class:browsing={manualZone}
        title="Choose a zone to view"
        on:click={toggleZonePick}>{zoneName || "Select zone…"} ▾</button
      >
      {#if manualZone}
        <button
          class="btn"
          title="Return to your current zone"
          on:click={() => pickZone("")}>⟲ My Zone</button
        >
      {/if}
    {/if}
    {#if layers.length > 1}<span class="layers">{layers.length} layers</span
      >{/if}
    <button
      class="btn"
      class:active={follow}
      on:click={recenter}
      title="Center on you">Follow</button
    >
    <button
      class="btn"
      class:active={reset}
      on:click={resetmap}
      title="Reset map view">Reset</button
    >
    <div class="bar-right">
      <label class="chk"
        ><input
          type="checkbox"
          checked={focusLevel}
          on:change={toggleFocusLevel}
        /> Focus Current Level
      </label>
      <label class="chk"
        ><input type="checkbox" checked={showTrail} on:change={toggleTrail} /> Show
        Trail</label
      >
      <button
        class="btn"
        on:click={resetTrail}
        title="Clear your movement trail">Reset Trail</button
      >
      {#if !popout}
        <button
          class="btn popout-btn"
          title="Pop out the map as an overlay"
          aria-label="Pop out map"
          on:click={() => OpenPopout("map", "")}
        >
          <svg
            viewBox="0 0 24 24"
            width="13"
            height="13"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M10 6H6a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4" />
            <path d="M14 4h6v6" />
            <path d="M20 4 12 12" />
          </svg>
        </button>
      {/if}
    </div>
  </div>
  {#if zonePickOpen}
    <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
    <div class="zone-bd" on:click={() => (zonePickOpen = false)}></div>
    <div class="zone-dd">
      <input
        class="zone-search"
        placeholder="Type 3+ letters to filter…"
        bind:value={zoneQuery}
        bind:this={zoneSearchEl}
        on:keydown={(e) => {
          if (e.key === "Escape") zonePickOpen = false;
          if (e.key === "Enter" && zoneOptions.length === 1)
            pickZone(zoneOptions[0].name);
        }}
      />
      <div class="zone-opts">
        {#if mTrailZone}
          <button class="zone-opt zone-cur" on:click={() => pickZone("")}
            >Current: {mTrailZone}</button
          >
        {/if}
        {#each zoneOptions as z (z.name)}
          <button
            class="zone-opt"
            class:sel={z.name === zoneName}
            on:click={() => pickZone(z.name)}>{z.name}</button
          >
        {:else}
          <div class="zone-none">No zones match "{zoneQuery}"</div>
        {/each}
      </div>
    </div>
  {/if}

  <div class="canvas-wrap" bind:this={wrap}>
    {#if status}<div class="status">{status}</div>{/if}
    <canvas
      bind:this={canvas}
      on:wheel={onWheel}
      on:mousedown={onMouseDown}
      on:mousemove={onMouseMove}
      on:mouseup={onMouseUp}
      on:mouseleave={onMouseUp}
    ></canvas>

    <!-- points of interest (lower-left) -->
    <div class="poi-ui">
      {#if poiMode === "add"}
        <div class="poi-panel">
          <div class="poi-title">Add Marker</div>
          <input
            class="poi-in"
            placeholder="Name"
            bind:value={poiName}
            on:keydown={(e) => e.key === "Enter" && addPOI()}
          />
          <div class="poi-coords">
            <label for="poi-in">X: </label><input
              size="10"
              class="poi-in"
              placeholder="X"
              bind:value={poiX}
            />
            <label for="poi-in">Y: </label><input
              size="10"
              class="poi-in"
              placeholder="Y"
              bind:value={poiY}
            />
            <label for="poi-in">Z: </label><input
              size="10"
              class="poi-in"
              placeholder="Z"
              bind:value={poiZ}
            />
          </div>
          <label class="poi-chk">
            <input
              type="checkbox"
              bind:checked={poiPerm}
              disabled={poiFuse || !!manualZone}
            /> Save marker permanently
          </label>
          {#if isOfficer && !popout}
            <label class="poi-chk poi-fuse-chk">
              <input type="checkbox" bind:checked={poiFuse} /> Save as Fuse Shared
              Marker (everyone sees it)
            </label>
          {/if}
          {#if poiErr}<div class="poi-err">{poiErr}</div>{/if}
          <div class="poi-actions">
            <button class="btn" on:click={addPOI}>Add</button>
            <button class="btn" on:click={() => (poiMode = "")}>Cancel</button>
          </div>
        </div>
      {:else if poiMode === "delete"}
        <div class="poi-panel">
          <div class="poi-title">Delete Marker</div>
          {#each allPOIs as p}
            <button class="poi-item" on:click={() => deletePOI(p)}>
              <span class="poi-x">✕</span>
              {p.name}
              <span class="poi-tag" class:fuse={p.fuse}
                >{p.fuse ? "fuse" : p.perm ? "saved" : "temp"}</span
              >
            </button>
          {:else}
            <div class="poi-empty">No markers in this zone</div>
          {/each}
          {#if poiErr}<div class="poi-err">{poiErr}</div>{/if}
          <div class="poi-actions">
            <button class="btn" on:click={() => (poiMode = "")}>Close</button>
          </div>
        </div>
      {:else if poiMode === "share"}
        <div class="poi-panel">
          <div class="poi-title">Share Markers</div>
          <label class="poi-chk">
            <input type="radio" bind:group={shareSel} value="all" /> All saved markers
            in {zoneName} ({permPOIs.length})
          </label>
          {#each permPOIs as p, i}
            <label class="poi-chk">
              <input type="radio" bind:group={shareSel} value={i} />
              {p.name}
            </label>
          {/each}
          <div class="poi-actions">
            <button
              class="btn"
              disabled={!shareList.length}
              on:click={() => (shareDlgOpen = true)}>Send…</button
            >
            <button class="btn" on:click={() => (poiMode = "")}>Cancel</button>
          </div>
        </div>
      {:else if poiMenuOpen}
        <div class="poi-panel poi-menu">
          <button class="poi-item" on:click={openPOIAdd}>Add Marker</button>
          <button class="poi-item" on:click={openPOIDelete}
            >Delete Marker</button
          >
          {#if !popout && permPOIs.length}
            <button class="poi-item" on:click={openPOIShare}
              >Share Markers</button
            >
          {/if}
        </div>
      {/if}
      {#if trackMenuOpen}
        <div class="poi-panel track-panel">
          <div class="poi-title">Show Track Range</div>
          {#each TRACK_CLASSES as c}
            <button
              class="poi-item"
              class:sel={trackClass === c.name}
              on:click={() => selectTrackClass(c.name)}
            >
              {c.name}
              <span class="poi-tag">{c.radius} units</span>
            </button>
          {/each}
          {#if trackClass}
            <button class="poi-item" on:click={() => selectTrackClass("")}
              >Off</button
            >
          {/if}
        </div>
      {/if}
      {#if strobeTick < strobeSelfUntil}
        <div class="strobe-notice">
          Your position is being highlighted for other users…
        </div>
      {/if}
      <div class="tool-row">
        <button
          class="btn poi-flag"
          title="Points of interest"
          on:click={togglePOIMenu}>⚑</button
        >
        <button
          class="btn track-btn poi-flag"
          class:on={trackClass}
          title="Show Track Range"
          aria-label="Show Track Range"
          on:click={toggleTrackMenu}
        >
          <svg
            viewBox="0 0 24 24"
            width="13"
            height="13"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <circle cx="12" cy="12" r="9.5" />
            <circle cx="12" cy="12" r="5" />
            <path d="M12 12 L20 6" />
          </svg>
        </button>
        {#if isOfficer}
          <button
            class="btn poi-flag strobe-btn"
            class:on={strobeTick < strobeSelfUntil}
            title="Strobe your position on everyone's map (10s)"
            on:click={startStrobe}>🚨</button
          >
        {/if}
      </div>
    </div>
  </div>
</div>

{#if shareDlgOpen}
  <ShareDialog
    title="Share Map Markers"
    previewLines={[
      `${shareList.length} marker${shareList.length === 1 ? "" : "s"} in ${zoneName}`,
      shareList.map((p) => p.name).join(", "),
    ]}
    onSend={(addr) => ShareMarkers(addr, zoneName, JSON.stringify(shareList))}
    onClose={() => (shareDlgOpen = false)}
  />
{/if}

<style>
  .map {
    position: relative; /* anchors the zone-picker dropdown (bar clips overflow) */
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
    flex-shrink: 0;
    /* Keep controls on one line in the narrow overlay (no 3-line label wrap). */
    white-space: nowrap;
    overflow: hidden;
  }
  .zone {
    color: var(--accent);
    font-weight: 600;
    font-size: 13px;
  }
  /* Zone picker: the zone label doubles as the dropdown trigger. */
  .zone-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    font-family: inherit;
  }
  .zone-btn:hover {
    text-decoration: underline;
  }
  .zone-btn.browsing {
    color: #e3a008; /* browsing a zone you're not in */
  }
  .zone-bd {
    position: fixed;
    inset: 0;
    z-index: 29;
  }
  .zone-dd {
    position: absolute;
    top: 32px;
    left: 8px;
    z-index: 30;
    width: 240px;
    display: flex;
    flex-direction: column;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 6px;
    box-shadow: 0 6px 22px rgba(0, 0, 0, 0.5);
    overflow: hidden;
  }
  .zone-search {
    margin: 8px;
    padding: 5px 8px;
    background: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 12.5px;
  }
  .zone-opts {
    max-height: 300px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    padding-bottom: 4px;
  }
  .zone-opt {
    background: none;
    border: none;
    text-align: left;
    padding: 4px 12px;
    font-size: 12.5px;
    color: var(--text-primary);
    cursor: pointer;
  }
  .zone-opt:hover {
    background: rgba(255, 255, 255, 0.07);
  }
  .zone-opt.sel {
    color: var(--accent);
    font-weight: 600;
  }
  .zone-cur {
    color: #e3a008;
    font-weight: 600;
    border-bottom: 1px solid var(--border);
    margin-bottom: 2px;
  }
  .zone-none {
    padding: 6px 12px;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }
  .layers {
    color: var(--text-muted);
    font-size: 11px;
  }
  .hint {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: auto;
  }
  .hint code {
    background: var(--border);
    border-radius: 3px;
    padding: 1px 4px;
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
  .btn.active {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .popout-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 3px 7px;
  }
  .popout-btn:hover {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .chk {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--text-secondary);
    font-size: 11px;
    cursor: pointer;
  }
  .chk input {
    accent-color: var(--accent);
  }
  .bar-right {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .canvas-wrap {
    position: relative;
    flex: 1;
    overflow: hidden;
  }
  canvas {
    display: block;
    cursor: grab;
  }
  canvas:active {
    cursor: grabbing;
  }
  /* Overlay map is locked on the player — no panning, so no grab cursor. */
  .map.popout canvas,
  .map.popout canvas:active {
    cursor: default;
  }
  .status {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    color: var(--text-muted);
    font-size: 13px;
    pointer-events: none;
    text-align: center;
  }

  /* points of interest */
  .poi-ui {
    position: absolute;
    left: 10px;
    bottom: 10px;
    z-index: 5;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
  }
  .poi-flag {
    font-size: 14px;
    padding: 3px 9px;
    line-height: 1.2;
    filter: grayscale(100%);
  }
  .track-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 3px 9px;
    line-height: 1.2;
    color: var(--text-secondary);
  }
  .track-btn.on {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .track-btn svg {
    display: block;
  }
  .poi-item.sel {
    color: var(--accent);
  }
  .tool-row {
    display: flex;
    gap: 6px;
  }
  .strobe-btn.on {
    border-color: #ff3b3b;
    animation: strobe-pulse 0.4s linear infinite;
  }
  .strobe-notice {
    background: rgba(224, 92, 92, 0.14);
    border: 1px solid #e05c5c;
    border-radius: 6px;
    color: #ffb3b3;
    font-size: 12px;
    padding: 7px 10px;
    max-width: 240px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
    animation: strobe-pulse 1s ease-in-out infinite;
  }
  @keyframes strobe-pulse {
    0%,
    100% {
      opacity: 1;
    }
    50% {
      opacity: 0.55;
    }
  }
  .poi-panel {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    max-height: 280px;
    overflow-y: auto;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
  }
  .poi-menu {
    padding: 5px;
  }
  .poi-title {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .poi-in {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 4px 8px;
    outline: none;
    width: 100%;
    min-width: 0;
  }
  .poi-in:focus {
    border-color: var(--accent-dim);
  }
  .poi-coords {
    display: flex;
    gap: 6px;
  }
  .poi-chk {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .poi-chk input {
    accent-color: var(--accent);
  }
  .poi-actions {
    display: flex;
    gap: 6px;
    justify-content: flex-end;
  }
  .poi-item {
    background: none;
    border: none;
    text-align: left;
    color: var(--text-primary);
    font-size: 12px;
    padding: 5px 7px;
    border-radius: 4px;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
  }
  .poi-item:hover {
    background: rgba(255, 255, 255, 0.06);
  }
  .poi-x {
    color: #e05c5c;
    font-weight: 700;
  }
  .poi-tag {
    margin-left: auto;
    font-size: 10px;
    color: var(--text-muted);
  }
  .poi-tag.fuse {
    color: #f5c518;
    font-weight: 700;
  }
  .poi-fuse-chk {
    color: #f5c518;
  }
  .poi-err {
    font-size: 11.5px;
    color: #ff6b6b;
    max-width: 220px;
  }
  .poi-empty {
    font-size: 12px;
    color: var(--text-muted);
    padding: 2px;
  }
</style>
