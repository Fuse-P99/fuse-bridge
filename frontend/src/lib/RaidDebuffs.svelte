<script>
  // The raid card's Debuffs section — boss debuffs + sieve, plus per-add
  // Current Target blocks. Shared by RaidCardView and the Raid Debuffs overlay.
  //
  // Boss debuff rows carry a countdown: a bar in the row background plus the
  // remaining time after the name ("Malo 5:23"). Adds get no countdowns — they
  // never live long enough for the timers to matter. Local-first: the client's
  // own guild-chat parse (GetLocalRaidTimers) starts the countdown instantly;
  // the server's at_ms on the card is the fallback for out-of-game viewers.
  // Duration is the matching Fuse "Debuffs Macros" trigger's configured timer.
  import { onMount, onDestroy } from "svelte";
  import { GetLocalRaidTimers } from "../../bindings/FuseBridge/app.js";

  export let card;
  // See RaidAssignments: the heading is for the raid card, not the overlay.
  export let showLabel = true;
  // Anything to show? Drives the overlay rendering nothing at all rather than
  // an empty box reading "None called yet".
  export let hasAny = false;

  // Show only debuffs that have actually been cast, in this fixed order.
  // Names match the server's debuff keys exactly (substring matching would let
  // ESlow light the Slow row and vice versa).
  const DEBUFF_ORDER = ["Tash", "Malo", "OOS", "Slow", "ESlow", "Cripple"];
  function debuffList(list) {
    return DEBUFF_ORDER.map((name) => {
      const d = (list || []).find(
        (x) => x.name && x.name.toLowerCase() === name.toLowerCase(),
      );
      return d ? { name, caster: d.value, atMs: d.at_ms || 0 } : null;
    }).filter(Boolean);
  }
  $: castDebuffs = debuffList(card.debuffs);
  $: hasAny = !!(
    castDebuffs.length ||
    card.sieve ||
    (card.current_targets && card.current_targets.length)
  );

  // ── countdown bars ───────────────────────────────────────────────────────
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

  const norm = (s) => (s || "").toLowerCase().replace(/[^a-z0-9]/g, "");

  // Countdown state ({frac, remain} or null) for one debuff row on one mob.
  // Prefers the local sighting (matched on the shorthand target text the
  // debuffer typed); falls back to the server's cast time. Duration comes from
  // the local entry or the Fuse-package duration table.
  function debuffTimer(nowMs, timers, name, mobName, serverAtMs) {
    if (!card || card.status === "complete") return null;
    const key = (name || "").toLowerCase();
    const durs = (timers && timers.debuff_durations) || {};
    let at = 0;
    let dur = 0;
    const cands = ((timers && timers.debuffs) || []).filter(
      (d) => d.name === key,
    );
    if (cands.length) {
      const mobN = norm(mobName);
      let best = null;
      for (const d of cands) {
        const dn = norm(d.target);
        if (mobN && dn && (mobN.includes(dn) || dn.includes(mobN))) {
          if (!best || d.at_ms > best.at_ms) best = d;
        }
      }
      // A single sighting with no name overlap still belongs to the only mob
      // the server shows this debuff on.
      if (!best && cands.length === 1) best = cands[0];
      if (best) {
        at = best.at_ms;
        dur = best.dur_ms || durs[key] || 0;
      }
    }
    if (!at && serverAtMs) {
      at = serverAtMs;
      dur = durs[key] || 0;
    }
    if (!at || !dur) return null;
    const remain = at + dur - nowMs;
    if (remain <= 0) return null;
    return { frac: Math.min(1, remain / dur), remain };
  }

  // "5:23" — minutes unpadded, seconds padded, for the text after the name.
  function fmtRemain(ms) {
    const s = Math.max(0, Math.ceil(ms / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }
</script>

<div class="rc-col">
  {#if showLabel}<div class="rc-label">Debuffs</div>{/if}
  {#each castDebuffs as { name, caster, atMs }}
    {@const t = debuffTimer(now, lt, name, card.target, atMs)}
    <div class="rc-line">
      {#if t}<div class="rc-tfill" style="width:{t.frac * 100}%"></div>{/if}
      <span class="rc-check">✓</span>
      <span class="rc-dname done">{name}</span>
      {#if t}<span class="rc-time">{fmtRemain(t.remain)}</span>{/if}
      <span class="rc-caster">{caster}</span>
    </div>
  {:else}
    <div class="rc-line rc-none">None called yet</div>
  {/each}
  {#if card.sieve}
    <div class="rc-line">
      <span class="rc-check">✓</span>
      <span class="rc-dname done">Sieve</span>
      {#key card.sieve}<span class="rc-caster rc-countbox">x{card.sieve}</span
        >{/key}
    </div>
  {/if}

  <!-- Current Target(s): non-boss adds we're fighting en route. One block, or
       two side-by-side when two adds are up, each with its own debuffs/sieve. -->
  {#if card.current_targets && card.current_targets.length}
    <div class="rc-curtargets" class:two={card.current_targets.length === 2}>
      {#each card.current_targets as ct}
        <div class="rc-curtarget">
          <div class="rc-sublabel">Current Target</div>
          <div class="rc-curname">{ct.name}</div>
          <!-- No countdowns on adds: they die too fast for the timers to matter. -->
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
              {#key ct.sieve}<span class="rc-caster rc-countbox"
                  >x{ct.sieve}</span
                >{/key}
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
  /* Debuff countdown fill, behind the row's text. */
  .rc-tfill {
    position: absolute;
    inset: 0 auto 0 0;
    background: rgba(79, 179, 169, 0.22);
    pointer-events: none;
  }
  /* Text sits above the countdown fill (position: relative wins the paint
     order against the absolutely-positioned bar). */
  .rc-dname {
    position: relative;
    color: #d7dee6;
    font-weight: 600;
  }
  .rc-dname.done {
    color: var(--text-primary);
  }
  .rc-check {
    position: relative;
    color: var(--success);
    font-weight: 800;
    margin-right: 10px;
  }
  /* Remaining time after the debuff name, e.g. "Malo 5:23". */
  .rc-time {
    position: relative;
    color: var(--text-secondary);
    font-variant-numeric: tabular-nums;
  }
  /* Sieve count in a rounded borderless box that's invisible at rest and
     lights up on increment (the {#key} remount restarts the animation). */
  .rc-countbox {
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
  .rc-caster {
    position: relative;
    color: var(--text-secondary);
    margin-left: auto;
  }
  .rc-none {
    font-size: 13px;
    color: var(--text-muted);
    font-style: italic;
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
</style>
