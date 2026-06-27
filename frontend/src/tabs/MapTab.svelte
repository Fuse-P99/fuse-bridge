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
  let status   = 'Waiting for zone…'

  let pos = null               // {x,y,z,heading,zone,time}
  let havePos = false
  let others = []              // guildmates [{name,x,y,z,heading}]
  let charName = ''
  let trail = []               // recent base coords [{bx,by}]

  // viewport: screen = base*scale + offset
  let scale = 1, offsetX = 0, offsetY = 0
  let follow = true
  let view0 = false            // whether an initial fit has been done for this zone

  let dragging = false, lastMX = 0, lastMY = 0
  let justLoaded = false
  let pollTimer, drawReq

  // EQ world -> base screen space (North up, East right): bx = -X, by = -Y.
  const bX = x => -x
  const bY = y => -y

  function colorOf(r, g, b) { return `rgb(${r},${g},${b})` }

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
        lines.push({ x1: bX(x1), y1: bY(y1), x2: bX(x2), y2: bY(y2), color: colorOf(r, g, b) })
        zsum += z1 + z2; zn += 2
      } else if (kind === 'P' && f.length >= 8) {
        const x = +f[0], y = +f[1], r = +f[3], g = +f[4], b = +f[5]
        const label = f.slice(7).join(',').trim().replace(/_/g, ' ')
        points.push({ x: bX(x), y: bY(y), color: colorOf(r, g, b), label })
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
    layers = []; bounds = null; mapBase = null; trail = []; view0 = false
    const key = zoneToBase[zone.toLowerCase()] || resolveMapBase(zone, manifestBases)
    if (!key || !manifest[key]) { status = `No map bundled for "${zone}"`; draw(); return }
    mapBase = key
    const fileBase = manifest[key].base
    const layerNums = manifest[key].layers && manifest[key].layers.length ? manifest[key].layers : [1]
    const loaded = []
    for (const n of layerNums) {
      try {
        const res = await fetch(`/maps/${fileBase}_${n}.txt`)
        if (!res.ok) continue
        loaded.push(parseMap(await res.text()))
      } catch { /* skip */ }
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
    view0 = false
    justLoaded = true
    fitView()
    draw()
  }

  function fitView() {
    if (!bounds || !canvas) return
    const W = canvas.width, H = canvas.height
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
      offsetX = W / 2 - bX(pos.x) * scale
      offsetY = H / 2 - bY(pos.y) * scale
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
      const x = sx(bX(o.x)), y = sy(bY(o.y))
      ctx.fillStyle = '#33d6ff'
      ctx.beginPath(); ctx.arc(x, y, 4, 0, Math.PI * 2); ctx.fill()
      ctx.fillStyle = '#bfefff'
      ctx.fillText(o.name || '', x, y - 7)
    }

    // player trail
    if (trail.length > 1) {
      ctx.strokeStyle = 'rgba(255,210,90,0.5)'
      ctx.lineWidth = 2
      ctx.beginPath()
      for (let i = 0; i < trail.length; i++) {
        const p = trail[i]
        const X = sx(p.bx), Y = sy(p.by)
        i ? ctx.lineTo(X, Y) : ctx.moveTo(X, Y)
      }
      ctx.stroke()
    }

    // player dot + heading
    if (havePos) {
      const x = sx(bX(pos.x)), y = sy(bY(pos.y))
      if (pos.heading >= 0) {
        const a = pos.heading * Math.PI / 180  // 0=N(up), CW
        const hx = Math.sin(a), hy = -Math.cos(a)
        ctx.strokeStyle = '#ffd25a'
        ctx.lineWidth = 2
        ctx.beginPath(); ctx.moveTo(x, y); ctx.lineTo(x + hx * 14, y + hy * 14); ctx.stroke()
      }
      ctx.fillStyle = '#ffd25a'
      ctx.beginPath(); ctx.arc(x, y, 5, 0, Math.PI * 2); ctx.fill()
      ctx.strokeStyle = '#1a1200'; ctx.lineWidth = 1; ctx.stroke()
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
    offsetX += e.clientX - lastMX
    offsetY += e.clientY - lastMY
    lastMX = e.clientX; lastMY = e.clientY
    requestDraw()
  }
  function onMouseUp() { dragging = false }

  function recenter() { follow = true; if (!havePos) fitView(); requestDraw() }

  // ── polling ──────────────────────────────────────────────────────────────
  async function poll() {
    try {
      const z = await GetCurrentZone()
      if (z && z !== zoneName) await loadZone(z)
      const p = await GetPlayerPosition()
      if (p && p.time) {
        const changed = !pos || p.time !== pos.time
        pos = p; havePos = true
        if (changed && p.zone === zoneName) {
          trail.push({ bx: bX(p.x), by: bY(p.y) })
          if (trail.length > 60) trail.shift()
        }
      }
      if (mapBase && zoneName) {
        others = await GetGuildMapPositions(zoneName) || []
      }
      requestDraw()
    } catch { /* ignore poll errors */ }
  }

  function resize() {
    if (!canvas || !wrap) return
    canvas.width = wrap.clientWidth
    canvas.height = wrap.clientHeight
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
    {#if !havePos}<span class="hint">Bind <code>/loc</code> to a movement key for live tracking</span>{/if}
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
  .canvas-wrap { position:relative; flex:1; overflow:hidden; }
  canvas { display:block; cursor:grab; }
  canvas:active { cursor:grabbing; }
  .status {
    position:absolute; top:50%; left:50%; transform:translate(-50%,-50%);
    color:var(--text-muted); font-size:13px; pointer-events:none; text-align:center;
  }
</style>
