<script>
  // One horizontal bar per setting: the fill is the share enabled (subtle
  // gold at 0.45 over a faint full-width track), the value sits just past
  // the bar's end, and the hover carries the exact counts plus the
  // setting's own explanation. items: [{ key, label, on, n, tip, note }].
  import ApexChart from "./ApexChart.svelte";
  import {
    GOLD,
    TRACK,
    TEXT_PRIMARY,
    tipHTML,
    fmtPct,
    pct,
  } from "./chartTheme.js";

  export let items = [];
  export let subject = "installs";
  // onSelect(index): when set, bars are clickable (the Fuse-section drill-down).
  export let onSelect = null;

  $: series = [
    { name: "Enabled", data: items.map((i) => (i.n > 0 ? pct(i.on, i.n) : 0)) },
  ];
  $: height = Math.max(56, items.length * 26 + 22);
  $: options = {
    plotOptions: {
      bar: {
        horizontal: true,
        barHeight: "60%",
        borderRadius: 3,
        borderRadiusApplication: "end",
        colors: {
          backgroundBarColors: [TRACK],
          backgroundBarRadius: 3,
          backgroundBarOpacity: 1,
        },
        dataLabels: { position: "top" },
      },
    },
    colors: [GOLD],
    fill: { opacity: 0.45 },
    xaxis: {
      min: 0,
      max: 100,
      categories: items.map((i) => i.label),
      labels: { show: false },
    },
    yaxis: {
      labels: {
        style: { colors: TEXT_PRIMARY, fontSize: "11.5px" },
        maxWidth: 230,
        align: "left",
      },
    },
    grid: { show: false, padding: { right: 44, top: -6, bottom: -6 } },
    dataLabels: {
      enabled: true,
      textAnchor: "start",
      offsetX: 6,
      style: { colors: [TEXT_PRIMARY] },
      formatter: (v, { dataPointIndex }) => {
        const it = items[dataPointIndex];
        return it && it.n > 0 ? `${v}%` : "—";
      },
    },
    tooltip: {
      custom: ({ dataPointIndex }) => {
        const it = items[dataPointIndex];
        if (!it) return "";
        const rows =
          it.n > 0
            ? [
                ["Enabled", `${it.on} of ${it.n} (${fmtPct(it.on, it.n)})`],
                ["Off", String(Math.max(0, it.n - it.on))],
              ]
            : [[subject, "none reported this setting yet"]];
        return tipHTML(
          it.label,
          rows,
          [it.tip, it.note].filter(Boolean).join("\n"),
        );
      },
    },
    chart: onSelect
      ? {
          events: {
            dataPointSelection: (e, ctx, cfg) => onSelect(cfg.dataPointIndex),
          },
        }
      : {},
    states: onSelect
      ? { active: { filter: { type: "lighten", value: 0.18 } } }
      : {},
  };
</script>

<ApexChart type="bar" {series} {options} {height} />
