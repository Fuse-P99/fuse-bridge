<script>
  import { onMount, onDestroy } from 'svelte'
  import { GetTimers, IsAdminMode } from '../../wailsjs/go/main/App'
  import { linked } from '../lib/linkState.js'
  import { activeTab } from '../lib/nav.js'
  import RaidCardView from '../lib/RaidCardView.svelte'

  let data = null
  let loading = true
  let admin = false
  let timer

  // Expansion state.
  let raidOpen = true          // current-raid card auto-expanded
  let completedOpen = {}       // completed raid index → open

  async function load() {
    try { data = await GetTimers() } catch { data = null }
    loading = false
  }
  onMount(async () => {
    admin = await IsAdminMode()
    await load()
    timer = setInterval(load, 60000)
  })
  onDestroy(() => clearInterval(timer))

  let prevLinked
  $: if ($linked !== prevLinked) { prevLinked = $linked; if ($linked) load() }

  const LABEL = { popped: 'Popped', in_window: 'In Window', upcoming: 'Upcoming' }

  function dotClass(m) {
    if (m.status === 'in_window' && !(m.trackers && m.trackers.length)) return 'untracked'
    return m.status
  }
  function trackerLabel(t) {
    let s = t.name || 'Unknown'
    if (t.role) s += ` (${t.role})`
    if (t.ago)  s += ` · ${t.ago}`
    return s
  }
  function toggleCompleted(i) {
    completedOpen = { ...completedOpen, [i]: !completedOpen[i] }
  }

  $: popped   = (data && data.mobs) ? data.mobs.filter(m => m.status === 'popped')    : []
  $: inWindow = (data && data.mobs) ? data.mobs.filter(m => m.status === 'in_window') : []
  $: upcoming = (data && data.mobs) ? data.mobs.filter(m => m.status === 'upcoming')  : []

  // Fully-populated fictional card for admins to tweak the layout without a live raid.
  const sampleCard = {
    target: 'Lord Nagafen', status: 'active',
    active_main_tank: 'Tanky', active_ramp_tank: 'Bruiser',
    main_tank_list: 'Tanky, Steelskin, Ironhide', rampage_tank_list: 'Bruiser, Basher',
    trash_tank_list: 'Warddog, Meatwall', bump_list: 'Nudge, Shove',
    fluffer_clerics: 'Healbot, Mendy, Pious',
    debuffs: [ { name: 'Malo', value: 'Debuffa' }, { name: 'Slow', value: 'Slowpoke' }, { name: 'Snare', value: 'Snarey' } ],
    ch_chain: [
      { label: '1', cleric: 'Cleric1', tank: 'Tanky' },
      { label: '2', cleric: 'Cleric2', tank: 'Tanky' },
      { label: '3', cleric: 'Cleric3', tank: 'Tanky' },
      { label: 'RR1', cleric: 'RampCleric', tank: 'Bruiser' },
    ],
    loot: ['Flowing Black Silk Sash', 'Cloak of Flames', "Nature Walker's Scimitar"],
  }
</script>

