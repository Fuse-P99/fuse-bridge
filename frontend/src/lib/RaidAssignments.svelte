<script>
  // The raid card's Assignments section — current tank(s) with proc counts,
  // ramp tank, and the called tank/bump lists. Shared by RaidCardView and the
  // Raid Assignments overlay so both render identically.
  //
  // Fighting-style disciplines (Defensive / Evasive) get a countdown row under
  // the tank who popped one. Local-first, like the debuff and CH bars: the
  // client's own log parse starts the bar immediately and the server's card is
  // the fallback for viewers who aren't in game. Windows are tracked for every
  // warrior, not just the current tank, so a tank called up mid-window shows the
  // time they have left rather than starting blank.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { GetLocalRaidTimers } from "../../bindings/FuseBridge/app.js";

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

  // ── discipline countdowns ──────────────────────────────────────────────────
  const DISC_DUR = 180000; // matches tankDiscDuration / discDurMs
  const DISC_WARN = 30000; // last 30s: slow pulse

  let lt = null; // GetLocalRaidTimers payload
  let now = Date.now();
  let pollTimer, animReq, offTimers;

  async function poll() {
    if (!card || card.status === "complete") return;
    try {
      lt = await GetLocalRaidTimers();
    } catch {
      /* keep last */
    }
  }
  function animLoop() {
    now = Date.now();
    animReq = requestAnimationFrame(animLoop);
  }
  onMount(() => {
    poll();
    // Push: repaint the instant a call is seen in the local log. The interval
    // stays as a safety net (missed event, server-side updates).
    offTimers = Events.On("raidtimers-changed", poll);
    pollTimer = setInterval(poll, 1000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (offTimers) offTimers();
    if (animReq) cancelAnimationFrame(animReq);
  });

  // The live discipline window for one tank, or null. Prefers the local sighting
  // (no round trip); falls back to the server's card for out-of-game viewers.
  function discFor(nowMs, timers, cardIn, name) {
    if (!name || !cardIn || cardIn.status === "complete") return null;
    const key = name.toLowerCase();
    let at = 0;
    let dur = DISC_DUR;
    let kind = "";
    for (const d of (timers && timers.discs) || []) {
      if ((d.name || "").toLowerCase() === key && d.at_ms > at) {
        at = d.at_ms;
        dur = d.dur_ms || DISC_DUR;
        kind = d.kind;
      }
    }
    if (!at) {
      const s = (cardIn.tank_discs || {})[key];
      if (s && s.at_ms) {
        at = s.at_ms;
        kind = s.kind;
      }
    }
    if (!at) return null;
    const remain = at + dur - nowMs;
    if (remain <= 0) return null;
    return {
      label: kind === "evasive" ? "EVASIVE" : "DEFENSIVE",
      frac: Math.max(0, Math.min(1, remain / dur)),
      remain,
      warn: remain <= DISC_WARN,
    };
  }

  // "2:14" — the same shape the debuff rows use.
  function fmtRemain(ms) {
    const s = Math.max(0, Math.ceil(ms / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }
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
  <!-- Discipline window(s) for whoever is tanking right now. One row per tank
       with a live disc; the name is only repeated when two are tanking, where
       an unlabelled row would be ambiguous. -->
  {#each currentTanks as tk}
    {@const d = discFor(now, lt, card, tk)}
    {#if d}
      <div class="rc-disc" class:warn={d.warn}>
        <div class="rc-discfill" style="width:{d.frac * 100}%"></div>
        <span class="rc-discico">🛡</span>
        <span class="rc-disclabel">{d.label}</span>
        {#if currentTanks.length > 1}<span class="rc-discwho">{tk}</span>{/if}
        <span class="rc-disctime">{fmtRemain(d.remain)}</span>
      </div>
    {/if}
  {/each}
  {#if card.active_ramp_tank}<div class="rc-line">
      <span class="rc-k">Ramp Tank</span>
      <span class="rc-assignedname">{card.active_ramp_tank}</span>
    </div>{/if}
  {#if card.active_ramp_tank}
    {@const d = discFor(now, lt, card, card.active_ramp_tank)}
    {#if d}
      <div class="rc-disc" class:warn={d.warn}>
        <div class="rc-discfill" style="width:{d.frac * 100}%"></div>
        <span class="rc-discico">🛡</span>
        <span class="rc-disclabel">{d.label}</span>
        <span class="rc-disctime">{fmtRemain(d.remain)}</span>
      </div>
    {/if}
  {/if}
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
  /* ── discipline countdown ─────────────────────────────────────────────────
     Sits directly under the tank it belongs to, indented so it reads as a
     property of that line rather than another assignment. The drain bar is the
     background, same anatomy as the debuff rows. */
  .rc-disc {
    position: relative;
    display: flex;
    align-items: baseline;
    gap: 5px;
    margin-left: 10px;
    padding: 1px 6px;
    border-radius: 3px;
    overflow: hidden;
    font-size: 11px;
    line-height: 1.5;
    background: rgba(255, 255, 255, 0.05);
  }
  .rc-discfill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    background: rgba(79, 179, 169, 0.4);
  }
  .rc-discico,
  .rc-disclabel,
  .rc-discwho,
  .rc-disctime {
    position: relative; /* above the fill */
  }
  .rc-disclabel {
    font-weight: 700;
    letter-spacing: 0.07em;
    color: #7fd8cf;
  }
  .rc-discwho {
    color: var(--text-secondary);
  }
  .rc-disctime {
    margin-left: auto;
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }
  /* Last 30 seconds: a slow, shallow pulse — enough to catch the eye on a
     glance without competing with the raid's actual alerts. */
  .rc-disc.warn .rc-discfill {
    background: rgba(227, 160, 8, 0.45);
    animation: discpulse 1.6s ease-in-out infinite;
  }
  .rc-disc.warn .rc-disclabel {
    color: #ffd45e;
  }
  @keyframes discpulse {
    50% {
      opacity: 0.45;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .rc-disc.warn .rc-discfill {
      animation: none;
    }
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
