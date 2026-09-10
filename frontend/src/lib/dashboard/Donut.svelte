<script>
  // Composition of a small categorical: a donut with the total in the middle
  // and a legend that carries the counts in text (the color is the key, never
  // the only signal). slices: [{ label, count, color? }].
  import ApexChart from "./ApexChart.svelte";
  import { SURFACE, TEXT, TEXT_PRIMARY, tipHTML, fmtPct } from "./chartTheme.js";
  import { PALETTE } from "../catColor.js";

  export let slices = [];
  export let total = 0; // hover denominator; defaults to the slices' sum
  export let subject = "installs";
  export let centerLabel = "";
  export let height = 170;

  $: series = slices.map((s) => s.count);
  $: sum = series.reduce((a, b) => a + b, 0);
  $: denom = total || sum;
  $: options = {
    labels: slices.map((s) => s.label),
    colors: slices.map((s, i) => s.color || PALETTE[i % PALETTE.length]),
    legend: {
      position: "right",
      formatter: (name, o) => `${name}  ${series[o.seriesIndex] ?? 0}`,
    },
    stroke: { colors: [SURFACE], width: 2 },
    plotOptions: {
      pie: {
        expandOnClick: false,
        donut: {
          size: "64%",
          labels: {
            show: true,
            name: { show: true, fontSize: "10px", color: TEXT, offsetY: 16 },
            value: {
              show: true,
              fontSize: "16px",
              fontWeight: 700,
              color: TEXT_PRIMARY,
              offsetY: -8,
              formatter: (v) => v,
            },
            total: {
              show: true,
              showAlways: true,
              label: centerLabel || subject,
              color: TEXT,
              fontSize: "10px",
              formatter: () => String(sum),
            },
          },
        },
      },
    },
    dataLabels: { enabled: false },
    tooltip: {
      custom: ({ seriesIndex }) => {
        const s = slices[seriesIndex];
        return s
          ? tipHTML(s.label, [
              [subject, `${s.count} of ${denom} (${fmtPct(s.count, denom)})`],
            ])
          : "";
      },
    },
  };
</script>

{#if sum > 0}
  <ApexChart type="donut" {series} {options} {height} />
{:else}
  <div class="dn-empty">Nothing reported yet</div>
{/if}

<style>
  .dn-empty {
    color: var(--text-muted);
    font-size: 11px;
    font-style: italic;
    padding: 12px 0;
  }
</style>
