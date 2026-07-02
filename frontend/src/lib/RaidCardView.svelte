<script>
  export let card
  export let liveHP = null   // live client HP for the active raid; null = use card value

  let openClass = {}
  function toggleClass(c) { openClass = { ...openClass, [c]: !openClass[c] } }

  $: hp = card.status === 'complete'
    ? 0
    : (liveHP != null && liveHP >= 0 ? liveHP : (card.target_hp ?? 100))
</script>

<div class="raidcard">
  <div class="rc-grid">
    <!-- Assignments -->
    <div class="rc-col">
      <div class="rc-label yellow">Assignments</div>
      {#if card.active_main_tank}<div class="rc-line"><span class="rc-k">Tank</span>{card.active_main_tank}</div>{/if}
      {#if card.active_ramp_tank}<div class="rc-line"><span class="rc-k">Ramp Tank</span>{card.active_ramp_tank}</div>{/if}
      {#if card.main_tank_list}<div class="rc-line"><span class="rc-k">MT List</span>{card.main_tank_list}</div>{/if}
      {#if card.rampage_tank_list}<div class="rc-line"><span class="rc-k">Ramp List</span>{card.rampage_tank_list}</div>{/if}
      {#if card.trash_tank_list}<div class="rc-line"><span class="rc-k">Trash</span>{card.trash_tank_list}</div>{/if}
      {#if card.bump_list}<div class="rc-line"><span class="rc-k">Bump</span>{card.bump_list}</div>{/if}
      {#if card.debuffs && card.debuffs.length}
        <div class="rc-sublabel">Debuffs</div>
        {#each card.debuffs as d}<div class="rc-line"><span class="rc-k">{d.name}</span>{d.value}</div>{/each}
      {/if}
    </div>

    <!-- CH Chain -->
    <div class="rc-col">
      <div class="rc-label blue">CH Chain</div>
      {#if card.fluffer_clerics}<div class="rc-line"><span class="rc-k">Fluffer</span>{card.fluffer_clerics}</div>{/if}
      {#if card.ch_chain && card.ch_chain.length}
        <div class="rc-ch">
          {#each card.ch_chain as s}
            <div class="rc-chrow">
              <span class="rc-chnum">{s.label}</span>
              <span class="rc-chcleric">{s.cleric}</span>
              <span class="rc-charrow">→</span>
              <span class="rc-chtank">{s.tank}</span>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Raiders -->
    <div class="rc-col">
      <div class="rc-label orange">Raiders <span class="rc-total">{card.raiders ? card.raiders.total : 0}</span></div>
      {#if card.raiders && card.raiders.groups}
        {#each card.raiders.groups as g}
          <div class="rc-class">
            <div class="rc-class-head" class:has={g.members && g.members.length}
                 on:click={() => g.members && g.members.length && toggleClass(g.class)}>
              <span class="rc-abbr">{g.class}</span>
              <span class="rc-cnt">{g.members ? g.members.length : 0}</span>
              {#if g.members && g.members.length}<span class="rc-chev2">{openClass[g.class] ? '▾' : '▸'}</span>{/if}
            </div>
            {#if openClass[g.class] && g.members}
              {#each g.members as m}
                <div class="rc-member">{m.name}{#if m.level} ({m.level}){/if}{#if m.discord} · <span class="rc-disc">{m.discord}</span>{/if}</div>
              {/each}
            {/if}
          </div>
        {/each}
      {/if}
    </div>
  </div>

  <!-- Target -->
  <div class="rc-target">
    <div class="rc-label red">Target</div>
    <div class="rc-target-name">{card.target}</div>
    <div class="rc-bar">
      <div class="rc-fill" style="width:{hp}%"></div>
      <span class="rc-bar-txt">{card.status === 'complete' ? 'Dead' : hp + '%'}</span>
    </div>
  </div>

  <!-- Loot + Discord channel -->
  <div class="rc-bottom">
    <div class="rc-col">
      <div class="rc-label gold">Loot</div>
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
      <div class="rc-label grey">Discord Channel</div>
      {#if card.discord_url}
        <a class="rc-chanlink" href={card.discord_url} target="_blank" rel="noreferrer">Open raid channel →</a>
      {:else}
        <div class="rc-none">Not linked yet</div>
      {/if}
    </div>
  </div>
</div>

<style>
  .raidcard { padding: 8px 4px 6px 22px; display: flex; flex-direction: column; gap: 12px; }

  .rc-grid { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 14px; }
  .rc-bottom { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
  @media (max-width: 720px) {
    .rc-grid, .rc-bottom { grid-template-columns: 1fr; }
  }

  .rc-col { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
  .rc-label {
    font-size: 10px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; margin-bottom: 2px;
  }
  .rc-label.yellow { color: #d9c24a; }
  .rc-label.blue   { color: #58a6ff; }
  .rc-label.orange { color: #e8933a; }
  .rc-label.red    { color: #ff5555; }
  .rc-label.gold   { color: #e3a008; }
  .rc-label.grey   { color: var(--text-muted); }
  .rc-sublabel { font-size: 10px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; margin-top: 6px; }

  .rc-line { font-size: 12px; color: var(--text-secondary); word-break: break-word; }
  .rc-k { display: inline-block; min-width: 62px; color: var(--text-muted); font-size: 11px; margin-right: 6px; }

  .rc-ch { display: flex; flex-direction: column; gap: 2px; font-family: var(--font-mono); }
  .rc-chrow { display: flex; align-items: baseline; gap: 6px; font-size: 11px; }
  .rc-chnum { min-width: 30px; text-align: center; color: var(--bg); background: #58a6ff; border-radius: 3px; font-weight: 700; font-size: 10px; }
  .rc-chcleric { color: var(--text-primary); }
  .rc-charrow { color: var(--text-muted); }
  .rc-chtank { color: var(--text-secondary); }

  .rc-total { color: var(--text-muted); font-weight: 400; }
  .rc-class { display: flex; flex-direction: column; }
  .rc-class-head { display: flex; align-items: center; gap: 6px; font-size: 12px; padding: 1px 0; color: var(--text-muted); }
  .rc-class-head.has { cursor: pointer; color: var(--text-secondary); }
  .rc-abbr { font-family: var(--font-mono); font-weight: 700; min-width: 30px; }
  .rc-cnt { color: var(--text-muted); }
  .rc-chev2 { margin-left: auto; font-size: 10px; }
  .rc-member { font-size: 11px; color: var(--text-secondary); margin-left: 18px; }
  .rc-disc { color: var(--text-muted); }

  .rc-target { display: flex; flex-direction: column; gap: 4px; }
  .rc-target-name { font-size: 14px; font-weight: 700; color: #ff7a7a; }
  .rc-bar {
    position: relative; height: 18px; border-radius: 4px; overflow: hidden;
    background: #3a1414; border: 1px solid #5c2020;
  }
  .rc-fill { position: absolute; inset: 0 auto 0 0; background: linear-gradient(90deg, #b91c1c, #ef4444); transition: width 0.4s ease; }
  .rc-bar-txt {
    position: absolute; inset: 0; display: flex; align-items: center; justify-content: center;
    font-size: 11px; font-weight: 700; color: #fff; text-shadow: 0 1px 2px rgba(0,0,0,0.6);
  }

  .rc-loot { font-size: 12px; color: var(--text-primary); }
  .rc-loot a { color: var(--accent); text-decoration: none; }
  .rc-loot a:hover { text-decoration: underline; }
  .rc-price { color: #e3a008; font-size: 11px; margin-left: 6px; }
  .rc-chanlink { color: var(--accent); font-size: 12px; text-decoration: none; }
  .rc-chanlink:hover { text-decoration: underline; }
  .rc-none { font-size: 12px; color: var(--text-muted); font-style: italic; }
</style>
