<script>
  export let card
  export let liveHP = null   // live client HP for the active raid; null = use card value

  let openClass = {}
  function toggleClass(c) { openClass = { ...openClass, [c]: !openClass[c] } }

  $: hp = card.status === 'complete'
    ? 0
    : (liveHP != null && liveHP >= 0 ? liveHP : (card.target_hp ?? 100))

  // Raiders grouped into 4 role columns.
  const RAIDER_COLS = [
    { title: 'Priests', classes: ['CLR', 'SHM', 'DRU'] },
    { title: 'Casters', classes: ['MAG', 'WIZ', 'ENC', 'NEC'] },
    { title: 'Tanks',   classes: ['WAR', 'SHD', 'PAL'] },
    { title: 'DPS',     classes: ['ROG', 'MNK', 'RNG', 'BRD'] },
  ]
  $: raiderMap = Object.fromEntries(((card.raiders && card.raiders.groups) || []).map(g => [g.class, g]))
  // Reactive columns: counts/lists must be derived here (not via a helper
  // function called from the template) or Svelte won't re-render them when the
  // card data refreshes — counts would freeze at their mount-time values.
  // Also guards members:null (no one of that class) from the server.
  $: raiderCols = RAIDER_COLS.map(col => ({
    title: col.title,
    groups: col.classes.map(ab => {
      const g = raiderMap[ab]
      return g && g.members ? g : { class: ab, members: [] }
    }),
  }))

  // Split the CH chain into main and rampage (RR#) so we can space them apart.
  $: chMain = ((card.ch_chain) || []).filter(s => !(s.label || '').startsWith('RR'))
  $: chRamp = ((card.ch_chain) || []).filter(s => (s.label || '').startsWith('RR'))

  // Always list these debuffs; attribute the most recent caster + a check when seen.
  const DEBUFF_ORDER = ['Tash', 'Malo', 'Slow', 'Cripple']
  function debuffCaster(key) {
    if (!card.debuffs) return ''
    const k = key.toLowerCase()
    for (const d of card.debuffs) {
      if (d.name && d.name.toLowerCase().includes(k)) return d.value
    }
    return ''
  }
</script>

