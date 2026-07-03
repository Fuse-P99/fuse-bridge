<script>
  import { onMount, onDestroy } from 'svelte'
  import { GetTimers, IsAdminMode, GetMobHPs } from '../../wailsjs/go/main/App'
  import { linked } from '../lib/linkState.js'
  import { activeTab } from '../lib/nav.js'
  import RaidCardView from '../lib/RaidCardView.svelte'

  let data = null
  let loading = true
  let admin = false
  let mobHPs = {}         // lower mob name → live HP percent
  let timer
  let hpTimer
  let pollMs = 60000      // active raids poll every 5s, otherwise 60s

  let openRaid = {}       // mob name → expanded (default true)
  let completedOpen = {}  // completed raid index → open

  function toggleRaid(name) { openRaid = { ...openRaid, [name]: !(openRaid[name] ?? true) } }
  function toggleCompleted(i) { completedOpen = { ...completedOpen, [i]: !completedOpen[i] } }

  // Live HP lookups take the map as an arg so Svelte tracks it as a dependency.
  function hpFor(hps, name) { return name ? hps[name.toLowerCase()] : undefined }
  function raidLiveHP(hps, m) {
    if (m.sample) return null
    let h = hpFor(hps, m.raid && m.raid.target)
    if (h === undefined) h = hpFor(hps, m.name)
    return h === undefined ? -1 : h
  }

  // Poll faster while a real raid is active so assignments/HP stay fresh.
  function reschedule() {
    const active = (data && data.mobs && data.mobs.some(m => m.is_raid))
    const want = active ? 5000 : 60000
    if (want !== pollMs) {
      pollMs = want
      clearInterval(timer)
      timer = setInterval(load, pollMs)
    }
  }

  async function load() {
    try { data = await GetTimers() } catch { data = null }
    loading = false
    reschedule()
  }
  async function pollHP() {
    try { mobHPs = await GetMobHPs() || {} } catch { mobHPs = {} }
  }
  onMount(async () => {
    admin = await IsAdminMode()
    await load()
    timer = setInterval(load, pollMs)
    hpTimer = setInterval(pollHP, 2000)
  })
  onDestroy(() => { clearInterval(timer); clearInterval(hpTimer) })

  let prevLinked
  $: if ($linked !== prevLinked) { prevLinked = $linked; if ($linked) load() }

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

  // Fully-populated fictional card so admins can tweak the layout without a live raid.
  const sampleGroups = [
    { class: 'CLR', members: [ { name: 'Healbot', level: 60, discord: 'Bob' }, { name: 'Mendy', level: 59, discord: 'Alice' } ] },
    { class: 'WAR', members: [ { name: 'Tanky', level: 60, discord: 'Carl' }, { name: 'Ironhide', level: 60, discord: 'Dave' } ] },
    { class: 'SHD', members: [ { name: 'Grimtank', level: 58, discord: 'Eve' } ] },
    { class: 'PAL', members: [ { name: 'Lightbringer', level: 60, discord: 'Frank' } ] },
    { class: 'MAG', members: [ { name: 'Pewpew', level: 60, discord: 'Grace' } ] },
    { class: 'BRD', members: [ { name: 'Songbird', level: 60, discord: 'Heidi' } ] },
    { class: 'DRU', members: [] },
    { class: 'ENC', members: [ { name: 'Mezzer', level: 59, discord: 'Ivan' } ] },
    { class: 'MNK', members: [ { name: 'Puncher', level: 60, discord: 'Judy' } ] },
    { class: 'NEC', members: [] },
    { class: 'RNG', members: [] },
    { class: 'ROG', members: [ { name: 'Backstab', level: 60, discord: 'Ken' } ] },
    { class: 'SHM', members: [ { name: 'Slower', level: 60, discord: 'Laura' } ] },
    { class: 'WIZ', members: [ { name: 'Nukey', level: 60, discord: 'Mike' } ] },
  ]
  const sampleCard = {
    target: 'Lord Nagafen', status: 'active', target_hp: 62,
    active_main_tank: 'Tanky', active_ramp_tank: 'Bruiser',
    main_tank_list: 'Tanky, Steelskin, Ironhide', rampage_tank_list: 'Bruiser, Basher',
    trash_tank_list: 'Warddog, Meatwall', bump_list: 'Nudge, Shove',
    fluffer_clerics: 'Healbot, Mendy, Pious',
    debuffs: [ { name: 'Malo', value: 'Debuffa' }, { name: 'Slow', value: 'Slowpoke' }, { name: 'Snare', value: 'Snarey' } ],
    ch_chain: [
      { label: '111', cleric: 'Cleric1', tank: 'Tanky' },
      { label: '222', cleric: 'Cleric2', tank: 'Tanky' },
      { label: '333', cleric: 'Cleric3', tank: 'Tanky' },
      { label: '444', cleric: 'Cleric4', tank: 'Tanky' },
      { label: '555', cleric: 'Cleric5', tank: 'Tanky' },
      { label: '666', cleric: 'Cleric6', tank: 'Tanky' },
      { label: 'RR1', cleric: 'RampCleric1', tank: 'Bruiser' },
      { label: 'RR2', cleric: 'RampCleric2', tank: 'Bruiser' },
    ],
    loot: [
      { name: 'Flowing Black Silk Sash', wiki_url: 'https://wiki.project1999.com/Flowing_Black_Silk_Sash', price: '250 DKP · Tanky' },
      { name: 'Cloak of Flames', wiki_url: 'https://wiki.project1999.com/Cloak_of_Flames', price: '175 DKP · Pewpew' },
    ],
    raiders: { total: 14, groups: sampleGroups },
    discord_url: 'https://discord.com/channels/0/0',
  }

  function computePopped(d, isAdmin) {
    let list = (d && d.mobs) ? d.mobs.filter(m => m.status === 'popped') : []
    //if (isAdmin) {
    //  list = [{ name: sampleCard.target + ' (sample)', status: 'popped', is_raid: true, sample: true, raid: sampleCard, trackers: [] }, ...list]
    //}
    // Current raid always leads the popped list (server appends synthetic
    // off-board raid entries at the end, and board order is arbitrary).
    return [...list.filter(m => m.is_raid), ...list.filter(m => !m.is_raid)]
  }
  $: popped = computePopped(data, admin)
  $: inWindow = (data && data.mobs) ? data.mobs.filter(m => m.status === 'in_window') : []
  $: upcoming = (data && data.mobs) ? data.mobs.filter(m => m.status === 'upcoming')  : []
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
      {#if data.porter}
        <div class="porter"><span class="ptag">PORTER</span> {data.porter}</div>
      {/if}
      {#if data.logistics}
        <div class="porter"><span class="ptag">LOGISTICS</span> {data.logistics}</div>
      {/if}
      {#if data.idol}
        <div class="porter"><span class="ptag">IDOL</span> {data.idol}</div>
      {/if}

      <!-- Popped (current raid = gold swords + expandable card; admin sample prepended) -->
      {#if popped.length}
        <div class="group-title popped">Popped <span class="count">({popped.length})</span></div>
        {#each popped as m (m.name)}
          <div class="mob">
            <div class="mob-head" class:clickable={m.is_raid} on:click={() => { if (m.is_raid) toggleRaid(m.name) }}>
              {#if m.is_raid}
                <span class="swords" title="Current raid">⚔</span>
              {:else}
                <span class="dot {dotClass(m)}"></span>
              {/if}
              <span class="mob-name" class:raid={m.is_raid}>{m.name}</span>
              {#if m.is_raid}<span class="chev chev-auto">{(openRaid[m.name] ?? true) ? '▾' : '▸'}</span>{/if}
            </div>
            {#if m.is_raid && m.raid}
              <div class:collapsed={!(openRaid[m.name] ?? true)}>
                <RaidCardView card={m.raid} liveHP={raidLiveHP(mobHPs, m)} />
              </div>
            {:else if !m.is_raid && m.detail}
              <div class="mob-detail">{m.detail}</div>
            {/if}
            {#if !m.is_raid && hpFor(mobHPs, m.name) !== undefined}
              <div class="mini-bar">
                <div class="mini-fill" style="width:{hpFor(mobHPs, m.name)}%"></div>
                <span class="mini-txt">{hpFor(mobHPs, m.name)}%</span>
              </div>
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
  .collapsed { display:none; }

  .dot { width:8px; height:8px; border-radius:50%; flex-shrink:0; }
  .dot.popped    { background:#ff5555; }
  .dot.in_window { background:#3fb950; }
  .dot.untracked { background:#ff5555; }
  .dot.upcoming  { background:var(--text-muted); }
  .dot.completedDot { background:var(--success); }
  .swords { color:#e3a008; font-size:14px; line-height:1; flex-shrink:0; }

  .mob-detail   { color:var(--text-secondary); font-size:12px; margin:1px 0 0 15px; }
  .mob-trackers { color:var(--text-muted); font-size:11px; font-style:italic; margin:1px 0 0 15px; }

  /* Inline HP bar for a non-current popped mob */
  .mini-bar {
    position:relative; height:12px; border-radius:3px; overflow:hidden; margin:3px 0 0 15px;
    background:#3a1414; border:1px solid #5c2020;
  }
  .mini-fill { position:absolute; inset:0 auto 0 0; background:linear-gradient(90deg,#b91c1c,#ef4444); transition:width 0.4s ease; }
  .mini-txt { position:absolute; inset:0; display:flex; align-items:center; justify-content:center; font-size:9px; font-weight:700; color:#fff; }

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
