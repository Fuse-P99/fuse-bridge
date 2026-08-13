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
  import { Events } from "@wailsio/runtime";
  import { GetLocalRaidTimers } from "../../bindings/FuseBridge/app.js";

  export let card;
  // See RaidAssignments: the heading is for the raid card, not the overlay.
  export let showLabel = true;
  // Anything to show? See RaidAssignments.
  export let hasAny = false;
  // Overlay "Flashing" setting: off suppresses the cast-start flash on the
  // cleric's name. The raid card (tab) never passes it, so the tab flashes.
  export let flash = true;
  // Overlay "Show Timing": append "+3.4s" after each cleric's name — how long
  // after the previous caster in the chain they started their cast.
  export let showTiming = false;

  // Per-slot timing, keyed by chain label. Computed when THAT slot's cast
  // time changes and frozen until its next cast — so the value survives the
  // predecessor going again on the next rotation (a live my-at−pred-at would
  // flip negative there and vanish). Everyone's rows get one, not just the
  // viewer's.
  let chDeltas = {}; // label → {at, txt}
  function updateDeltas(rows) {
    for (let i = 0; i < rows.length; i++) {
      const s = rows[i].s;
      const at = s.called_at_ms || 0;
      if (!at || s.dead || s.stale) continue;
      const cur = chDeltas[s.label];
      if (cur && cur.at === at) continue; // same cast — keep the frozen value
      // The nearest rotating predecessor (wrapping); dead and stale slots
      // don't rotate, so they're skipped, exactly like the metronome walk.
      let pred = null;
      for (let step = 1; step < rows.length; step++) {
        const p = rows[(i - step + rows.length) % rows.length].s;
        if (p.dead || p.stale) continue;
        pred = p;
        break;
      }
      const pat = pred ? pred.called_at_ms || 0 : 0;
      const d = (at - pat) / 1000;
      // A gap over 30s isn't chain timing — it's a fresh burst after a lull.
      if (!pat || d <= 0 || d > 30) continue;
      chDeltas[s.label] = { at, txt: `+${d.toFixed(1)}s` };
    }
    chDeltas = chDeltas;
  }
  $: if (showTiming) {
    updateDeltas(mainRows);
    updateDeltas(rampRows);
  }
  // Render only while the cached value belongs to the slot's CURRENT cast, so
  // a new fight or a re-called slot can't show a stale number. Takes the map
  // as an argument so the template re-evaluates when chDeltas changes —
  // reading it only inside the body would hide the dependency from Svelte.
  function deltaFor(map, s) {
    const c = map[s.label];
    return c && c.at === (s.called_at_ms || 0) ? c.txt : "";
  }

  $: hasAny = !!(
    card.fluffer_clerics ||
    (card.ch_chain && card.ch_chain.length)
  );

  const CH_MS = 10000; // Complete Heal cast time
  // Second marks along the cast bar: a notch each 20% (= every 2s of the 10s
  // cast), so a cleric can pace the chain off the bar's edge crossing them
  // without reading the countdown number.
  const CH_TICKS = [20, 40, 60, 80];

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

  // ── the user's own slot ──────────────────────────────────────────────────
  // The tailed character's row renders in gold, and when the slot BEFORE
  // theirs starts casting the row grows a gold ring that pulses once per
  // second, aligned to that cast's start — the metronome clerics count
  // between CHs. It runs until the user's own cast starts, and dies with the
  // predecessor's 10s window either way.
  $: me = ((lt && lt.toon) || "").toLowerCase();

  // The ring can mount a beat late (a server-fallback sighting of the
  // predecessor's cast), so a negative animation-delay slots it into the
  // right phase: pulses land exactly on whole seconds since the cast began.
  // Depends only on the cast's start time, so it's computed once per cast.
  function pulseDelay(at) {
    if (!at) return 0;
    return -((Date.now() - at) % 1000);
  }

  // Rows with their live cast state, so "is the slot before mine casting"
  // is answerable without recomputing timers per lookup.
  $: mainRows = chMain.map((s) => ({ s, t: chTimer(now, lt, s) }));
  $: rampRows = chRamp.map((s) => ({ s, t: chTimer(now, lt, s) }));

  // The cast start (at ms) of the nearest live predecessor of the user's
  // slot while it is mid-cast, else 0. Dead and stale slots don't rotate, so
  // they're skipped both when finding "me" and when walking backwards; the
  // walk wraps because the chain does.
  function nextAtFor(rows, meName) {
    if (!meName) return 0;
    const myIdx = rows.findIndex(
      (r) =>
        !r.s.dead && !r.s.stale && (r.s.cleric || "").toLowerCase() === meName,
    );
    if (myIdx < 0) return 0;
    for (let step = 1; step < rows.length; step++) {
      const r = rows[(myIdx - step + rows.length) % rows.length];
      if (r.s.dead || r.s.stale) continue;
      return r.t ? r.t.at : 0; // the one true predecessor decides
    }
    return 0;
  }
  $: mainNextAt = nextAtFor(mainRows, me);
  $: rampNextAt = nextAtFor(rampRows, me);

  // You got skipped: the nearest live slot AFTER the user's started a cast
  // NEWER than the predecessor's — the chain moved past the user's spot, so
  // the count is over. The start-time comparison is what separates a real
  // skip from the successor's previous-rotation bar still draining; in a
  // two-slot chain the walk finds the predecessor itself, whose cast can
  // never out-date itself, so it degrades to "never skipped" cleanly.
  function skippedFor(rows, meName, prevAt) {
    if (!meName || !prevAt) return false;
    const myIdx = rows.findIndex(
      (r) =>
        !r.s.dead && !r.s.stale && (r.s.cleric || "").toLowerCase() === meName,
    );
    if (myIdx < 0) return false;
    for (let step = 1; step < rows.length; step++) {
      const r = rows[(myIdx + step) % rows.length];
      if (r.s.dead || r.s.stale) continue;
      return !!r.t && r.t.at > prevAt;
    }
    return false;
  }
  $: mainSkipped = skippedFor(mainRows, me, mainNextAt);
  $: rampSkipped = skippedFor(rampRows, me, rampNextAt);

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
      // server reported; a genuinely newer time on either side means a re-call
      // and wins outright. The same-cast window must clear the forward
      // pipeline's worst skew (2s send batch + server processing + the card's
      // 5s poll) or a busy night re-anchors a running bar to the server's
      // later clock mid-cast; re-calls of one slot sit a full chain cycle
      // apart (15s+), so 8s stays unambiguous.
      at = !at || Math.abs(loc.at_ms - at) < 8000 ? loc.at_ms : Math.max(at, loc.at_ms);
      intr = Math.max(intr, loc.interrupted_at_ms || 0);
    }
    if (!at || intr >= at) return null;
    const remain = at + CH_MS - nowMs;
    if (remain <= 0) return null;
    // at identifies the cast: the caster's pulse keys on it, so a re-call
    // (new at) replays the flash while the frames of one cast never do.
    return { frac: Math.min(1, remain / CH_MS), remain, at };
  }
