<script>
  import { onMount, tick } from 'svelte'
  import {
    GetCharNames, GetCharContent,
    IsFilteredToon, ToggleFilteredToon
  } from '../../wailsjs/go/main/App'

  let chars           = []
  let selected        = ''
  let rawContent      = ''
  let highlighted     = ''
  let query           = ''
  let excludeBots     = true
  let excludeFiltered = false
  let matchOffsets    = []
  let matchIdx        = 0
  let detailEl

  // Context menu
  let ctx = { visible: false, x: 0, y: 0, name: '', filtered: false }

  // ── data loading ──────────────────────────────────────────────────────────

  async function loadChars() {
    chars = await GetCharNames(excludeBots, excludeFiltered) || []
    if (selected && !chars.includes(selected)) {
      selected    = ''
      rawContent  = ''
      highlighted = ''
    }
  }

  async function selectChar(name) {
    selected   = name
    rawContent = await GetCharContent(name)
    rebuildHighlight()
  }

  // ── search / highlight ────────────────────────────────────────────────────

  function esc(text) {
    return text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
  }

  function rebuildHighlight() {
    if (!rawContent) { highlighted = ''; matchOffsets = []; matchIdx = 0; return }

    if (!query) {
      highlighted  = esc(rawContent)
      matchOffsets = []
      matchIdx     = 0
      return
    }

    const lower  = rawContent.toLowerCase()
    const lowerQ = query.toLowerCase()
    matchOffsets  = []
    for (let p = 0;;) {
      const i = lower.indexOf(lowerQ, p)
      if (i === -1) break
      matchOffsets.push(i)
      p = i + lowerQ.length
    }
    if (matchIdx >= matchOffsets.length) matchIdx = 0

    // Build HTML by splitting on match regions
    let html = '', last = 0
    for (let mi = 0; mi < matchOffsets.length; mi++) {
      const s = matchOffsets[mi], e = s + query.length
      html += esc(rawContent.slice(last, s))
      const cls = mi === matchIdx ? 'current' : ''
      html += `<mark class="${cls}">${esc(rawContent.slice(s, e))}</mark>`
      last = e
    }
    html += esc(rawContent.slice(last))
    highlighted = html
  }

  async function scrollToCurrent() {
    await tick()
    detailEl?.querySelector('mark.current')?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }

  function handleSearch(e) {
    query    = e.target.value
    matchIdx = 0
    rebuildHighlight()
    if (matchOffsets.length) scrollToCurrent()
  }

  function prevMatch() {
    if (!matchOffsets.length) return
    matchIdx = (matchIdx - 1 + matchOffsets.length) % matchOffsets.length
    rebuildHighlight()
    scrollToCurrent()
  }

  function nextMatch() {
    if (!matchOffsets.length) return
    matchIdx = (matchIdx + 1) % matchOffsets.length
    rebuildHighlight()
    scrollToCurrent()
  }

  // ── context menu ──────────────────────────────────────────────────────────

  async function onRightClick(e, name) {
    e.preventDefault()
    const filtered = await IsFilteredToon(name)
    ctx = { visible: true, x: e.clientX, y: e.clientY, name, filtered }
  }

  function closeCtx() { ctx = { ...ctx, visible: false } }

  async function toggleFilter() {
    await ToggleFilteredToon(ctx.name)
    closeCtx()
    await loadChars()
  }

  // ── lifecycle ─────────────────────────────────────────────────────────────

  onMount(async () => {
    await loadChars()
    window.addEventListener('click', closeCtx)
    return () => window.removeEventListener('click', closeCtx)
  })

  function onExcludeChange() { loadChars() }
</script>

<svelte:window on:keydown={e => e.key === 'Escape' && closeCtx()} />

