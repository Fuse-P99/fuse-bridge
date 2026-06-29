<script>
  import { onMount, tick } from 'svelte'
  import {
    GetCharNames, GetCharContent, GetCharInventory,
    GetCharSpellbook, GetCharClassWithInference, GetSpellsForClass,
    GetCharInfos, RefreshCharInfos, GetSettings, SaveSettings,
    IsFilteredToon, ToggleFilteredToon
  } from '../../wailsjs/go/main/App'
  import { scale } from '../lib/scale.js'
  import { classAbbr } from '../lib/classAbbr.js'

  let chars           = []   // CharEntry[]
  let selected        = ''
  let rawContent      = ''
  let highlighted     = ''
  let query           = ''
  let excludeBots     = true
  let excludeFiltered = true
  let charInfos       = {}   // lower(name) -> { level, class }
  let matchOffsets    = []
  let matchIdx        = 0
  let detailEl

  let detailTab      = 'all'   // 'all' | 'inventory' | 'spells'
  let inventoryItems = []      // InventoryItem[]

  // Class + spellbook — loaded eagerly in selectChar
  let charClass        = ''   // canonical class name; '' = unknown
  let charClassLoading = false
  let spellbook        = null // string[] from local file; null = file not found

  // Spell list — loaded lazily when Spells tab is first opened
  let spellList     = []
  let spellsLoaded  = '' // which character's spell list is cached
  let spellsLoading = false
  let spellsError   = ''

  // Classes with no player-castable spells — hide the Spells tab for these.
  const nonCasterClasses = new Set(['Monk', 'Rogue', 'Warrior'])

  // Context menu
  let ctx = { visible: false, x: 0, y: 0, name: '', filtered: false }

  // ── data loading ──────────────────────────────────────────────────────────

  async function loadChars(keepSelection = false) {
    chars = await GetCharNames(query, excludeBots, excludeFiltered) || []
    if (!keepSelection && selected && !chars.some(e => e.name === selected)) {
      selected = ''
      rawContent = ''
      highlighted = ''
      inventoryItems = []
      clearState()
    }
    await loadCharInfos(chars.map(c => c.name))
  }

  // Populate level/class for names not yet in this session's cache: show the
  // local %APPDATA% cache instantly, then refresh from the server (which also
  // updates the on-disk cache). Each name is resolved at most once per session.
  async function loadCharInfos(names) {
    const missing = names.filter(n => !(n.toLowerCase() in charInfos))
    if (!missing.length) return
    applyCharInfos(missing, await GetCharInfos(missing) || {})       // instant (cache)
    applyCharInfos(missing, await RefreshCharInfos(missing) || {})   // fresh (server)
  }

  function applyCharInfos(names, got) {
    const merged = { ...charInfos }
    for (const n of names) {
      const k = n.toLowerCase()
      if (got[k]) merged[k] = got[k]
      else if (!(k in merged)) merged[k] = { level: 0, class: '' } // mark attempted
    }
    charInfos = merged
  }

  // meta string ("60 ENC") for a character; '' when class is unknown. infos is
  // passed explicitly so Svelte re-renders the list when the cache updates.
  function charMeta(name, infos) {
    const ci = infos[name.toLowerCase()]
    if (!ci || !ci.class) return ''
    const ab = classAbbr(ci.class)
    if (!ab) return ''
    return ci.level > 0 ? `${ci.level} ${ab}` : ab
  }

  // Last-seen zone for a character; '' when unknown.
  function charZone(name, infos) {
    const ci = infos[name.toLowerCase()]
    return ci && ci.zone ? ci.zone : ''
  }

  function clearState() {
    charClass        = ''
    charClassLoading = false
    spellbook        = null
    spellList        = []
    spellsLoaded     = ''
    spellsError      = ''
  }

  async function selectChar(name) {
    selected = name
    clearState()

    charClassLoading = true

    // Load content, inventory, and spellbook all in parallel (all local/fast).
    const [content, inventory, sb] = await Promise.all([
      GetCharContent(name),
      GetCharInventory(name),
      GetCharSpellbook(name)
    ])

    // Guard: user may have clicked a different character while we were loading.
    if (selected !== name) return

    rawContent     = content
    inventoryItems = inventory || []
    spellbook      = sb   // null = file not found; [] = file present but empty
    rebuildHighlight()

    // Resolve class via server then spellbook inference (slow — server HTTP call).
    const cls = await GetCharClassWithInference(name, sb || []) || ''
    if (selected === name) {
      charClass        = cls
      charClassLoading = false
    }
  }

  // Show the Spells tab unless class is definitively a non-caster.
  $: showSpellsTab = !charClass || !nonCasterClasses.has(charClass)

  async function openSpellsTab() {
    detailTab = 'spells'
    await loadSpellList()
  }

  async function loadSpellList() {
    if (spellsLoaded === selected) return
    if (!charClass) return  // nothing to load yet; tab shows "class unknown"
    spellsLoading = true
    spellsError   = ''
    spellList     = []
    try {
      spellList = await GetSpellsForClass(charClass) || []
    } catch (e) {
      spellsError = String(e)
    } finally {
      spellsLoading = false
      spellsLoaded  = selected
    }
  }

  // Reload the spell list when class resolves and the Spells tab is active.
  $: if (charClass && detailTab === 'spells' && spellsLoaded !== selected) {
    loadSpellList()
  }

  // ── reactive spell derivations ────────────────────────────────────────────

  // Normalize a spell name for comparison: lowercase and treat backtick as
  // apostrophe (EQ spellbook files use ` for possessives; wiki uses ').
  function normalizeName(n) { return n.toLowerCase().replace(/`/g, "'") }

  // Set of spell names from the local spellbook file (normalized for comparison).
  // null when no spellbook file exists — disables missing highlighting.
  $: spellbookSet = spellbook ? new Set(spellbook.map(normalizeName)) : null

  // Spell list grouped by level, highest level first, alpha within each level.
  $: levelGroups = (() => {
    const groups = new Map()
    for (const s of spellList) {
      if (!groups.has(s.level)) groups.set(s.level, [])
      groups.get(s.level).push(s)
    }
    return [...groups.keys()]
      .sort((a, b) => b - a)
      .map(level => ({ level, spells: groups.get(level) }))
  })()

  $: missingCount = spellbookSet
    ? spellList.filter(s => !spellbookSet.has(normalizeName(s.name))).length
    : 0

  function isMissing(spellName) {
    return spellbookSet !== null && !spellbookSet.has(normalizeName(spellName))
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

  async function handleSearch(e) {
    query    = e.target.value
    matchIdx = 0
    await loadChars(false)
    // Auto-select the top result and jump to the match in the content pane.
    if (query && chars.length) {
      detailTab = 'all'
      if (!chars.some(c => c.name === selected)) {
        await selectChar(chars[0].name)
      }
    }
    rebuildHighlight()
    if (matchOffsets.length) scrollToCurrent()
  }

  function prevMatch() {
    if (!matchOffsets.length) return
    matchIdx = (matchIdx - 1 + matchOffsets.length) % matchOffsets.length
    rebuildHighlight(); scrollToCurrent()
  }

  function nextMatch() {
    if (!matchOffsets.length) return
    matchIdx = (matchIdx + 1) % matchOffsets.length
    rebuildHighlight(); scrollToCurrent()
  }

  // ── inventory ─────────────────────────────────────────────────────────────

  function wikiLink(name) {
    return 'https://wiki.project1999.com/' + name.replace(/ /g, '_')
  }

  $: visibleInventory = query
    ? inventoryItems.filter(it =>
        it.name.toLowerCase().includes(query.toLowerCase()) ||
        it.location.toLowerCase().includes(query.toLowerCase()))
    : inventoryItems

  // ── context menu ──────────────────────────────────────────────────────────

  async function onRightClick(e, name) {
    e.preventDefault()
    const filtered = await IsFilteredToon(name)
    // The menu lives inside .shell (CSS zoom:$scale), which scales its coordinate
    // space — divide the viewport cursor coords by the zoom so it lands at the cursor.
    ctx = { visible: true, x: e.clientX / $scale, y: e.clientY / $scale, name, filtered }
  }

  function closeCtx() { ctx = { ...ctx, visible: false } }

  async function toggleFilter() {
    await ToggleFilteredToon(ctx.name)
    closeCtx()
    await loadChars()
  }

  // ── clipboard commands ────────────────────────────────────────────────────

  let copyMsg = ''
  let copyTimer

  function copyCommand(cmd) {
    navigator.clipboard.writeText(cmd)
    copyMsg = `Command copied to clipboard — ${cmd}`
    clearTimeout(copyTimer)
    copyTimer = setTimeout(() => copyMsg = '', 3000)
  }

  // ── lifecycle ─────────────────────────────────────────────────────────────

  onMount(async () => {
    const s         = await GetSettings()
    excludeBots     = s.exclude_bots     ?? true
    excludeFiltered = s.exclude_filtered ?? true
    await loadChars(true)
    window.addEventListener('click', closeCtx)
    return () => window.removeEventListener('click', closeCtx)
  })

  async function onExcludeChange() {
    const s = await GetSettings()
    await SaveSettings({ ...s, exclude_bots: excludeBots, exclude_filtered: excludeFiltered })
    loadChars(true)
  }
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
      {#if detailTab === 'all' && matchOffsets.length}
        <span class="match-info">{matchIdx + 1}/{matchOffsets.length}</span>
        <button class="nav" on:click={prevMatch} title="Previous">↑</button>
        <button class="nav" on:click={nextMatch} title="Next">↓</button>
      {/if}
    </div>
    <div class="filter-row">
      <label class="chk">
        <input type="checkbox" bind:checked={excludeBots}     on:change={onExcludeChange} />
        Exclude Bots<span class="dot dot-bot"></span>
      </label>
      <label class="chk">
        <input type="checkbox" bind:checked={excludeFiltered} on:change={onExcludeChange} />
        Exclude Filtered<span class="dot dot-filtered"></span>
      </label>
    </div>
  </div>

  <!-- split pane -->
  <div class="split">
    <div class="list">
      {#each chars as entry}
        {@const meta = charMeta(entry.name, charInfos)}
        {@const zone = charZone(entry.name, charInfos)}
        <div
          class="char-item"
          class:sel={entry.name === selected}
          role="button"
          tabindex="0"
          on:click={() => selectChar(entry.name)}
          on:keydown={e => e.key === 'Enter' && selectChar(entry.name)}
          on:contextmenu={e => onRightClick(e, entry.name)}
        >
          <div class="char-row">
            <span class="char-name">{entry.name}{#if query && entry.match_count > 0}<span class="match-badge">({entry.match_count})</span>{/if}{#if !excludeBots && entry.is_bot}<span class="dot dot-bot" title="Bot"></span>{/if}{#if !excludeFiltered && entry.is_filtered}<span class="dot dot-filtered" title="Filtered"></span>{/if}</span>
            {#if meta}<span class="char-meta">{meta}</span>{/if}
          </div>
          {#if zone}<div class="char-zone">{zone}</div>{/if}
        </div>
      {:else}
        <div class="empty">No characters</div>
      {/each}
    </div>

    <div class="detail-pane">
      {#if selected}
        <!-- sub-tab bar -->
        <div class="sub-tabs">
          <button class="sub-tab" class:active={detailTab === 'all'}
            on:click={() => detailTab = 'all'}>All</button>

          <button class="sub-tab" class:active={detailTab === 'inventory'}
            on:click={() => detailTab = 'inventory'}>
            Inventory{#if inventoryItems.length > 0}<span class="tab-count">({inventoryItems.length})</span>{/if}
          </button>

          {#if showSpellsTab}
            <button class="sub-tab" class:active={detailTab === 'spells'}
              on:click={openSpellsTab}>
              Spells{#if charClassLoading}
                <span class="tab-loading">…</span>
              {:else if spellsLoaded === selected && spellList.length > 0}
                <span class="tab-count" class:tab-missing={missingCount > 0}>
                  {#if missingCount > 0}({missingCount} missing){:else}(✓){/if}
                </span>
              {/if}
            </button>
          {/if}
        </div>

        <!-- All tab -->
        {#if detailTab === 'all'}
          <div class="detail" bind:this={detailEl}>
            <pre class="pre">{@html highlighted}</pre>
          </div>

        <!-- Inventory tab -->
        {:else if detailTab === 'inventory'}
          <div class="detail">
            {#if visibleInventory.length === 0}
              <div class="empty">{inventoryItems.length === 0 ? 'No inventory file found' : 'No items match'}</div>
            {:else}
              <table class="inv-table">
                <thead>
                  <tr>
                    <th class="col-slot">Slot</th>
                    <th class="col-item">Item</th>
                    <th class="col-count">#</th>
                  </tr>
                </thead>
                <tbody>
                  {#each visibleInventory as item}
                    <tr class:dim={item.count === 1}>
                      <td class="col-slot slot-label">{item.location}</td>
                      <td class="col-item">
                        <a class="wiki-link" href={wikiLink(item.name)} target="_blank" rel="noreferrer">{item.name}</a>
                      </td>
                      <td class="col-count">{#if item.count > 1}<span class="stack">{item.count}</span>{/if}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {/if}
          </div>

        <!-- Spells tab -->
        {:else if detailTab === 'spells'}
          <div class="detail spells-pane">
            {#if charClassLoading}
              <div class="empty">Identifying class…</div>

            {:else if !charClass}
              <div class="empty">
                Class unknown or missing spellbook file — log in and run <code>/who</code> to set the class and run <code>/outputfile spellbook</code>.
              </div>

            {:else if spellsLoading}
              <div class="empty">Loading spells…</div>

            {:else if spellsError}
              <div class="empty">{spellsError}</div>

            {:else if spellList.length === 0}
              <div class="empty">No spells found for {charClass}</div>

            {:else}
              <!-- summary bar -->
              <div class="spell-summary">
                <span class="spell-class">{charClass}</span>
                <span class="spell-count-info">
                  {#if spellbook === null}
                    · {spellList.length} spells · <span class="no-sb">No spellbook file — run <code>/outputfile spellbook</code></span>
                  {:else}
                    · {spellList.length - missingCount}/{spellList.length} known
                    {#if missingCount > 0}<span class="missing-badge">{missingCount} missing</span>{/if}
                  {/if}
                </span>
              </div>

              <!-- spell list grouped by level (highest first) -->
              <div class="spell-list">
                {#each levelGroups as group}
                  <div class="spell-level-header">Level {group.level}</div>
                  {#each group.spells as spell}
                    {@const missing = isMissing(spell.name)}
                    <div class="spell-row" class:spell-missing={missing}>
                      <div class="spell-name-col">
                        {#if spell.wiki_url}
                          <a class="spell-link" class:spell-link-missing={missing}
                             href={spell.wiki_url} target="_blank" rel="noreferrer">
                            {spell.name}
                          </a>
                        {:else}
                          <span class="spell-link" class:spell-link-missing={missing}>{spell.name}</span>
                        {/if}
                      </div>
                      <div class="spell-desc-col">
                        {#if spell.description}
                          <span class="spell-desc">{spell.description}</span>
                        {/if}
                      </div>
                      <div class="spell-stat-col">
                        {#if charClass === 'Bard'}
                          {#if spell.spell_type}<span class="spell-stat">{spell.spell_type}</span>{/if}
                        {:else}
                          {#if spell.mana > 0}<span class="spell-stat">{spell.mana}m</span>{/if}
                        {/if}
                      </div>
                    </div>
                  {/each}
                {/each}
              </div>
            {/if}
          </div>
        {/if}

      {:else}
        <div class="detail">
          <div class="empty">Select a character</div>
        </div>
      {/if}
    </div>
  </div>

  <!-- command footer -->
  <div class="cmd-footer">
    <div class="cmd-buttons">
      <button class="cmd-btn" on:click={() => copyCommand('/outputfile inventory')}>Copy Inventory Command</button>
      <button class="cmd-btn" on:click={() => copyCommand('/outputfile spellbook')}>Copy Spellbook Command</button>
    </div>
    {#if copyMsg}
      <span class="copy-msg">{copyMsg}</span>
    {/if}
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
    display:flex; flex-direction:column; gap:2px;
  }
  .char-item:hover  { background:rgba(255,255,255,0.04); color:var(--text-primary); font-weight:500;}
  .char-item.sel    { background:rgba(200,169,81,0.12);  color:var(--accent); font-weight:500;}
  .char-row { display:flex; align-items:center; gap:6px; }
  .char-name { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-weight:300; color:var(--text-primary); }
  .char-meta { margin-left:auto; color:var(--text-muted); font-size:11px; white-space:nowrap; }
  .char-zone { color:var(--text-muted); font-size:11px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; color:var(--accent);}
  .match-badge { color:var(--text-muted); font-size:11px; margin-left:4px; }

  /* status dots — bot (blue) and filtered (yellow) */
  .dot {
    display:inline-block; width:7px; height:7px;
    border-radius:50%; flex-shrink:0; vertical-align:middle;
  }
  .dot-bot      { background:#4a9eff; margin-left:5px; }
  .dot-filtered { background:#c8a951; margin-left:5px; }

  /* detail pane */
  .detail-pane { display:flex; flex-direction:column; flex:1; overflow:hidden; }

  /* sub-tabs */
  .sub-tabs {
    display:flex; gap:0; flex-shrink:0;
    border-bottom:1px solid var(--border);
    background:var(--bg-secondary);
    padding:0 12px;
  }
  .sub-tab {
    background:none; border:none; border-bottom:2px solid transparent;
    color:var(--text-muted); cursor:pointer; font-size:12px;
    padding:7px 12px 6px; transition:color 0.15s, border-color 0.15s;
    margin-bottom:-1px;
  }
  .sub-tab:hover { color:var(--text-primary); }
  .sub-tab.active { color:var(--accent); border-bottom-color:var(--accent); }
  .tab-count { color:var(--text-muted); font-size:11px; margin-left:4px; }
  .tab-missing { color:#e05c5c; }
  .tab-loading { color:var(--text-muted); font-size:11px; margin-left:3px; }

  .detail { flex:1; overflow:auto; padding:10px 14px; }
  .pre {
    font-size:13px; color:var(--text-secondary);
    line-height:1.6; margin:0; white-space:pre-wrap; word-break:break-word;
    user-select:text;
  }

  /* inventory table */
  .inv-table { width:100%; border-collapse:collapse; font-size:12px; }
  .inv-table thead th {
    text-align:left; color:var(--text-muted); font-weight:600;
    font-size:10px; letter-spacing:0.06em; text-transform:uppercase;
    padding:6px 10px 6px 0; border-bottom:1px solid var(--border);
    position:sticky; top:0; background:var(--bg-primary);
  }
  .inv-table tbody tr { border-bottom:1px solid rgba(37,40,54,0.6); }
  .inv-table tbody tr:hover { background:rgba(255,255,255,0.03); }
  .inv-table td { padding:5px 10px 5px 0; vertical-align:middle; }

  .col-slot  { width:110px; }
  .col-count { width:36px; text-align:right; padding-right:4px; }
  .slot-label { color:var(--text-muted); font-size:11px; }

  .wiki-link { color:var(--text-primary); text-decoration:none; transition:color 0.12s; }
  a.wiki-link:hover { color:var(--accent); text-decoration:underline; }

  .stack {
    background:rgba(200,169,81,0.15); color:var(--accent);
    border-radius:3px; font-size:11px; padding:1px 5px;
  }

  .empty { padding:40px 20px; color:var(--text-muted); font-size:12px; text-align:center; }
  .empty code {
    background:var(--bg-panel); border:1px solid var(--border);
    border-radius:3px; font-size:11px; padding:1px 5px;
    color:var(--text-secondary);
  }

  /* spell list */
  .spells-pane { display:flex; flex-direction:column; gap:0; }

  .spell-summary {
    display:flex; align-items:center; gap:6px; flex-shrink:0;
    padding:8px 0 6px; border-bottom:1px solid var(--border);
    font-size:12px; color:var(--text-muted); margin-bottom:4px;
  }
  .spell-class { color:var(--accent); font-weight:600; }
  .no-sb { color:var(--text-muted); font-style:italic; }
  .no-sb code {
    background:var(--bg-panel); border:1px solid var(--border);
    border-radius:3px; font-size:11px; padding:1px 5px;
    color:var(--text-secondary); font-style:normal;
  }
  .missing-badge {
    background:rgba(224,92,92,0.12); color:#e05c5c;
    border-radius:3px; font-size:11px; padding:1px 6px; margin-left:4px;
  }

  .spell-list { flex:1; overflow-y:auto; }

  .spell-level-header {
    font-size:10px; font-weight:700; letter-spacing:0.07em; text-transform:uppercase;
    color:var(--text-muted); padding:10px 0 3px;
    border-bottom:1px solid var(--border);
    margin-bottom:1px;
  }

  .spell-row {
    display:flex; align-items:center;
    padding:3px 0; gap:8px;
    border-bottom:1px solid rgba(37,40,54,0.4);
  }
  .spell-row:last-child { border-bottom:none; }

  .spell-name-col  { flex:0 0 180px; min-width:0; }
  .spell-desc-col  { flex:1; min-width:0; overflow:hidden; }
  .spell-stat-col  { flex:0 0 70px; text-align:right; }

  .spell-link {
    font-size:12px; color:var(--text-secondary);
    text-decoration:none; white-space:nowrap; overflow:hidden;
    text-overflow:ellipsis; display:block; transition:color 0.12s;
  }
  a.spell-link:hover { color:var(--accent); text-decoration:underline; }

  .spell-link-missing { color:#e05c5c !important; }
  a.spell-link-missing:hover { color:#f07070 !important; }

  .spell-desc { font-size:10px; color:var(--text-muted); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; display:block; }
  .spell-stat { font-size:10px; color:var(--text-muted); white-space:nowrap; }

  /* command footer */
  .cmd-footer {
    display:flex; align-items:center; gap:12px; flex-shrink:0;
    padding:8px 12px;
    border-top:1px solid var(--border);
    background:var(--bg-secondary);
  }
  .cmd-buttons { display:flex; gap:8px; }
  .cmd-btn {
    background:var(--bg-panel); border:1px solid var(--border); border-radius:4px;
    color:var(--text-secondary); cursor:pointer; font-size:11px;
    padding:4px 10px; transition:border-color 0.15s, color 0.15s;
    white-space:nowrap;
  }
  .cmd-btn:hover { border-color:var(--accent-dim); color:var(--accent); }
  .copy-msg { font-size:11px; color:var(--success); animation: fade-in 0.15s ease; }
  @keyframes fade-in { from { opacity:0 } to { opacity:1 } }

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