</script>

<div class="rc-col" class:noflash={!flash}>
  {#if showLabel}<div class="rc-label">Clerics</div>{/if}
  {#if card.fluffer_clerics}<div class="rc-line">
      <span class="rc-k">Fluffer</span>{card.fluffer_clerics}
    </div>{/if}
  {#if card.ch_chain && card.ch_chain.length}
    <div class="rc-ch">
      {#each mainRows as row}
        {@const s = row.s}
        {@const t = row.t}
        {@const mine = me && !s.dead && (s.cleric || "").toLowerCase() === me}
        <div
          class="rc-line"
          class:mine
          class:stale={s.stale}
          title={s.stale ? s.stale_why || "inactive" : null}
        >
          {#if t}
            <div class="rc-chfill" style="width:{t.frac * 100}%"></div>
            {#each CH_TICKS as p}
              <div class="rc-chtick" style="left: {p}%"></div>
            {/each}
          {/if}
          {#if mine && mainNextAt && !t && !mainSkipped}
            {#key mainNextAt}<div
                class="rc-nextring"
                style="animation-delay: {pulseDelay(mainNextAt)}ms"
              ></div>{/key}
          {/if}
          <span class="rc-chnum" class:dead={s.dead} class:stale={s.stale}
            >{s.label}</span
          >
          {#key t ? t.at : 0}
            <span class="rc-chcleric" class:cast={!!t} class:dead={s.dead}
              >{s.cleric}{#if s.dead}<span class="rc-deadx">✗</span>{/if}</span
            >
          {/key}
          {#if showTiming && deltaFor(chDeltas, s)}<span class="rc-chdelta"
              >{deltaFor(chDeltas, s)}</span
            >{/if}
          <span class="rc-charrow">→</span>
          <span class="rc-chtank">{s.tank}</span>
          {#if t}<span class="rc-chtime">{Math.ceil(t.remain / 1000)}s</span>{/if}
        </div>
      {/each}
      {#if rampRows.length}
        <div class="rc-chgap"></div>
        {#each rampRows as row}
          {@const s = row.s}
          {@const t = row.t}
          {@const mine = me && !s.dead && (s.cleric || "").toLowerCase() === me}
          <div
            class="rc-line"
            class:mine
            class:stale={s.stale}
            title={s.stale ? s.stale_why || "inactive" : null}
          >
            {#if t}
              <div class="rc-chfill" style="width:{t.frac * 100}%"></div>
              {#each CH_TICKS as p}
                <div class="rc-chtick" style="left: {p}%"></div>
              {/each}
            {/if}
            {#if mine && rampNextAt && !t && !rampSkipped}
              {#key rampNextAt}<div
                  class="rc-nextring"
                  style="animation-delay: {pulseDelay(rampNextAt)}ms"
                ></div>{/key}
            {/if}
            <span class="rc-chnum ramp" class:dead={s.dead} class:stale={s.stale}
              >{s.label}</span
            >
            {#key t ? t.at : 0}
              <span class="rc-chcleric" class:cast={!!t} class:dead={s.dead}
                >{s.cleric}{#if s.dead}<span class="rc-deadx">✗</span>{/if}</span
              >
            {/key}
            {#if showTiming && deltaFor(chDeltas, s)}<span class="rc-chdelta"
                >{deltaFor(chDeltas, s)}</span
              >{/if}
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
  /* The 2s notches. Fixed positions on the row (the bar's track), so the
     fill's edge sweeps across them; bottom-anchored and dim enough to read as
     ruler marks under the text on filled and empty track alike. */
  .rc-chtick {
    position: absolute;
    bottom: 1px;
    height: 45%;
    width: 1px;
    background: rgba(255, 255, 255, 0.35);
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
  /* The user's own slot: gold badge, gold cast bar, gold name — findable at
     a glance in a nine-row chain. */
  .rc-line.mine .rc-chnum {
    background: #b8860b;
    color: #16130a;
  }
  .rc-line.mine .rc-chfill {
    background: rgba(184, 134, 11, 0.5);
  }
  .rc-line.mine .rc-chcleric {
    color: #f5d67b;
    font-weight: 700;
  }
  /* "You're next": a gold ring around the user's row while the previous slot
     is casting — flashing once per second, phase-locked to that cast's start
     (see pulseDelay), so it ticks the seconds a cleric counts between CHs.
     It disappears when the user's own cast starts, when the next cleric goes
     without them (skipped — see skippedFor), or when the predecessor's 10s
     window ends; the {#key} remount re-anchors it per cast. */
  .rc-nextring {
    position: absolute;
    inset: 0;
    border: 1px solid #e3a008;
    border-radius: 3px;
    pointer-events: none;
    animation: nextpulse 1s ease-out infinite;
  }
  @keyframes nextpulse {
    from {
      border-color: #ffd76e;
      box-shadow:
        0 0 8px 2px rgba(227, 160, 8, 0.75),
        inset 0 0 6px rgba(227, 160, 8, 0.55);
    }
    to {
      border-color: #e3a008;
      box-shadow: none;
    }
  }
  /* Stale slot (moved position / missed cycles / left the zone): the same
     grey as death but no ✗ — they're out of the rotation, not down. Declared
     after .mine so a stale own-slot reads stale, not gold. */
  .rc-chnum.stale,
  .rc-line.mine .rc-chnum.stale {
    background: #4a4a4a;
    color: #9aa0a6;
  }
  .rc-line.stale .rc-chcleric {
    color: var(--text-muted);
    font-weight: 500;
  }
  .rc-line.stale .rc-chtank {
    color: var(--text-muted);
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
    border-radius: 3px;
  }
  /* The caster lights up as their cast starts — the same sharp-flash,
     slow-decay pulse as the sieve/proc counters (the {#key} remount on the
     cast's start time restarts it; one cast never replays it). */
  .rc-chcleric.cast {
    animation: countflash 0.5s ease-out;
  }
  /* Overlay "Flashing" off: the cast still highlights structurally (bar,
     countdown), it just doesn't pulse. */
  .noflash .rc-chcleric.cast {
    animation: none;
  }
  /* "Show Timing": seconds since the previous caster went, after the name. */
  .rc-chdelta {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    white-space: nowrap;
  }
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