<div class="chars">

  <!-- toolbar -->
  <div class="toolbar">
    <div class="search-row">
      <input
        class="search"
        type="text"
        placeholder="Search name, inventory, spells…"
        value={query}
        on:input={handleSearch}
      />
      {#if matchOffsets.length}
        <span class="match-info">{matchIdx + 1}/{matchOffsets.length}</span>
        <button class="nav" on:click={prevMatch} title="Previous">↑</button>
        <button class="nav" on:click={nextMatch} title="Next">↓</button>
      {/if}
    </div>
    <div class="filter-row">
      <label class="chk">
        <input type="checkbox" bind:checked={excludeBots}     on:change={onExcludeChange} />
        Exclude Bots
      </label>
      <label class="chk">
        <input type="checkbox" bind:checked={excludeFiltered} on:change={onExcludeChange} />
        Exclude Filtered
      </label>
    </div>
  </div>

  <!-- split pane -->
  <div class="split">
    <div class="list">
      {#each chars as name}
        <div
          class="char-item"
          class:sel={name === selected}
          role="button"
          tabindex="0"
          on:click={() => selectChar(name)}
          on:keydown={e => e.key === 'Enter' && selectChar(name)}
          on:contextmenu={e => onRightClick(e, name)}
        >{name}</div>
      {:else}
        <div class="empty">No characters</div>
      {/each}
    </div>

    <div class="detail" bind:this={detailEl}>
      {#if selected}
        <pre class="pre">{@html highlighted}</pre>
      {:else}
        <div class="empty">Select a character</div>
      {/if}
    </div>
  </div>
</div>

<!-- context menu -->
{#if ctx.visible}
  <div class="ctx-menu" style="left:{ctx.x}px;top:{ctx.y}px" on:click|stopPropagation>
    <button class="ctx-item" on:click={toggleFilter}>
      {ctx.filtered ? 'Unfilter' : 'Filter'} {ctx.name}
    </button>
  </div>
{/if}

<style>
  .chars { display:flex; flex-direction:column; height:100%; overflow:hidden; }

  /* toolbar */
  .toolbar {
    display:flex; flex-direction:column; gap:6px;
    padding:8px 12px;
    border-bottom:1px solid var(--border);
    background:var(--bg-secondary);
    flex-shrink:0;
  }
  .search-row { display:flex; align-items:center; gap:6px; }
  .search {
    flex:1; background:var(--bg-input); border:1px solid var(--border);
    border-radius:4px; color:var(--text-primary); font-size:12px;
    padding:5px 9px; outline:none;
  }
  .search:focus { border-color:var(--accent-dim); }
  .match-info { color:var(--text-muted); font-size:11px; white-space:nowrap; }
  .nav {
    background:var(--bg-panel); border:1px solid var(--border); border-radius:3px;
    color:var(--text-primary); cursor:pointer; font-size:12px; padding:2px 8px;
  }
  .nav:hover { border-color:var(--accent-dim); color:var(--accent); }

  .filter-row { display:flex; gap:14px; }
  .chk {
    display:flex; align-items:center; gap:5px;
    cursor:pointer; font-size:12px; color:var(--text-secondary);
  }
  .chk input { accent-color:var(--accent); }

  /* split pane */
  .split { display:flex; flex:1; overflow:hidden; }

  .list {
    width:180px; min-width:180px; overflow-y:auto;
    border-right:1px solid var(--border); background:var(--bg-panel);
  }
  .char-item {
    padding:6px 12px; cursor:pointer; font-size:12px;
    color:var(--text-secondary); transition:background 0.1s, color 0.1s;
  }
  .char-item:hover  { background:rgba(255,255,255,0.04); color:var(--text-primary); }
  .char-item.sel    { background:rgba(200,169,81,0.12);  color:var(--accent); }

  .detail { flex:1; overflow:auto; padding:10px 14px; }
  .pre {
    font-family:var(--font-mono); font-size:11px; color:var(--text-secondary);
    line-height:1.6; margin:0; white-space:pre-wrap; word-break:break-word;
    user-select:text;
  }

  .empty { padding:40px 20px; color:var(--text-muted); font-size:12px; text-align:center; }

  /* context menu */
  .ctx-menu {
    position:fixed; background:var(--bg-secondary); border:1px solid var(--border-hover);
    border-radius:4px; padding:4px 0; z-index:1000;
    box-shadow:0 4px 16px rgba(0,0,0,0.6); min-width:160px;
  }
  .ctx-item {
    display:block; width:100%; background:none; border:none;
    color:var(--text-primary); cursor:pointer; font-size:12px;
    padding:7px 14px; text-align:left; transition:background 0.1s;
  }
  .ctx-item:hover { background:rgba(200,169,81,0.1); color:var(--accent); }
</style>
