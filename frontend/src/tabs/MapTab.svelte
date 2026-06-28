<script context="module">
  // State that must survive leaving/re-entering the Map tab. The component
  // unmounts when you switch tabs; module-level state persists. Only one MapTab
  // instance exists at a time.
  let mTrail = []        // local player's trail: [{bx,by}] in player-screen space
  let mTrailZone = ''    // zone the trail belongs to; cleared when the zone changes
  let mShowTrail = false // "Show Trail" toggle, default off
  // Movement tracking for leading direction lines. key '' = local player, else a
  // guildmate name. value: {x,y,dirx,diry,movedAt} where dir is a screen-space unit.
  let mMove = {}

  const LEAD_GRACE = 2500 // ms after the last move to keep showing the leading line
</script>

<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { GetCurrentZone, GetPlayerPosition, GetGuildMapPositions, GetCharacterName, GetZoneInfo } from '../../wailsjs/go/main/App'
  import { resolveMapBase, normalizeZone } from '../lib/zoneMaps.js'

  let canvas, wrap
  let ctx
  let manifest = {}            // lowercaseKey -> { base, layers:[...] }
  let manifestBases = new Set() // lowercase keys
  let zoneToBase = {}          // lower(zone display name) -> manifest base key

  let zoneName = ''            // current EQ zone (display name)
  let mapBase  = null          // resolved manifest base, or null
  let layers   = []            // [{ z, lines:[{x1,y1,x2,y2,color}], points:[{x,y,color,label}] }]
  let bounds   = null          // { minX,maxX,minY,maxY }
  let status   = 'Waiting for new zone or /who output...'

  let pos = null               // {x,y,z,heading,zone,time}
  let havePos = false
  let others = []              // guildmates [{name,x,y,z,heading}]
  let charName = ''
  let showTrail = mShowTrail   // local copy of the persisted toggle

  // viewport: screen = base*scale + offset
  let scale = 1, offsetX = 0, offsetY = 0
  let follow = true
  let reset = false
  let view0 = false            // whether an initial fit has been done for this zone

  let dragging = false, lastMX = 0, lastMY = 0
  let justLoaded = false
  let pollTimer, drawReq

  // The map geometry and the player's /loc data are not always expressed with the
  // same Y-axis convention, so keep the geometry transform separate from the
  // player marker transform.
  const mapX = x => x
  const mapY = y => y
  const playerX = x => -x
  const playerY = y => -y

  // Record movement for an entity (key '' = local player) and derive a screen-space
  // heading from the last two *distinct* locs. The dot is plotted with playerX/playerY
  // (which negate), so the on-screen movement vector is normalize(-dx, -dy).
  function setMove(key, x, y) {
    const prev = mMove[key]
    if (prev && (prev.x !== x || prev.y !== y)) {
      const dx = -(x - prev.x), dy = -(y - prev.y)
      const len = Math.hypot(dx, dy)
      if (len > 0) {
        mMove[key] = { x, y, dirx: dx / len, diry: dy / len, movedAt: Date.now() }
        return
      }
    }
    // First sighting or no movement: keep prior direction, refresh position only.
    mMove[key] = { x, y, dirx: prev?.dirx ?? 0, diry: prev?.diry ?? 0, movedAt: prev?.movedAt ?? 0 }
  }

  // Draw a leading direction line if the entity moved within LEAD_GRACE. A
  // stationary entity (last two locs identical) shows no leading line.
  function drawLead(px, py, key, color) {
    const m = mMove[key]
    if (!m || !m.movedAt || (m.dirx === 0 && m.diry === 0)) return
    if (Date.now() - m.movedAt > LEAD_GRACE) return
    ctx.strokeStyle = color
    ctx.lineWidth = 2
    ctx.beginPath(); ctx.moveTo(px, py); ctx.lineTo(px + m.dirx * 14, py + m.diry * 14); ctx.stroke()
  }

  function toggleTrail(e) { showTrail = e.target.checked; mShowTrail = showTrail; requestDraw() }
  function resetTrail() { mTrail = []; requestDraw() }

  function colorOf(r, g, b) {
    const isBlack = r === 0 && g === 0 && b === 0
    return isBlack ? 'white' : `rgb(${r},${g},${b})`
  }

  function parseMap(text) {
    const lines = [], points = []
    let zsum = 0, zn = 0
    for (const raw of text.split(/\r?\n/)) {
      const line = raw.trim()
      if (!line) continue
      const kind = line[0]
      const rest = line.slice(1).trim()
      const f = rest.split(',')
      if (kind === 'L' && f.length >= 9) {
        const x1 = +f[0], y1 = +f[1], z1 = +f[2], x2 = +f[3], y2 = +f[4], z2 = +f[5]
        const r = +f[6], g = +f[7], b = +f[8]
        lines.push({ x1: mapX(x1), y1: mapY(y1), x2: mapX(x2), y2: mapY(y2), color: colorOf(r, g, b) })
        zsum += z1 + z2; zn += 2
      } else if (kind === 'P' && f.length >= 8) {
        const x = +f[0], y = +f[1], r = +f[3], g = +f[4], b = +f[5]
        const label = f.slice(7).join(',').trim().replace(/_/g, ' ')
        points.push({ x: mapX(x), y: mapY(y), color: colorOf(r, g, b), label })
      }
    }
    return { z: zn ? zsum / zn : 0, lines, points }
  }

  async function loadManifest() {
    try {
      const res = await fetch('/maps/manifest.json')
      manifest = await res.json()
      manifestBases = new Set(Object.keys(manifest))
    } catch { manifest = {}; manifestBases = new Set() }
  }

  async function fetchMapText(fileBase) {
    const clean = (fileBase || '').replace(/\.txt$/i, '')
    const variants = [clean, clean.toLowerCase()]
    for (const v of variants) {
      const fileName = `${v}.txt`
      try {
        const res = await fetch(`/maps/${fileName}`)
        if (res.ok) return await res.text()
      } catch { /* skip */ }
    }
    return null
  }

  // Build a zone-display-name -> map-base lookup from the server's eqzones data,
  // matching each zone's long name and nicknames against the bundled map bases.
  async function loadZoneIndex() {
    try {
      const zones = await GetZoneInfo() || []
      const idx = {}
      for (const z of zones) {
        if (!z || !z.name) continue
        const cands = [z.name, ...(z.nicks || [])]
        for (const c of cands) {
          const n = normalizeZone(c)
          if (manifestBases.has(n)) { idx[z.name.toLowerCase()] = n; break }
        }
      }
      zoneToBase = idx
    } catch { zoneToBase = {} }
  }

  async function loadZone(zone) {
    zoneName = zone
    // Clear the trail only when the actual zone changes — not when the component
    // remounts (tab switch) for the same zone.
    if (zone !== mTrailZone) { mTrail = []; mTrailZone = zone }
    layers = []; bounds = null; mapBase = null; view0 = false
    const key = zoneToBase[zone.toLowerCase()] || resolveMapBase(zone, manifestBases)
    if (!key || !manifest[key]) { status = `No map bundled for "${zone}"`; draw(); return }
    mapBase = key
    const fileBase = manifest[key].base
    const layerNums = manifest[key].layers && manifest[key].layers.length ? manifest[key].layers : [1]
    const loaded = []

    // The bundled maps use the unnumbered .txt file for the base geometry and
    // numbered files such as _1.txt for POI/overlay layers.
    const baseText = await fetchMapText(fileBase)
    if (baseText) loaded.push(parseMap(baseText))

    for (const n of layerNums) {
      const layerText = await fetchMapText(`${fileBase}_${n}`)
      if (layerText) loaded.push(parseMap(layerText))
    }
    loaded.sort((a, b) => a.z - b.z)
    layers = loaded
    if (!layers.length) { status = `No map data for "${zone}"`; draw(); return }
    // bounds across all layers
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity
    for (const L of layers) for (const ln of L.lines) {
      minX = Math.min(minX, ln.x1, ln.x2); maxX = Math.max(maxX, ln.x1, ln.x2)
      minY = Math.min(minY, ln.y1, ln.y2); maxY = Math.max(maxY, ln.y1, ln.y2)
    }
    bounds = { minX, maxX, minY, maxY }
    status = ''
    follow = false
    reset = false
    view0 = false
    justLoaded = true
    fitView()
    draw()
  }

  function fitView() {
    if (!bounds || !canvas) return
    const W = Math.max(1, canvas.width || wrap?.clientWidth || 1)
    const H = Math.max(1, canvas.height || wrap?.clientHeight || 1)
    const spanX = Math.max(1, bounds.maxX - bounds.minX)
    const spanY = Math.max(1, bounds.maxY - bounds.minY)
    scale = 0.9 * Math.min(W / spanX, H / spanY)
    offsetX = W / 2 - ((bounds.minX + bounds.maxX) / 2) * scale
    offsetY = H / 2 - ((bounds.minY + bounds.maxY) / 2) * scale
    view0 = true
  }

  // Per-layer opacity from player elevation. For now, draw all layers so
  // the map geometry is always visible in the pane.
  function layerAlphas() {
    return layers.map(() => 1)
  }

  const sx = bx => bx * scale + offsetX
  const sy = by => by * scale + offsetY

  function draw() {
    if (!ctx || !canvas) return
    const W = canvas.width, H = canvas.height
    ctx.globalCompositeOperation = 'source-over'
    ctx.globalAlpha = 1
    ctx.clearRect(0, 0, W, H)
    ctx.fillStyle = '#0d1117'
    ctx.fillRect(0, 0, W, H)

    if (!layers.length) return

    if (follow && havePos) {
      offsetX = W / 2 - playerX(pos.x) * scale
      offsetY = H / 2 - playerY(pos.y) * scale
    }

    const alphas = layerAlphas()
    const showLabels = scale > 0.35

    for (let i = 0; i < layers.length; i++) {
      const a = alphas[i]
      if (a <= 0.02) continue
      ctx.globalAlpha = a
      ctx.lineWidth = Math.max(1, Math.round(Math.min(2, scale * 2)))
      ctx.lineJoin = 'round'
      ctx.lineCap = 'round'
      const L = layers[i]
      ctx.beginPath()
      let cur = null
      // group strokes by color for speed
      const byColor = new Map()
      for (const ln of L.lines) {
        if (!byColor.has(ln.color)) byColor.set(ln.color, [])
        byColor.get(ln.color).push(ln)
      }
      for (const [color, arr] of byColor) {
        ctx.strokeStyle = color
        ctx.beginPath()
        for (const ln of arr) {
          ctx.moveTo(sx(ln.x1), sy(ln.y1))
          ctx.lineTo(sx(ln.x2), sy(ln.y2))
        }
        ctx.stroke()
      }
      if (showLabels) {
        ctx.font = '10px sans-serif'
        ctx.textAlign = 'center'
        for (const p of L.points) {
          ctx.fillStyle = p.color
          ctx.fillText(p.label, sx(p.x), sy(p.y))
        }
      }
    }
    ctx.globalAlpha = 1

    // guildmates (exclude self)
    ctx.font = '10px sans-serif'
    ctx.textAlign = 'center'
    for (const o of others) {
      if (charName && o.name && o.name.toLowerCase() === charName.toLowerCase()) continue
      const x = sx(playerX(o.x)), y = sy(playerY(o.y))
      drawLead(x, y, o.name, '#33d6ff')  // leading direction line (no trail for network users)
      ctx.fillStyle = '#33d6ff'
      ctx.beginPath(); ctx.arc(x, y, 4, 0, Math.PI * 2); ctx.fill()
      ctx.fillStyle = '#bfefff'
      ctx.fillText(o.name || '', x, y - 7)
    }

    // player trail (local only; persists across tab switches until zone change/reset)
    if (showTrail && mTrail.length > 1) {
      ctx.strokeStyle = 'rgba(255,210,90,0.5)'
      ctx.lineWidth = 2
      ctx.beginPath()
      for (let i = 0; i < mTrail.length; i++) {
        const p = mTrail[i]
        const X = sx(p.bx), Y = sy(p.by)
        i ? ctx.lineTo(X, Y) : ctx.moveTo(X, Y)
      }
      ctx.stroke()
    }

    // player dot + leading direction line + name (always labeled)
    if (havePos) {
      const x = sx(playerX(pos.x)), y = sy(playerY(pos.y))
      drawLead(x, y, '', '#ffd25a')
      ctx.fillStyle = '#ffd25a'
      ctx.beginPath(); ctx.arc(x, y, 5, 0, Math.PI * 2); ctx.fill()
      ctx.strokeStyle = '#1a1200'; ctx.lineWidth = 1; ctx.stroke()
      ctx.fillStyle = '#ffe9a8'
      ctx.font = '10px sans-serif'
      ctx.textAlign = 'center'
      ctx.fillText(charName || 'You', x, y - 9)
    }
  }

  function requestDraw() {
    if (drawReq) return
    drawReq = requestAnimationFrame(() => { drawReq = 0; draw() })
  }

  // ── interaction ────────────────────────────────────────────────────────────
  function onWheel(e) {
    e.preventDefault()
    const rect = canvas.getBoundingClientRect()
    const cx = e.clientX - rect.left, cy = e.clientY - rect.top
    const factor = e.deltaY < 0 ? 1.1 : 1 / 1.1
    const wx = (cx - offsetX) / scale, wy = (cy - offsetY) / scale
    scale *= factor
    offsetX = cx - wx * scale
    offsetY = cy - wy * scale
    requestDraw()
  }
  function onMouseDown(e) { dragging = true; lastMX = e.clientX; lastMY = e.clientY }
  function onMouseMove(e) {
    if (!dragging) return
    follow = false
    reset = false
    offsetX += e.clientX - lastMX
    offsetY += e.clientY - lastMY
    lastMX = e.clientX; lastMY = e.clientY
    requestDraw()
  }
  function onMouseUp() { dragging = false }

  // Follow: center on the player and zoom so ~1000 loc units are visible in every
  // direction (the smaller canvas half-dimension maps to 1000 world units).
  const FOLLOW_RADIUS = 1000
  function recenter() {
    follow = true
    if (havePos && canvas) {
      const half = Math.min(canvas.width, canvas.height) / 2
      if (half > 0) scale = half / FOLLOW_RADIUS
    } else if (!havePos) {
      fitView()
    }
    requestDraw()
  }
  function resetmap() { follow = false; fitView()}

  // ── polling ──────────────────────────────────────────────────────────────
  async function poll() {
    try {
      // The character name may not be known at mount (log not yet identified);
      // keep trying so the local player's own server position gets filtered out.
      if (!charName) charName = (await GetCharacterName().catch(() => '')) || ''
      const z = await GetCurrentZone()
      if (z && z !== zoneName) await loadZone(z)
      const p = await GetPlayerPosition()
      if (p && p.time) {
        const changed = !pos || p.time !== pos.time
        pos = p; havePos = true
        if (p.zone === zoneName) {
          setMove('', p.x, p.y)
          if (changed) {
            mTrail.push({ bx: playerX(p.x), by: playerY(p.y) })
            if (mTrail.length > 200) mTrail.shift()
          }
        }
      }
      if (mapBase && zoneName) {
        others = await GetGuildMapPositions(zoneName) || []
        const present = new Set()
        for (const o of others) {
          if (charName && o.name && o.name.toLowerCase() === charName.toLowerCase()) continue
          present.add(o.name)
          setMove(o.name, o.x, o.y)
        }
        // Forget movement state for guildmates no longer in the zone.
        for (const k of Object.keys(mMove)) {
          if (k !== '' && !present.has(k)) delete mMove[k]
        }
      }
      requestDraw()
    } catch { /* ignore poll errors */ }
  }

  function resize() {
    if (!canvas || !wrap) return
    const W = Math.max(1, wrap.clientWidth || 0)
    const H = Math.max(1, wrap.clientHeight || 0)
    canvas.width = W
    canvas.height = H
    if (!view0 || justLoaded) {
      fitView()
      justLoaded = false
    }
    draw()
  }

  onMount(async () => {
    ctx = canvas.getContext('2d')
    charName = (await GetCharacterName().catch(() => '')) || ''
    await loadManifest()
    await loadZoneIndex()
    await tick()
    resize()
    window.addEventListener('resize', resize)
    await poll()
    pollTimer = setInterval(poll, 1000)
  })
  onDestroy(() => {
    clearInterval(pollTimer)
    window.removeEventListener('resize', resize)
    if (drawReq) cancelAnimationFrame(drawReq)
  })
