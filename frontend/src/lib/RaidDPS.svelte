<script>
  import { onMount, onDestroy } from "svelte";
  import { GetRaidDPS } from "../../bindings/FuseBridge/app.js";
  import { classAbbr } from "./classAbbr.js";

  // hasAny lets the card hide the whole row when nothing is being fought —
  // same contract as OtherTimers.
  export let hasAny = false;

  // Full-scale deflection. A raid pushing past this pegs the needle, which is
  // the honest reading: past 2k the exact number stops changing decisions.
  const DPS_MAX = 2000;

  let data = {
    officer: false,
    mob: "",
    engaged_s: 0,
    total: 0,
    raid_dps: 0,
    top: [],
    classes: [],
    threat: [],
  };
  let timer;
  let busy = false;

  $: hasAny = !!(data.officer && data.total > 0);

  // Semicircular gauge: 180° of arc, needle swept by raid DPS against DPS_MAX.
  const CX = 60,
    CY = 56,
    R = 46;
  $: frac = Math.max(0, Math.min(1, (data.raid_dps || 0) / DPS_MAX));
  $: needleDeg = frac * 180;

  function polar(deg) {
    const rad = (Math.PI * (180 - deg)) / 180;
    return [CX + R * Math.cos(rad), CY - R * Math.sin(rad)];
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
      const d = await GetRaidDPS();
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
</script>

{#if hasAny}
  <div class="rd">
    <!-- ── left third: the gauge ── -->
    <div class="rd-third rd-gauge">
      <svg viewBox="0 0 120 68" class="rd-svg">
        <path d={arc(0, 60)} class="rd-arc green" />
        <path d={arc(60, 120)} class="rd-arc yellow" />
        <path d={arc(120, 180)} class="rd-arc red" />
        <g class="rd-needle" style="transform: rotate({needleDeg}deg)">
          <line x1={CX} y1={CY} x2={CX - R + 8} y2={CY} />
        </g>
        <circle cx={CX} cy={CY} r="3.5" class="rd-hub" />
      </svg>
      <div class="rd-total">
        Raid DPS - <span class="rd-big">{num(data.raid_dps)}</span>
      </div>
    </div>

    <!-- ── middle third: top 5 by live DPS ── -->
    <div class="rd-third">
      <div class="rd-head">Top 5 DPS</div>
      {#each data.top as p (p.name)}
        <div class="rd-row" class:dead={p.dead}>
          <span class="rd-name">
            {#if p.dead}<span class="rd-tomb" title="Dead">🪦</span>{/if}{p.name}
          </span>
          <span class="rd-cls">{classAbbr(p.class) || ""}</span>
          <span class="rd-val">{num(p.dps)}</span>
        </div>
      {/each}
    </div>

    <!-- ── right third: estimated threat, main tank always present ── -->
    <div class="rd-third">
      <div class="rd-head">
        Top 5 Threat <span class="rd-est">(est.)</span>
      </div>
      {#each data.threat as t (t.name)}
        <div class="rd-row" class:dead={t.dead} class:tank={t.is_tank}>
          <span class="rd-rank">{t.rank ? t.rank + "." : "—"}</span>
          <span class="rd-name">
            {#if t.dead}<span class="rd-tomb" title="Dead">🪦</span>{/if}{t.name}{#if t.is_tank}<span
                class="rd-mt"
                title="Main tank">MT</span
              >{/if}
          </span>
          <span class="rd-val">
            {#if t.rank}{num(t.threat)}{:else}<span class="rd-none"
                >not relaying</span
              >{/if}
          </span>
        </div>
      {/each}
    </div>
  </div>
{/if}

<style>
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
    opacity: 0.85;
  }
  .rd-arc.green {
    stroke: #3fa34d;
  }
  .rd-arc.yellow {
    stroke: #c9a227;
  }
  .rd-arc.red {
    stroke: #b23b3b;
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
  .rd-est {
    color: #8a8a8a;
    font-weight: 400;
    text-transform: none;
    letter-spacing: 0;
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
  .rd-row.tank {
    color: #e8e8e8;
  }
  .rd-rank {
    color: #8a8a8a;
    width: 1.6em;
    text-align: right;
    font-variant-numeric: tabular-nums;
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
  .rd-mt {
    margin-left: 3px;
    font-size: 0.6rem;
    color: #d4af37;
    font-weight: 700;
  }
  .rd-cls {
    color: #7f7f7f;
    font-size: 0.66rem;
  }
  .rd-val {
    margin-left: auto;
    color: #e8e8e8;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .rd-none {
    color: #8a8a8a;
    font-size: 0.66rem;
  }
</style>
