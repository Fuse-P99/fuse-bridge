<script>
  // The raid card's Assignments section — current tank(s) with proc counts,
  // ramp tank, and the called tank/bump lists. Shared by RaidCardView and the
  // Raid Assignments overlay so both render identically.
  export let card;
  // The section heading. Wanted on the raid card, where three sections sit
  // side by side; redundant in the Special Overlay, whose title bar already
  // says "Raid Assignments".
  export let showLabel = true;
  // Whether this section has anything at all to show. The overlay renders no
  // box (and, in "Hide when 0" mode, no title bar) until it does — an empty
  // panel is a translucent strip over the game saying nothing.
  export let hasAny = false;

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

  $: hasAny = !!(
    currentTanks.length ||
    card.active_ramp_tank ||
    card.main_tank_list ||
    card.rampage_tank_list ||
    card.trash_tank_list ||
    card.bump_list ||
    card.main_assist
  );
</script>

<div class="rc-col">
  {#if showLabel}<div class="rc-label">Assignments</div>{/if}
  {#if currentTanks.length}<div class="rc-line">
      <span class="rc-k"
        >{currentTanks.length > 1 ? "Current Tanks" : "Current Tank"}</span
      >
      <span class="rc-assignedname"
        >{#each currentTanks as tk, i}{i > 0 ? " / " : ""}{tk}{#if procFor(card.tank_procs, tk)}{#key procFor(card.tank_procs, tk)}<span
              class="rc-proc"
              title="Weapon procs">⚡x{procFor(card.tank_procs, tk)}</span
            >{/key}{/if}{/each}</span
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
  <!-- Main Assist — from a guild "ASSIST - Name" call; all raid kinds. -->
  {#if card.main_assist}<div class="rc-line">
      <span class="rc-k">Main Assist</span>
      <span class="rc-assignedname">{card.main_assist}</span>
    </div>{/if}
</div>

<style>
  .rc-col {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }
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
  .rc-line:hover {
    background: rgba(255, 255, 255, 0.03);
  }
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
  /* Proc counter beside a tank name (gold lightning, like the raid swords),
     in a rounded borderless box that's invisible at rest and lights up on
     increment (the {#key} remount restarts the animation). */
  .rc-proc {
    color: #e3a008;
    font-weight: 700;
    margin-left: 6px;
    font-size: 11px;
    white-space: nowrap;
    border-radius: 5px;
    padding: 0 5px;
    background: transparent;
    animation: countflash 0.5s ease-out;
  }
  /* Sharp pulse of brightness that decays more slowly (ease-out tail). */
  @keyframes countflash {
    from {
      background: rgba(255, 255, 255, 0.5);
      filter: brightness(1.9);
    }
    to {
      background: transparent;
      filter: brightness(1);
    }
  }
</style>