</script>

<div class="map">
  <div class="bar">
    <span class="zone">{zoneName || '—'}</span>
    {#if layers.length > 1}<span class="layers">{layers.length} layers</span>{/if}
    <button class="btn" class:active={follow} on:click={recenter} title="Center on you">Follow</button>
    <button class="btn" class:active={reset} on:click={resetmap} title="Reset map view">Reset</button>
    <div class="bar-right">
      <label class="chk"><input type="checkbox" checked={showTrail} on:change={toggleTrail} /> Show Trail</label>
      <button class="btn" on:click={resetTrail} title="Clear your movement trail">Reset Trail</button>
    </div>
  </div>
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
  </div>
</div>

<style>
  .map { display:flex; flex-direction:column; height:100%; overflow:hidden; }
  .bar {
    display:flex; align-items:center; gap:12px; padding:6px 12px;
    border-bottom:1px solid var(--border); background:var(--bg-secondary); flex-shrink:0;
  }
  .zone { color:var(--accent); font-weight:600; font-size:13px; }
  .layers { color:var(--text-muted); font-size:11px; }
  .hint { color:var(--text-muted); font-size:11px; margin-left:auto; }
  .hint code { background:var(--border); border-radius:3px; padding:1px 4px; }
  .btn {
    background:var(--bg-panel); border:1px solid var(--border); border-radius:3px;
    color:var(--text-secondary); cursor:pointer; font-size:11px; padding:2px 8px;
  }
  .btn.active { color:var(--accent); border-color:var(--accent-dim); }
  .chk { display:flex; align-items:center; gap:4px; color:var(--text-secondary); font-size:11px; cursor:pointer; }
  .chk input { accent-color:var(--accent); }
  .bar-right { margin-left:auto; display:flex; align-items:center; gap:12px; }
  .canvas-wrap { position:relative; flex:1; overflow:hidden; }
  canvas { display:block; cursor:grab; }
  canvas:active { cursor:grabbing; }
  .status {
    position:absolute; top:50%; left:50%; transform:translate(-50%,-50%);
    color:var(--text-muted); font-size:13px; pointer-events:none; text-align:center;
  }
</style>
