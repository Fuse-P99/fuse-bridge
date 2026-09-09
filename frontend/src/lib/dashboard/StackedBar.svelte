<script>
  // A 100% stacked composition bar with a legend that carries each share in
  // text. segments: [{ label, count, color? }]. Colors default to the
  // category palette so an overlay category keeps the color it has on the
  // Timers tab.
  import ApexChart from "./ApexChart.svelte";
  import { SURFACE, tipHTML, fmtPct } from "./chartTheme.js";
  import { PALETTE } from "../catColor.js";

  export let segments = [];
  export let subject = "triggers";
  export let title = "";

  $: sum = segments.reduce((a, s) => a + s.count, 0);
  $: series = segments.map((s) => ({ name: s.label, data: [s.count] }));
  $: height = 64 + Math.ceil(segments.length / 3) * 20;
  $: options = {
    chart: { stacked: true, stackType: "100%" },
    colors: segments.map((s, i) => s.color || PALETTE[i % PALETTE.length]),
    plotOptions: { bar: { horizontal: true, barHeight: "40%", borderRadius: 3 } },
    stroke: { width: 2, colors: [SURFACE] },
    fill: { opacity: 0.8 },
    xaxis: { categories: [title || subject], labels: { show: false } },
    yaxis: { labels: { show: false } },
    grid: { show: false, padding: { left: -10, right: 0, top: -12, bottom: 0 } },
    legend: {
      position: "bottom",
      horizontalAlign: "left",
      formatter: (name, o) =>
        `${name}  ${segments[o.seriesIndex] ? segments[o.seriesIndex].count : 0}`,
    },
    tooltip: {
      custom: ({ seriesIndex }) => {
        const s = segments[seriesIndex];
        return s
          ? tipHTML(s.label, [
              [subject, `${s.count} of ${sum} (${fmtPct(s.count, sum)})`],
            ])
          : "";
      },
    },
  };
</script>

{#if sum > 0}
  <ApexChart type="bar" {series} {options} {height} />
{:else}
  <div class="sb-empty">Nothing reported yet</div>
{/if}

<style>
  .sb-empty {
    color: var(--text-muted);
    font-size: 11px;
    font-style: italic;
    padding: 10px 0;
  }
</style>
