<script>
  import { onMount, onDestroy } from 'svelte'
  import { GetZones } from '../../wailsjs/go/main/App'

  let zones       = []
  let selectedIdx = 0
  let error       = ''
  let interval

  function since(dateStr) {
    const mins = Math.floor((Date.now() - new Date(dateStr).getTime()) / 60000)
    if (mins < 1)   return 'just now'
    if (mins === 1) return '1 minute ago'
    if (mins < 60)  return `${mins} minutes ago`
    const hrs = Math.floor(mins / 60)
    if (hrs === 1)  return '1 hour ago'
    return `${hrs} hours ago`
  }

  function buildDetail(zone) {
    if (!zone) return ''

    const guilds = {}
    for (const c of zone.characters || []) {
      const g = c.guild || '(No Guild)'
      if (!guilds[g]) guilds[g] = { total: 0, classes: {} }
      guilds[g].total++
      guilds[g].classes[c.class] = (guilds[g].classes[c.class] || 0) + 1
    }

    const names = Object.keys(guilds).sort((a, b) => {
      if (a === 'Fuse') return -1
      if (b === 'Fuse') return  1
      return a.localeCompare(b)
    })

    let out = `${zone.name} (${zone.characters?.length || 0})\n`
    out    += `Seen: ${since(zone.last_seen)}\n\n`

    for (const g of names) {
      out += `<${g}> (${guilds[g].total})\n`
      for (const cls of Object.keys(guilds[g].classes).sort())
        out += `  ${cls} - ${guilds[g].classes[cls]}\n`
    }

    out += '\n' + '─'.repeat(36) + '\n\n'

    for (const c of zone.characters || []) {
      const guild = c.guild ? ` <${c.guild}>` : ''
      const race  = c.race  ? ` (${c.race})`  : ''
      if (c.class === 'Anon' || c.class === 'Role')
        out += `[${c.class}] ${c.name}${race}${guild}\n`
      else
        out += `[${c.level} ${c.class}] ${c.name}${race}${guild}\n`
    }
    return out.trim()
  }

  async function load() {
    try { zones = await GetZones() || []; error = '' }
    catch (e) { error = String(e) }
  }

  onMount(async () => { await load(); interval = setInterval(load, 10000) })
  onDestroy(() => clearInterval(interval))

  $: zone   = zones[selectedIdx]
  $: detail = buildDetail(zone)
</script>

<div class="zones">
  <div class="list">
    {#if !zones.length}
      <div class="empty">{error || 'No zone data'}</div>
    {/if}
    {#each zones as z, i}
      <div
        class="zone-row"
        class:sel={i === selectedIdx}
        role="button"
        tabindex="0"
        on:click={() => selectedIdx = i}
        on:keydown={e => e.key === 'Enter' && (selectedIdx = i)}
      >
        <span class="zone-name">{z.name}</span>
        <span class="zone-ct">({z.characters?.length || 0})</span>
      </div>
    {/each}
  </div>

  <div class="detail">
    {#if zone}
      <pre class="pre">{detail}</pre>
    {:else}
      <div class="empty">Select a zone</div>
    {/if}
  </div>
</div>

<style>
  .zones { display:flex; height:100%; overflow:hidden; }

  .list {
    width:230px; min-width:230px; overflow-y:auto;
    border-right:1px solid var(--border); background:var(--bg-panel);
  }

  .zone-row {
    display:flex; align-items:center; justify-content:space-between;
    padding:7px 12px; cursor:pointer; font-size:12px;
    transition:background 0.1s; gap:6px;
  }
  .zone-row:hover { background:rgba(255,255,255,0.04); }
  .zone-row.sel   { background:rgba(200,169,81,0.12); }

  .zone-name {
    color:var(--text-secondary); flex:1;
    overflow:hidden; text-overflow:ellipsis; white-space:nowrap;
  }
  .zone-row.sel .zone-name { color:var(--accent); }
  .zone-ct { color:var(--text-muted); font-size:11px; white-space:nowrap; }

  .detail { flex:1; overflow:auto; padding:10px 14px; }
  .pre {
    font-family:var(--font-mono); font-size:11px; color:var(--text-secondary);
    line-height:1.6; margin:0; white-space:pre-wrap; user-select:text;
  }

  .empty { padding:40px 20px; color:var(--text-muted); font-size:12px; text-align:center; }
</style>
