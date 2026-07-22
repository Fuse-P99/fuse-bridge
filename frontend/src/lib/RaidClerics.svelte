<script>
  // The raid card's Clerics section — fluffer assignment and the CH chain
  // (main + rampage). Shared by RaidCardView and the Raid Clerics overlay.
  //
  // Each chain slot shows a grey 10-second cast bar while its cleric's CH is
  // in flight. Local-first: this client's own guild-chat parse starts/restarts
  // the bar the instant the call is seen; the server's called_at_ms is the
  // fallback for out-of-game viewers. A repeat call of the same slot+cleric
  // restarts the bar; "Your spell is interrupted." (local for own casts,
  // server-relayed for everyone else's) stops it.
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

  // Remaining fraction (0..1, 0 = no bar) of a slot's 10s CH cast.
  function chFrac(nowMs, timers, slot) {
    if (!card || card.status === "complete" || slot.dead) return 0;
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
    if (!at || intr >= at) return 0;
    const remain = at + CH_MS - nowMs;
    return remain > 0 ? Math.min(1, remain / CH_MS) : 0;
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
        {@const frac = chFrac(now, lt, s)}
        <div class="rc-line">
          {#if frac > 0}<div class="rc-chfill" style="width:{frac * 100}%"></div>{/if}
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
          {@const frac = chFrac(now, lt, s)}
          <div class="rc-line">
            {#if frac > 0}<div
                class="rc-chfill"
                style="width:{frac * 100}%"
              ></div>{/if}
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
  /* Grey CH cast bar, depleting over the 10s cast, behind the row's text. */
  .rc-chfill {
    position: absolute;
    inset: 0 auto 0 0;
    background: rgba(154, 160, 166, 0.22);
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
</style>