<div class="timers">
  {#if !$linked}
    <div class="empty">
      <div class="big">Link your Discord account</div>
      <div class="hint">You must link your Discord account to validate your Fuse membership and view tracking.</div>
      <button class="link-btn" on:click={() => activeTab.set('general')}>Link your account on the Status tab →</button>
    </div>
  {:else if loading}
    <div class="empty">Loading raids…</div>
  {:else if !data || !data.verified}
    <div class="empty">
      <div class="big">Raids unavailable</div>
      <div class="hint">You could not be verified as a Fuse member.</div>
    </div>
  {:else}
    <div class="board">
      <!-- Batphone banners -->
      {#if data.batphones && data.batphones.length}
        {#each data.batphones as b}
          <div class="banner"><span class="banner-tag">BATPHONE</span> {b.text}</div>
        {/each}
      {/if}

      {#if data.porter}
        <div class="porter"><span class="ptag">PORTER</span> {data.porter}</div>
      {/if}

      {#if admin}
        <div class="group-title sample">Sample (admin preview)</div>
        <div class="mob">
          <div class="mob-head">
            <span class="swords">⚔</span>
            <span class="mob-name">{sampleCard.target}</span>
            <span class="chev">▾</span>
          </div>
          <RaidCardView card={sampleCard} />
        </div>
      {/if}

      <!-- Popped -->
      {#if popped.length}
        <div class="group-title popped">Popped <span class="count">({popped.length})</span></div>
        {#each popped as m}
          <div class="mob">
            <div class="mob-head" class:clickable={m.is_raid} on:click={() => { if (m.is_raid) raidOpen = !raidOpen }}>
              {#if m.is_raid}
                <span class="swords" title="Current raid">⚔</span>
              {:else}
                <span class="dot {dotClass(m)}"></span>
              {/if}
              <span class="mob-name" class:raid={m.is_raid}>{m.name}</span>
              {#if m.is_raid}<span class="chev chev-auto">{raidOpen ? '▾' : '▸'}</span>{/if}
            </div>
            {#if m.is_raid && m.raid && raidOpen}
              <RaidCardView card={m.raid} />
            {:else if m.detail}
              <div class="mob-detail">{m.detail}</div>
            {/if}
            {#if m.trackers && m.trackers.length}
              <div class="mob-trackers">
                {#each m.trackers as t, i}{i > 0 ? ', ' : ''}{trackerLabel(t)}{/each}
              </div>
            {/if}
          </div>
        {/each}
      {/if}

      <!-- Completed raids (last 30 min) -->
      {#if data.completed_raids && data.completed_raids.length}
        <div class="group-title completed">Completed Raids <span class="count">({data.completed_raids.length})</span></div>
        {#each data.completed_raids as r, i}
          <div class="mob">
            <div class="mob-head clickable" on:click={() => toggleCompleted(i)}>
              <span class="dot completedDot"></span>
              <span class="mob-name">{r.target}</span>
              {#if r.killed_ago}<span class="remaining">killed {r.killed_ago}</span>{/if}
              <span class="chev">{completedOpen[i] ? '▾' : '▸'}</span>
            </div>
            {#if completedOpen[i]}
              <RaidCardView card={r} />
            {/if}
          </div>
        {/each}
      {/if}

      <!-- In Window -->
      {#if inWindow.length}
        <div class="group-title in_window">In Window <span class="count">({inWindow.length})</span></div>
        {#each inWindow as m}
          <div class="mob">
            <div class="mob-head">
              <span class="dot {dotClass(m)}"></span>
              <span class="mob-name">{m.name}</span>
              {#if m.remaining}<span class="remaining">{m.remaining} remaining</span>{/if}
            </div>
            {#if m.trackers && m.trackers.length}
              <div class="mob-trackers">
                {#each m.trackers as t, i}{i > 0 ? ', ' : ''}{trackerLabel(t)}{/each}
              </div>
            {/if}
          </div>
        {/each}
      {/if}

      <!-- Upcoming -->
      {#if upcoming.length}
        <div class="group-title upcoming">Upcoming <span class="count">({upcoming.length})</span></div>
        {#each upcoming as m}
          <div class="mob">
            <div class="mob-head">
              <span class="dot {dotClass(m)}"></span>
              <span class="mob-name">{m.name}</span>
            </div>
            {#if m.detail}<div class="mob-detail">{m.detail}</div>{/if}
          </div>
        {/each}
      {/if}

      {#if !popped.length && !inWindow.length && !upcoming.length && !(data.completed_raids && data.completed_raids.length)}
        <div class="empty">No timers reported</div>
      {/if}
    </div>

    <div class="footer">
      {#if data.summary}<span>{data.summary}</span>{/if}
      {#if data.updated}<span class="upd">{data.updated}</span>{/if}
    </div>
  {/if}
</div>

<style>
  .timers { display:flex; flex-direction:column; height:100%; overflow:hidden; }
  .board { flex:1; overflow-y:auto; padding:10px 14px; }

  .banner {
    background:rgba(227,160,8,0.16); border:1px solid #e3a008; border-radius:6px;
    padding:8px 10px; margin-bottom:8px; font-size:13px; color:var(--text-primary);
  }
  .banner-tag {
    color:#e3a008; font-weight:800; font-size:10px; letter-spacing:0.08em; margin-right:8px;
  }

  .porter {
    background:var(--bg-panel); border:1px solid var(--border); border-radius:6px;
    padding:8px 10px; margin-bottom:12px; font-size:12px; color:var(--text-secondary);
  }
  .ptag { color:var(--accent); font-weight:700; font-size:10px; letter-spacing:0.06em; margin-right:6px; }

  .group-title {
    font-size:11px; font-weight:700; text-transform:uppercase; letter-spacing:0.06em;
    margin:14px 0 6px; color:var(--text-muted);
  }
  .group-title.popped    { color:#ff7a7a; }
  .group-title.in_window { color:#3fb950; }
  .group-title.upcoming  { color:var(--text-muted); }
  .group-title.completed { color:var(--success); }
  .group-title.sample    { color:#e3a008; }
  .group-title .count { font-weight:400; }

  .mob { padding:5px 0 6px; border-bottom:1px solid var(--border); }
  .mob:last-child { border-bottom:none; }
  .mob-head { display:flex; align-items:center; gap:7px; }
  .mob-head.clickable { cursor:pointer; }
  .mob-name { color:var(--text-primary); font-size:13px; font-weight:600; }
  .mob-name.raid { color:#e3a008; }
  .remaining { margin-left:auto; color:var(--text-secondary); font-size:12px; white-space:nowrap; }
  .chev { color:var(--text-muted); font-size:11px; margin-left:8px; }
  .chev-auto { margin-left:auto; }

  .dot { width:8px; height:8px; border-radius:50%; flex-shrink:0; }
  .dot.popped    { background:#ff5555; }
  .dot.in_window { background:#3fb950; }
  .dot.untracked { background:#ff5555; }
  .dot.upcoming  { background:var(--text-muted); }
  .dot.completedDot { background:var(--success); }
  .swords { color:#e3a008; font-size:14px; line-height:1; flex-shrink:0; }

  .mob-detail   { color:var(--text-secondary); font-size:12px; margin:1px 0 0 15px; }
  .mob-trackers { color:var(--text-muted); font-size:11px; font-style:italic; margin:1px 0 0 15px; }

  .footer {
    flex-shrink:0; display:flex; justify-content:space-between; gap:10px;
    padding:6px 14px; border-top:1px solid var(--border); background:var(--bg-secondary);
    color:var(--text-muted); font-size:11px;
  }
  .upd { white-space:nowrap; }

  .empty {
    display:flex; flex-direction:column; align-items:center; justify-content:center;
    height:100%; gap:6px; color:var(--text-muted); font-size:13px; text-align:center;
  }
  .empty .big { color:var(--text-secondary); font-size:15px; font-weight:600; }
  .empty .hint { font-size:12px; max-width:340px; line-height:1.5; }
  .link-btn {
    margin-top:8px; background:var(--bg-panel); border:1px solid var(--accent);
    color:var(--accent); border-radius:4px; cursor:pointer; font-size:12px; padding:6px 14px;
    transition:background 0.15s;
  }
  .link-btn:hover { background:var(--bg-input); }
</style>
