<script>
  import { onMount, onDestroy } from "svelte";
  import { GetRaidDPS } from "../../bindings/FuseBridge/app.js";
  import { classAbbr } from "./classAbbr.js";
  import ParseDialog from "./ParseDialog.svelte";

  // hasAny lets the card hide the whole row when nothing is being fought —
  // same contract as OtherTimers.
  export let hasAny = false;
  // The overlay is already titled "Raid DPS" by its window frame, so the
  // in-card header would be saying it twice — same reason OtherTimers has this.
  export let showLabel = true;
  // The card this board belongs to. Its target scopes the request, so a
  // completed raid keeps showing ITS fight's damage instead of whatever is
  // being killed now — several cards are on screen at once. No card (the
  // overlay) asks for the live fight.
  export let card = null;

  // Only a COMPLETED card names its fight. An active one asks for the live
  // view (empty mob) because a raid is not always on the mob it was called
  // for: trash on the way in, a wandering add, anything without a debuff call
  // naming it. Asking by the boss's name showed an empty board through all of
  // that, while every other panel on the card kept working.
  $: mob = card && card.status === "complete" ? card.target || "" : "";

  // The parse window is a card affordance, not an overlay one — there is no
  // room for a table on a translucent strip over the game.
  let parseOpen = false;

  // Full-scale deflection. A raid pushing past this pegs the needle, which is
  // the honest reading: past this the exact number stops changing decisions.
  const DPS_MAX = 4000;
  // Only the top quarter of the arc carries a colour. A gauge banded green
  // through red implies the middle means something, and it doesn't — raid DPS
  // has no "correct" value to sit at. Left plain, the dial answers the one
  // question colour can honestly answer: are we near the ceiling or not.
  const HOT_FROM = 0.75;
  // Labelled gradations, so the needle's position reads as a number instead of
  // a vibe.
  const TICKS = [1000, 2000, 3000];

  let data = {
    officer: false,
    mob: "",
    engaged_s: 0,
    total: 0,
    raid_dps: 0,
    top: [],
    classes: [],
  };
  let timer;
  let busy = false;

  $: hasAny = !!(data.officer && data.total > 0);

  // Semicircular gauge: 180° of arc, needle swept by raid DPS against DPS_MAX.
  // The radius leaves room for the tick labels to sit outside the arc without
  // growing the viewBox — inside it is where the needle sweeps.
  const CX = 60,
    CY = 56,
    R = 40;
  $: frac = Math.max(0, Math.min(1, (data.raid_dps || 0) / DPS_MAX));
  $: needleDeg = frac * 180;

  $: ticks = TICKS.filter((v) => v < DPS_MAX).map((v) => {
    const deg = (v / DPS_MAX) * 180;
    const [ix, iy] = polar(deg, R - 5);
    const [ox, oy] = polar(deg, R + 3);
    const [lx, ly] = polar(deg, R + 10);
    return { v, ix, iy, ox, oy, lx, ly, label: v / 1000 + "K" };
  });

  function polar(deg, r = R) {
    const rad = (Math.PI * (180 - deg)) / 180;
    return [CX + r * Math.cos(rad), CY - r * Math.sin(rad)];
  }
  function arc(fromDeg, toDeg) {
    const [x1, y1] = polar(fromDeg);
    const [x2, y2] = polar(toDeg);
    const large = toDeg - fromDeg > 180 ? 1 : 0;
    return `M ${x1} ${y1} A ${R} ${R} 0 ${large} 1 ${x2} ${y2}`;
  }

  async function poll() {
    if (busy) return;
    busy = true;
    try {
      const d = await GetRaidDPS(mob);
      if (d) data = d;
    } catch (e) {
      // Server unreachable or not an officer — keep the last values.
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    poll();
    timer = setInterval(poll, 3000);
  });
  onDestroy(() => clearInterval(timer));

  const num = (n) => Math.round(n || 0).toLocaleString();

  // Damage totals run to millions on a long raid fight; the exact digits are
  // never the point next to the share, so they're shortened.
  function short(n) {
    n = n || 0;
    if (n >= 1000000) return (n / 1000000).toFixed(n >= 10000000 ? 0 : 1) + "M";
    if (n >= 1000) return (n / 1000).toFixed(n >= 10000 ? 0 : 1) + "K";
    return String(Math.round(n));
  }

  // "shadow knight" → "Shadow Knight". Two of these aren't classes: "pets"
  // (which competes for a slot like one) and "remaining" (the folded tail,
  // holding the classes that didn't place plus anything we couldn't class at
  // all) — both capitalize the same way.
  function className(c) {
    return (c || "")
      .split(" ")
      .map((w) => (w ? w[0].toUpperCase() + w.slice(1) : w))
      .join(" ");
  }
</script>