<div class="raidcard">
  <!-- Target Health (top) -->
  <div class="rc-target">
    <div class="rc-label">Target Health</div>
    <div class="rc-bar">
      <div class="rc-fill" style="width:{hp}%"></div>
      <span class="rc-bar-txt">{card.status === 'complete' ? 'Dead' : hp + '%'}</span>
    </div>
  </div>

  <div class="rc-grid">
    <!-- Assignments -->
    <div class="rc-col">
      <div class="rc-label">Assignments</div>
      {#if card.active_main_tank}<div class="rc-line"><span class="rc-k">Tank</span>{card.active_main_tank}</div>{/if}
      {#if card.active_ramp_tank}<div class="rc-line"><span class="rc-k">Ramp Tank</span>{card.active_ramp_tank}</div>{/if}
      {#if card.main_tank_list}<div class="rc-line"><span class="rc-k">MT List</span>{card.main_tank_list}</div>{/if}
      {#if card.rampage_tank_list}<div class="rc-line"><span class="rc-k">Ramp List</span>{card.rampage_tank_list}</div>{/if}
      {#if card.trash_tank_list}<div class="rc-line"><span class="rc-k">Trash</span>{card.trash_tank_list}</div>{/if}
      {#if card.bump_list}<div class="rc-line"><span class="rc-k">Bump</span>{card.bump_list}</div>{/if}
    </div>

    <!-- Debuffs -->
    <div class="rc-col">
      <div class="rc-label">Debuffs</div>
      {#each DEBUFF_ORDER as name}
        {@const caster = debuffCaster(name)}
        <div class="rc-line">
          <span class="rc-dname" class:done={caster}>{name}</span>
          {#if caster}<span class="rc-check">✓</span> <span class="rc-caster">{caster}</span>{/if}
        </div>
      {/each}
    </div>

    <!-- Clerics -->
    <div class="rc-col">
      <div class="rc-label">Clerics</div>
      {#if card.fluffer_clerics}<div class="rc-line"><span class="rc-k">Fluffer</span>{card.fluffer_clerics}</div>{/if}
      {#if card.ch_chain && card.ch_chain.length}
        <div class="rc-ch">
          {#each chMain as s}
            <div class="rc-chrow">
              <span class="rc-chnum">{s.label}</span>
              <span class="rc-chcleric">{s.cleric}</span>
              <span class="rc-charrow">→</span>
              <span class="rc-chtank">{s.tank}</span>
            </div>
          {/each}
          {#if chRamp.length}
            <div class="rc-chgap"></div>
            {#each chRamp as s}
              <div class="rc-chrow">
                <span class="rc-chnum ramp">{s.label}</span>
                <span class="rc-chcleric">{s.cleric}</span>
                <span class="rc-charrow">→</span>
                <span class="rc-chtank">{s.tank}</span>
              </div>
            {/each}
          {/if}
        </div>
      {/if}
    </div>
  </div>

  <!-- Raiders (4 role columns) -->
  <div class="rc-col">
    <div class="rc-label">Raiders <span class="rc-total">{card.raiders ? card.raiders.total : 0}</span></div>
    <div class="rc-raiders-cols">
      {#each raiderCols as col}
        <div class="rc-rcol">
          <div class="rc-rcol-title">{col.title}</div>
          {#each col.groups as g (g.class)}
            <div class="rc-class">
              <div class="rc-class-head" class:has={g.members.length}
                   on:click={() => g.members.length && toggleClass(g.class)}>
                {#if g.members.length}<span class="rc-chev2">{openClass[g.class] ? '▾' : '▸'}</span>{/if}
                <span class="rc-abbr">{g.class}</span>
                <span class="rc-cnt">{g.members.length}</span>
              </div>
              {#if openClass[g.class]}
                {#each g.members as m}
                  <div class="rc-member">{m.name}{#if m.level} ({m.level}){/if}{#if m.discord} · <span class="rc-disc">{m.discord}</span>{/if}</div>
                {/each}
              {/if}
            </div>
          {/each}
        </div>
      {/each}
    </div>
  </div>

  <!-- Loot + Discord channel -->
  <div class="rc-bottom">
    <div class="rc-col">
      <div class="rc-label">Loot</div>
      {#if card.loot && card.loot.length}
        {#each card.loot as l}
          <div class="rc-loot">
            {#if l.wiki_url}<a href={l.wiki_url} target="_blank" rel="noreferrer">{l.name}</a>{:else}{l.name}{/if}
            {#if l.price}<span class="rc-price">{l.price}</span>{/if}
          </div>
        {/each}
      {:else}
        <div class="rc-none">No loot recorded</div>
      {/if}
    </div>
    <div class="rc-col">
      <div class="rc-label">Discord Channel</div>
      {#if card.discord_url}
        <a class="rc-chanlink" href={card.discord_url} target="_blank" rel="noreferrer">Open raid channel →</a>
      {:else}
        <div class="rc-none">Not linked yet</div>
      {/if}
    </div>
  </div>
</div>

<style>
  .raidcard { padding: 8px 4px 6px 22px; display: flex; flex-direction: column; gap: 14px; }

  .rc-grid { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 16px; }
  .rc-bottom { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  .rc-raiders-cols { display: grid; grid-template-columns: repeat(4, 1fr); gap: 4px 14px; }
  .rc-rcol { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .rc-rcol-title {
    font-size: 10px; font-weight: 700; letter-spacing: 0.04em; text-transform: uppercase;
    color: var(--text-muted); margin-bottom: 2px;
  }
  @media (max-width: 720px) {
    .rc-grid, .rc-bottom { grid-template-columns: 1fr; }
    .rc-raiders-cols { grid-template-columns: 1fr 1fr; }
  }

  .rc-col { display: flex; flex-direction: column; gap: 4px; min-width: 0; }

  /* All section headers gold */
  .rc-label {
    font-size: 11px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase;
    color: #e3a008; margin-bottom: 3px;
  }

  .rc-line { font-size: 13px; color: var(--text-primary); word-break: break-word; line-height: 1.4; }
  /* Higher-contrast labels for assignment fields, Fluffer, debuff names */
  .rc-k { display: inline-block; min-width: 66px; color: #d7dee6; font-size: 13px; font-weight: 600; margin-right: 6px; }
  .rc-dname { color: #d7dee6; font-weight: 600; }
  .rc-dname.done { color: var(--text-primary); }
  .rc-check { color: var(--success); font-weight: 800; }
  .rc-caster { color: var(--text-secondary); }

  .rc-ch { display: flex; flex-direction: column; gap: 2px; }
  .rc-chrow { display: flex; align-items: baseline; gap: 6px; font-size: 12px; }
  .rc-chnum { min-width: 34px; text-align: center; color: var(--bg); background: #0855e3; border-radius: 3px; font-weight: 700; font-size: 11px; }
  .rc-chnum.ramp { background: #475569; }
  .rc-chgap { height: 8px; }
  .rc-chcleric { color: var(--text-primary); }
  .rc-charrow { color: var(--text-muted); }
  .rc-chtank { color: var(--text-secondary); }

  .rc-total { color: var(--text-primary); font-weight: 400; }
  .rc-class { display: flex; flex-direction: column; }
  .rc-class-head { display: flex; align-items: center; gap: 6px; font-size: 12px; padding: 1px 0; color: var(--text-muted); }
  .rc-class-head.has { cursor: pointer; color: var(--text-primary); }
  .rc-abbr { font-weight: 500; min-width: 20px; }
  .rc-cnt { color: var(--text-muted); }
  .rc-chev2 { font-size: 16px; }
  .rc-member { font-size: 12px; color: var(--text-secondary); margin-left: 20px; }
  .rc-disc { color: var(--text-muted); }

  .rc-target { display: flex; flex-direction: column; gap: 4px; }
  .rc-bar {
    position: relative; height: 20px; border-radius: 4px; overflow: hidden;
    background: #3a1414; border: 1px solid #5c2020;
  }
  .rc-fill { position: absolute; inset: 0 auto 0 0; background: linear-gradient(90deg, #b91c1c, #ef4444); transition: width 0.4s ease; }
  .rc-bar-txt {
    position: absolute; inset: 0; display: flex; align-items: center; justify-content: center;
    font-size: 12px; font-weight: 700; color: #fff; text-shadow: 0 1px 2px rgba(0,0,0,0.6);
  }

  .rc-loot { font-size: 13px; color: var(--text-primary); }
  .rc-loot a { color: var(--accent); text-decoration: none; }
  .rc-loot a:hover { text-decoration: underline; }
  .rc-price { color: #e3a008; font-size: 12px; margin-left: 6px; }
  .rc-chanlink { color: var(--accent); font-size: 13px; text-decoration: none; }
  .rc-chanlink:hover { text-decoration: underline; }
  .rc-none { font-size: 13px; color: var(--text-muted); font-style: italic; }
</style>
