<script>
  export let card
</script>

<div class="raidcard">
  {#if card.status === 'complete'}
    <div class="rc-killed">Killed{card.killed_ago ? ` ${card.killed_ago}` : ''}</div>
  {/if}

  {#if card.active_main_tank || card.active_ramp_tank}
    <div class="rc-section">
      <div class="rc-label">Active Tanks</div>
      {#if card.active_main_tank}<div class="rc-line"><span class="rc-k">Current</span>{card.active_main_tank}</div>{/if}
      {#if card.active_ramp_tank}<div class="rc-line"><span class="rc-k">Rampage</span>{card.active_ramp_tank}</div>{/if}
    </div>
  {/if}

  {#if card.main_tank_list || card.rampage_tank_list || card.trash_tank_list || card.bump_list}
    <div class="rc-section">
      <div class="rc-label">Tank Assignments</div>
      {#if card.main_tank_list}<div class="rc-line"><span class="rc-k">Main</span>{card.main_tank_list}</div>{/if}
      {#if card.rampage_tank_list}<div class="rc-line"><span class="rc-k">Ramp</span>{card.rampage_tank_list}</div>{/if}
      {#if card.trash_tank_list}<div class="rc-line"><span class="rc-k">Trash</span>{card.trash_tank_list}</div>{/if}
      {#if card.bump_list}<div class="rc-line"><span class="rc-k">Bump</span>{card.bump_list}</div>{/if}
    </div>
  {/if}

  {#if card.debuffs && card.debuffs.length}
    <div class="rc-section">
      <div class="rc-label">Debuffs</div>
      {#each card.debuffs as d}<div class="rc-line"><span class="rc-k">{d.name}</span>{d.value}</div>{/each}
    </div>
  {/if}

  {#if card.fluffer_clerics}
    <div class="rc-section">
      <div class="rc-label">Fluffer Clerics</div>
      <div class="rc-line">{card.fluffer_clerics}</div>
    </div>
  {/if}

  {#if card.ch_chain && card.ch_chain.length}
    <div class="rc-section">
      <div class="rc-label">CH Chain</div>
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
    </div>
  {/if}

  {#if card.loot && card.loot.length}
    <div class="rc-section">
      <div class="rc-label">Loot</div>
      {#each card.loot as l}<div class="rc-line rc-loot">{l}</div>{/each}
    </div>
  {/if}
</div>

<style>
  .raidcard {
    padding: 8px 4px 4px 22px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .rc-killed { font-size: 12px; color: var(--success); font-weight: 600; }
  .rc-section { display: flex; flex-direction: column; gap: 3px; }
  .rc-label {
    font-size: 10px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase;
    color: #e3a008;
  }
  .rc-line { font-size: 12px; color: var(--text-secondary); }
  .rc-k {
    display: inline-block; min-width: 58px; color: var(--text-muted);
    font-size: 11px; margin-right: 6px;
  }
  .rc-loot { color: var(--text-primary); }

  .rc-ch { display: flex; flex-direction: column; gap: 2px; font-family: var(--font-mono); }
  .rc-chrow { display: flex; align-items: baseline; gap: 6px; font-size: 11px; }
  .rc-chnum {
    min-width: 26px; text-align: center; color: var(--bg);
    background: #e3a008; border-radius: 3px; font-weight: 700; font-size: 10px; padding: 0 2px;
  }
  .rc-chcleric { color: var(--text-primary); }
  .rc-charrow { color: var(--text-muted); }
  .rc-chtank { color: var(--text-secondary); }
</style>