{#if hasAny}
  <!-- One root element: this is a grid item in the card, so a bare label
       alongside .rd would be laid out as a second column. -->
  <div class="rc-col">
    {#if showLabel}<div class="rc-label">DPS</div>{/if}
    <div class="rd">
      <!-- ── left third: the gauge ── -->
      <div class="rd-third rd-gauge">
        <svg viewBox="0 0 120 68" class="rd-svg">
          <path d={arc(0, HOT_FROM * 180)} class="rd-arc" />
          <path d={arc(HOT_FROM * 180, 180)} class="rd-arc hot" />
          {#each ticks as t (t.v)}
            <line x1={t.ix} y1={t.iy} x2={t.ox} y2={t.oy} class="rd-tick" />
            <text x={t.lx} y={t.ly} class="rd-tlbl">{t.label}</text>
          {/each}
          <g class="rd-needle" style="transform: rotate({needleDeg}deg)">
            <line x1={CX} y1={CY} x2={CX - R + 8} y2={CY} />
          </g>
          <circle cx={CX} cy={CY} r="3.5" class="rd-hub" />
        </svg>
        <div class="rd-total">
          Raid DPS - <span class="rd-big">{num(data.raid_dps)}</span>
        </div>
      </div>

      <!-- ── middle third: top 5 ──
           The number shown is SDPS — damage over the whole fight — because
           that is exactly what the ranking is built on, so a row can never
           sit above another while showing a smaller number. The live 30s
           rate is a different quantity and printing it here made the list
           read as though it were sorted wrong. -->
      <div class="rd-third">
        <div class="rd-head">Top 5 DPS</div>
        {#each data.top as p (p.name)}
          <div class="rd-row" class:dead={p.dead}>
            <span class="rd-name">
              {#if p.dead}<span class="rd-tomb" title="Dead">🪦</span>{/if}{p.name}
            </span>
            <!-- Pets have no class to abbreviate, and on a charm fight one can
                 top the list — so say what it is rather than leave a blank
                 that reads like a lookup failure. -->
            <span class="rd-cls" class:pet={p.class === "pets"}>
              {p.class === "pets" ? "PET" : classAbbr(p.class) || ""}
            </span>
            <span class="rd-val" title="Damage over the whole fight">
              {num(p.sdps)}
            </span>
          </div>
        {/each}
      </div>

      <!-- ── right third: class composition of the raid's damage. The server
           names the top five (Pets competing as one of them) and folds
           everything past that — including anything it couldn't class — into
           a "remaining" row, so the percentages always add up to the whole
           raid. -->
      <div class="rd-third">
        <div class="rd-head">By Class</div>
        {#each data.classes as c (c.class)}
          <div class="rd-row" class:rest={c.class === "remaining"}>
            <span class="rd-name">{className(c.class)}</span>
            <span class="rd-val">{short(c.total)}</span>
            <span class="rd-pct">{c.pct.toFixed(0)}%</span>
          </div>
        {/each}
      </div>
    </div>
    {#if showLabel}
      <div class="rd-foot">
        <button class="rd-parse" on:click={() => (parseOpen = true)}>
          Parse ▸
        </button>
      </div>
    {/if}
  </div>
{/if}

{#if parseOpen}
  <ParseDialog {mob} onClose={() => (parseOpen = false)} />
{/if}

<style>
  .rc-col {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }
  /* Matches the card's other section headers (OtherTimers, Raiders, Loot). */
  .rc-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e3a008;
    margin-bottom: 3px;
  }
  .rd-foot {
    display: flex;
    justify-content: flex-end;
    margin-top: 2px;
  }
  .rd-parse {
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-muted);
    font-size: 10px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    padding: 2px 8px;
    cursor: pointer;
  }
  .rd-parse:hover {
    color: var(--accent);
    border-color: var(--accent);
  }
  .rd {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 10px;
    min-width: 0;
  }
  .rd-third {
    min-width: 0;
  }
  .rd-gauge {
    display: flex;
    flex-direction: column;
    align-items: center;
  }
  .rd-svg {
    width: 100%;
    max-width: 130px;
    height: auto;
  }
  .rd-arc {
    fill: none;
    stroke-width: 9;
    stroke: rgba(255, 255, 255, 0.16);
  }
  /* The only coloured stretch: the top quarter, where the raid is at its
     ceiling and the number stops being worth reading precisely. */
  .rd-arc.hot {
    stroke: #b23b3b;
    opacity: 0.85;
  }
  .rd-tick {
    stroke: rgba(255, 255, 255, 0.55);
    stroke-width: 1.2;
  }
  .rd-tlbl {
    fill: #9a9a9a;
    font-size: 7px;
    text-anchor: middle;
    dominant-baseline: middle;
    font-variant-numeric: tabular-nums;
  }
  .rd-needle {
    transform-origin: 60px 56px;
    transition: transform 0.5s ease-out;
  }
  .rd-needle line {
    stroke: #e8e8e8;
    stroke-width: 2.5;
    stroke-linecap: round;
  }
  .rd-hub {
    fill: #e8e8e8;
  }
  .rd-total {
    font-size: 0.75rem;
    color: #cfcfcf;
    white-space: nowrap;
    margin-top: 1px;
  }
  .rd-big {
    font-size: 0.95rem;
    font-weight: 700;
    color: #e8e8e8;
    font-variant-numeric: tabular-nums;
  }
  .rd-head {
    font-size: 0.68rem;
    color: #d4af37;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    white-space: nowrap;
  }
  .rd-row {
    display: flex;
    align-items: baseline;
    gap: 4px;
    font-size: 0.72rem;
    line-height: 1.4;
    min-width: 0;
  }
  .rd-row.dead .rd-name {
    color: #7a7a7a;
    text-decoration: line-through;
  }
  /* The folded tail is a summary, not a class — dimmed so the real rows read
     first. */
  .rd-row.rest .rd-name,
  .rd-row.rest .rd-val {
    color: #9a9a9a;
  }
  .rd-name {
    color: #d8d8d8;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .rd-tomb {
    margin-right: 2px;
  }
  .rd-pct {
    color: #8a8a8a;
    width: 2.6em;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .rd-cls {
    color: #7f7f7f;
    font-size: 0.66rem;
  }
  .rd-cls.pet {
    color: #6f9f6f;
  }
  .rd-val {
    margin-left: auto;
    color: #e8e8e8;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
</style>
