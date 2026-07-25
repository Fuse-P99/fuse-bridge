<script>
  // The raid card's Clerics section — fluffer assignment and the CH chain
  // (main + rampage). Shared by RaidCardView and the Raid Clerics overlay.
  //
  // While a slot's cleric is casting, the row shows a 10-second cast bar in
  // its background (a blue a shade darker than the ### badge) plus the seconds
  // remaining ("7s") after the tank's name. Local-first: this client's own
  // guild-chat parse starts/restarts the countdown the instant the call is
  // seen; the server's called_at_ms is the fallback for out-of-game viewers.
  // A repeat call of the same slot+cleric restarts it; "Your spell is
  // interrupted." (local for own casts, server-relayed for everyone else's)
  // stops it.
  import { onMount, onDestroy } from "svelte";
  import { GetLocalRaidTimers } from "../../bindings/FuseBridge/app.js";

  export let card;

  const CH_MS = 10000; // Complete Heal cast time

  // Split the CH chain into main and rampage (RR#) so we can space them apart.
  $: chMain = (card.ch_chain || []).filter(
    (s) => !(s.label || "").startsWith("RR"),
  );
  $: chRamp = (card.ch_chain || []).filter((s) =>
    (s.label || "").startsWith("RR"),
  );

  // ── cast bars ────────────────────────────────────────────────────────────
  let lt = null; // GetLocalRaidTimers payload
  let now = Date.now();
  let pollTimer, animReq;

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
    pollTimer = setInterval(poll, 1000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (animReq) cancelAnimationFrame(animReq);
  });

  // Cast state ({frac, remain} or null when idle) of a slot's 10s CH cast.
  function chTimer(nowMs, timers, slot) {
    if (!card || card.status === "complete" || slot.dead) return null;
    const cleric = (slot.cleric || "").toLowerCase();
    const loc = ((timers && timers.ch) || []).find(
      (c) =>
        c.label === slot.label && (c.cleric || "").toLowerCase() === cleric,
    );
    let at = slot.called_at_ms || 0;
    let intr = slot.interrupted_at_ms || 0;
    if (loc) {
      // The local sighting is the accurate clock when it's the same cast the
      // server reported (within a few seconds); a genuinely newer time on
      // either side means a re-call and wins outright.
      at = !at || Math.abs(loc.at_ms - at) < 4000 ? loc.at_ms : Math.max(at, loc.at_ms);
      intr = Math.max(intr, loc.interrupted_at_ms || 0);
    }
    if (!at || intr >= at) return null;
    const remain = at + CH_MS - nowMs;
    if (remain <= 0) return null;
    return { frac: Math.min(1, remain / CH_MS), remain };
  }
</script>

<div class="rc-col">
  <div class="rc-label">Clerics</div>
  {#if card.fluffer_clerics}<div class="rc-line">
      <span class="rc-k">Fluffer</span>{card.fluffer_clerics}
    </div>{/if}
  {#if card.ch_chain && card.ch_chain.length}
    <div class="rc-ch">
      {#each chMain as s}
        {@const t = chTimer(now, lt, s)}
        <div class="rc-line">
          {#if t}<div class="rc-chfill" style="width:{t.frac * 100}%"></div>{/if}
          <span class="rc-chnum" class:dead={s.dead}>{s.label}</span>
          <span class="rc-chcleric" class:dead={s.dead}
            >{s.cleric}{#if s.dead}<span class="rc-deadx">✗</span>{/if}</span
          >
          <span class="rc-charrow">→</span>
          <span class="rc-chtank">{s.tank}</span>
          {#if t}<span class="rc-chtime">{Math.ceil(t.remain / 1000)}s</span>{/if}
        </div>
      {/each}
      {#if chRamp.length}
        <div class="rc-chgap"></div>
        {#each chRamp as s}
          {@const t = chTimer(now, lt, s)}
          <div class="rc-line">
            {#if t}<div
                class="rc-chfill"
                style="width:{t.frac * 100}%"
              ></div>{/if}
            <span class="rc-chnum ramp" class:dead={s.dead}>{s.label}</span>
            <span class="rc-chcleric" class:dead={s.dead}
              >{s.cleric}{#if s.dead}<span class="rc-deadx">✗</span>{/if}</span
            >
            <span class="rc-charrow">→</span>
            <span class="rc-chtank">{s.tank}</span>
            {#if t}<span class="rc-chtime">{Math.ceil(t.remain / 1000)}s</span
              >{/if}
          </div>
        {/each}
      {/if}
    </div>
  {/if}
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
    position: relative;
    font-size: 12px;
    color: var(--text-primary);
    word-break: break-word;
    line-height: 1.4;
    gap: 4px;
    align-items: baseline;
    display: flex;
    font-weight: 500;
    width: 100%;
    border-radius: 3px;
    overflow: hidden;
  }
  .rc-line:hover {
    background: rgba(255, 255, 255, 0.03);
  }
  /* CH cast bar, depleting over the 10s cast, behind the row's text — a blue
     one shade darker than the #2122af ### badge so the badge stays readable
     on top of it. */
  .rc-chfill {
    position: absolute;
    inset: 0 auto 0 0;
    background: rgba(23, 24, 128, 0.6);
    pointer-events: none;
  }
  .rc-k {
    min-width: 66px;
    color: #d7dee6;
    margin-right: 6px;
  }

  .rc-ch {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  /* Chain-row text sits above the grey cast fill (position: relative wins the
     paint order against the absolutely-positioned bar). */
  .rc-chnum {
    position: relative;
    min-width: 34px;
    text-align: center;
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
    position: relative;
    color: var(--text-primary);
  }
  .rc-charrow {
    position: relative;
    color: var(--text-muted);
  }
  .rc-chtank {
    position: relative;
    color: var(--text-secondary);
    margin-left: auto;
  }
  /* Seconds left in the cast, after the tank name — only while casting. */
  .rc-chtime {
    position: relative;
    color: var(--text-primary);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
</style>
