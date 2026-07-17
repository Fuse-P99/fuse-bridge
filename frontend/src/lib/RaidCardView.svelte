<script>
  export let card;
  export let liveHP = null; // live client HP for the active raid; null = use card value

  let openClass = {};
  function toggleClass(c) {
    openClass = { ...openClass, [c]: !openClass[c] };
  }

  $: hp =
    card.status === "complete"
      ? 0
      : liveHP != null && liveHP >= 0
        ? liveHP
        : (card.target_hp ?? 100);

  // Raiders grouped into 4 role columns.
  const RAIDER_COLS = [
    { title: "Priests", classes: ["CLR", "SHM", "DRU"] },
    { title: "Casters", classes: ["MAG", "WIZ", "ENC", "NEC"] },
    { title: "Tanks", classes: ["WAR", "SHD", "PAL"] },
    { title: "DPS", classes: ["ROG", "MNK", "RNG", "BRD"] },
  ];
  $: raiderMap = Object.fromEntries(
    ((card.raiders && card.raiders.groups) || []).map((g) => [g.class, g]),
  );
  // Reactive columns: counts/lists must be derived here (not via a helper
  // function called from the template) or Svelte won't re-render them when the
  // card data refreshes — counts would freeze at their mount-time values.
  // Also guards members:null (no one of that class) from the server.
  $: raiderCols = RAIDER_COLS.map((col) => ({
    title: col.title,
    groups: col.classes.map((ab) => {
      const g = raiderMap[ab];
      return g && g.members ? g : { class: ab, members: [] };
    }),
  }));

  // Split the CH chain into main and rampage (RR#) so we can space them apart.
  $: chMain = (card.ch_chain || []).filter(
    (s) => !(s.label || "").startsWith("RR"),
  );
  $: chRamp = (card.ch_chain || []).filter((s) =>
    (s.label || "").startsWith("RR"),
  );

  // Show only debuffs that have actually been cast, in this fixed order.
  // Names match the server's debuff keys exactly (substring matching would let
  // ESlow light the Slow row and vice versa). Referencing card.debuffs directly
  // in the reactive statement is required so Svelte re-derives on card refresh.
  const DEBUFF_ORDER = ["Tash", "Malo", "OOS", "Slow", "ESlow", "Cripple"];
  function debuffList(list) {
    return DEBUFF_ORDER.map((name) => {
      const d = (list || []).find(
        (x) => x.name && x.name.toLowerCase() === name.toLowerCase(),
      );
      return d ? { name, caster: d.value } : null;
    }).filter(Boolean);
  }
  $: castDebuffs = debuffList(card.debuffs);

  // Current tank(s): two names (split across two adds) when the server sends
  // current_tanks, otherwise the single active main tank. Proc counts are looked
  // up per tank name from card.tank_procs (keyed lowercase).
  $: currentTanks =
    card.current_tanks && card.current_tanks.length >= 2
      ? card.current_tanks
      : card.active_main_tank
        ? [card.active_main_tank]
        : [];
  function procFor(procs, name) {
    if (!procs || !name) return 0;
    return procs[name.toLowerCase()] || 0;
  }
</script>

