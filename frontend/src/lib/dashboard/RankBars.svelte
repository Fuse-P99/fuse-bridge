<script>
  // A ranked list as horizontal bars, longest first (the server sorts), with
  // the count past each bar's end. rows: [{ label, value, sub }] — `sub` is
  // the full trigger path, shown only in the hover.
  import ApexChart from "./ApexChart.svelte";
  import { GOLD, TEXT_PRIMARY, tipText, fmtPct } from "./chartTheme.js";

  export let rows = [];
  export let total = 0;
  export let subject = "installs";
  export let empty = "Nothing yet";

  $: series = [{ name: subject, data: rows.map((r) => r.value) }];
  $: height = Math.max(56, rows.length * 24 + 22);
  $: options = {
    plotOptions: {
      bar: {
        horizontal: true,
        barHeight: "58%",
        borderRadius: 3,
        borderRadiusApplication: "end",
        dataLabels: { position: "top" },
      },
    },
    colors: [GOLD],
    fill: { opacity: 0.55 },
    xaxis: {
      categories: rows.map((r) => r.label),
      labels: { show: false },
      min: 0,
    },
    yaxis: {
      labels: {
        style: { colors: TEXT_PRIMARY, fontSize: "11px" },
        maxWidth: 220,
        align: "left",
      },
    },
    grid: { show: false, padding: { right: 36, top: -6, bottom: -6 } },
    dataLabels: {
      enabled: true,
      textAnchor: "start",
      offsetX: 6,
      style: { colors: [TEXT_PRIMARY] },
      formatter: (v) => v,
    },
  };
  function tip(_si, di) {
    const r = rows[di];
    if (!r) return "";
    const rr = [[subject, `${r.value} of ${total} (${fmtPct(r.value, total)})`]];
    if (r.sub) rr.push(["Where", r.sub]);
    return tipText(r.label, rr);
  }
</script>

{#if rows.length}
  <ApexChart type="bar" {series} {options} {height} {tip} />
{:else}
  <div class="rb-empty">{empty}</div>
{/if}

<style>
  .rb-empty {
    color: var(--text-muted);
    font-size: 11px;
    font-style: italic;
    padding: 10px 0;
  }
</style>