<div class="raidcard">
  <!-- Target Health (top) -->
  <div class="rc-target">
    <div class="rc-label">Target Health</div>
    <div class="rc-bar">
      <div class="rc-fill" style="width:{hp}%"></div>
      <span class="rc-bar-txt"
        >{card.status === "complete" ? "Dead" : hp + "%"}</span
      >
    </div>
  </div>

  <div class="rc-grid">
    <!-- Assignments -->
    <div class="rc-col">
      <div class="rc-label">Assignments</div>
      {#if currentTanks.length}<div class="rc-line">
          <span class="rc-k">{currentTanks.length > 1 ? "Current Tanks" : "Current Tank"}</span>
          <span class="rc-assignedname"
            >{#each currentTanks as tk, i}{i > 0 ? " / " : ""}{tk}{#if procFor(card.tank_procs, tk)}<span
                  class="rc-proc"
                  title="Weapon procs">⚡x{procFor(card.tank_procs, tk)}</span
                >{/if}{/each}</span
          >
        </div>{/if}
      {#if card.active_ramp_tank}<div class="rc-line">
          <span class="rc-k">Ramp Tank</span>
          <span class="rc-assignedname">{card.active_ramp_tank}</span>
        </div>{/if}
      <br />
      {#if card.main_tank_list}<div class="rc-line">
          <span class="rc-k">MT List</span>
          <span class="rc-assignedlist">{card.main_tank_list}</span>
        </div>{/if}
      {#if card.rampage_tank_list}<div class="rc-line">
          <span class="rc-k">Ramp List</span>
          <span class="rc-assignedlist">{card.rampage_tank_list}</span>
        </div>{/if}
      {#if card.trash_tank_list}<div class="rc-line">
          <span class="rc-k">Trash Tanks</span>
          <span class="rc-assignedlist">{card.trash_tank_list}</span>
        </div>{/if}
      {#if card.bump_list}<div class="rc-line">
          <span class="rc-k">Bump List</span>
          <span class="rc-assignedlist">{card.bump_list}</span>
        </div>{/if}
    </div>

    <!-- Debuffs — only show a line once that debuff has been cast -->
    <div class="rc-col">
      <div class="rc-label">Debuffs</div>
      {#each castDebuffs as { name, caster }}
        <div class="rc-line">
          <span class="rc-check">✓</span>
          <span class="rc-dname done">{name}</span>
          <span class="rc-caster">{caster}</span>
        </div>
      {:else}
        <div class="rc-line rc-none">None called yet</div>
      {/each}
      {#if card.sieve}
        <div class="rc-line">
          <span class="rc-check">✓</span>
          <span class="rc-dname done">Sieve</span>
          <span class="rc-caster">x{card.sieve}</span>
        </div>
      {/if}

      <!-- Current Target(s): non-boss adds we're fighting en route. One block, or
           two side-by-side when two adds are up, each with its own debuffs/sieve. -->
      {#if card.current_targets && card.current_targets.length}
        <div
          class="rc-curtargets"
          class:two={card.current_targets.length === 2}
        >
          {#each card.current_targets as ct}
            <div class="rc-curtarget">
              <div class="rc-sublabel">Current Target</div>
              <div class="rc-curname">{ct.name}</div>
              {#each debuffList(ct.debuffs) as { name, caster }}
                <div class="rc-line">
                  <span class="rc-check">✓</span>
                  <span class="rc-dname done">{name}</span>
                  <span class="rc-caster">{caster}</span>
                </div>
              {/each}
              {#if ct.sieve}
                <div class="rc-line">
                  <span class="rc-check">✓</span>
                  <span class="rc-dname done">Sieve</span>
                  <span class="rc-caster">x{ct.sieve}</span>
                </div>
              {/if}
              {#if !debuffList(ct.debuffs).length && !ct.sieve}
                <div class="rc-line rc-none">No debuffs yet</div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Clerics -->
    <div class="rc-col">
      <div class="rc-label">Clerics</div>
      {#if card.fluffer_clerics}<div class="rc-line">
          <span class="rc-k">Fluffer</span>{card.fluffer_clerics}
        </div>{/if}
      {#if card.ch_chain && card.ch_chain.length}
        <div class="rc-ch">
          {#each chMain as s}
            <div class="rc-line">
              <span class="rc-chnum" class:dead={s.dead}>{s.label}</span>
              <span class="rc-chcleric" class:dead={s.dead}
                >{s.cleric}{#if s.dead}<span class="rc-deadx">✗</span>{/if}</span
              >
              <span class="rc-charrow">→</span>
              <span class="rc-chtank">{s.tank}</span>
            </div>
          {/each}
          {#if chRamp.length}
            <div class="rc-chgap"></div>
            {#each chRamp as s}
              <div class="rc-line">
                <span class="rc-chnum ramp" class:dead={s.dead}>{s.label}</span>
                <span class="rc-chcleric" class:dead={s.dead}
                  >{s.cleric}{#if s.dead}<span class="rc-deadx">✗</span>{/if}</span
                >
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
    <div class="rc-label">
      Raiders <span class="rc-total"
        >{card.raiders ? card.raiders.total : 0}</span
      >
    </div>
    <div class="rc-raiders-cols">
      {#each raiderCols as col}
        <div class="rc-rcol">
          <div class="rc-rcol-title">{col.title}</div>
          {#each col.groups as g (g.class)}
            <div class="rc-class">
              <div
                class="rc-class-head"
                class:has={g.members.length}
                on:click={() => g.members.length && toggleClass(g.class)}
              >
                {#if g.members.length}<span class="rc-chev2"
                    >{openClass[g.class] ? "▾" : "▸"}</span
                  >{/if}
                <span class="rc-abbr">{g.class}</span>
                <span class="rc-cnt">({g.members.length})</span>
              </div>
              {#if openClass[g.class]}
                {#each g.members as m}
                  <div class="rc-member">
                    {m.name}
                    {#if m.level}
                      ({m.level})
                    {/if}{#if m.discord}
                      <span class="rc-disc">{m.discord}</span>{/if}
                  </div>
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
            {#if l.wiki_url}<a
                href={l.wiki_url}
                target="_blank"
                rel="noreferrer">{l.name}</a
              >{:else}{l.name}{/if}
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
        <a
          class="rc-chanlink"
          href={card.discord_url}
          target="_blank"
          rel="noreferrer">Open raid channel →</a
        >
      {:else}
        <div class="rc-none">Not linked yet</div>
      {/if}
    </div>
  </div>
</div>

<style>
  .rc-line:hover {
    background: rgba(255, 255, 255, 0.03);
  }

  .raidcard {
    padding: 8px 4px 6px 22px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .rc-grid {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 16px;
  }
  .rc-bottom {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
  .rc-raiders-cols {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 4px 14px;
  }
  .rc-rcol {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .rc-rcol-title {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-muted);
    margin-bottom: 2px;
  }
  @media (max-width: 720px) {
    .rc-grid,
    .rc-bottom {
      grid-template-columns: 1fr;
    }
    .rc-raiders-cols {
      grid-template-columns: 1fr 1fr;
    }
  }

  .rc-col {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  /* All section headers gold */
  .rc-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e3a008;
    margin-bottom: 3px;
  }

  .rc-line {
    font-size: 12px;
    color: var(--text-primary);
    word-break: break-word;
    line-height: 1.4;
    gap: 4px;
    align-items: baseline;
    display: flex;
    font-weight: 500;
    width: 100%;
  }

  /* Higher-contrast labels for assignment fields, Fluffer, debuff names */
  .rc-k {
    min-width: 66px;
    color: #d7dee6;
    margin-right: 6px;
  }
  .rc-assignedname {
    margin-left: auto;
    font-weight: 600;
  }
  .rc-assignedlist {
    margin-left: none;
    max-width: 100%;
    font-weight: 600;
  }
  .rc-dname {
    color: #d7dee6;
    font-weight: 600;
  }
  .rc-dname.done {
    color: var(--text-primary);
  }
  .rc-check {
    color: var(--success);
    font-weight: 800;
    margin-right: 10px;
  }
  .rc-caster {
    color: var(--text-secondary);
    margin-left: auto;
  }

  /* Proc counter beside a tank name (gold lightning, like the raid swords). */
  .rc-proc {
    color: #e3a008;
    font-weight: 700;
    margin-left: 6px;
    font-size: 11px;
    white-space: nowrap;
  }

  /* Current Target sub-sections inside the Debuffs column. */
  .rc-curtargets {
    margin-top: 8px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .rc-curtargets.two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
  }
  .rc-curtarget {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    border-top: 1px solid var(--border);
    padding-top: 5px;
  }
  .rc-sublabel {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .rc-curname {
    font-size: 12px;
    font-weight: 700;
    color: #e3a008;
    word-break: break-word;
    margin-bottom: 2px;
  }

  .rc-ch {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .rc-chnum {
    min-width: 34px;
    text-align: center;
    color: var(--bg);
    background: #2122af;
    border-radius: 3px;
    font-weight: 700;
    font-size: 11px;
  }
  .rc-chnum.ramp {
    background: #21227f;
  }
  /* Dead cleric: greyed slot number + struck-through name with an ✗. */
  .rc-chnum.dead {
    background: #4a4a4a;
    color: #9aa0a6;
  }
  .rc-chcleric.dead {
    color: var(--text-muted);
    text-decoration: line-through;
  }
  .rc-deadx {
    color: #ff5555;
    font-weight: 800;
    margin-left: 4px;
    text-decoration: none;
    display: inline-block;
  }
  .rc-chgap {
    height: 8px;
  }
  .rc-chcleric {
    color: var(--text-primary);
  }
  .rc-charrow {
    color: var(--text-muted);
  }
  .rc-chtank {
    color: var(--text-secondary);
    margin-left: auto;
  }

  .rc-total {
    color: var(--text-primary);
    font-weight: 400;
  }
  .rc-class {
    display: flex;
    flex-direction: column;
  }
  .rc-class-head {
    display: flex;
    align-items: center;
    font-size: 12px;
    color: var(--text-muted);
  }
  .rc-class-head.has {
    cursor: pointer;
    color: var(--text-primary);
  }
  .rc-abbr {
    font-weight: 500;
    min-width: 20px;
  }
  .rc-cnt {
    color: var(--text-accent);
    margin-left: 5px;
  }
  .rc-chev2 {
    font-size: 16px;
  }
  .rc-member {
    font-size: 12px;
    color: var(--text-secondary);
    margin-left: 5px;
    display: flex;
  }
  .rc-disc {
    color: var(--text-muted);
    margin-left: auto;
  }

  .rc-target {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .rc-bar {
    position: relative;
    height: 20px;
    border-radius: 4px;
    overflow: hidden;
    background: #3a1414;
    border: 1px solid #5c2020;
  }
  .rc-fill {
    position: absolute;
    inset: 0 auto 0 0;
    background: linear-gradient(90deg, #b91c1c, #ef4444);
    transition: width 0.8s ease;
  }
  .rc-bar-txt {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 700;
    color: #fff;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.6);
  }

  .rc-loot {
    font-size: 13px;
    color: var(--text-primary);
    min-width: 50%;
    display: flex;
  }
  .rc-loot a {
    color: var(--accent);
    text-decoration: none;
  }
  .rc-loot a:hover {
    text-decoration: underline;
  }
  .rc-price {
    color: #e3a008;
    font-size: 12px;
    margin-left: auto;
  }
  .rc-chanlink {
    color: var(--accent);
    font-size: 13px;
    text-decoration: none;
  }
  .rc-chanlink:hover {
    text-decoration: underline;
  }
  .rc-none {
    font-size: 13px;
    color: var(--text-muted);
    font-style: italic;
  }
</style>
