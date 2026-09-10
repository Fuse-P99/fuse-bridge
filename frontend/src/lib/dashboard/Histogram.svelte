<script>
  // A distribution over fixed buckets as gold columns (one hue — this is
  // magnitude), each direct-labeled with its count. buckets: [{ label, count }].
  import ApexChart from "./ApexChart.svelte";
  import { GOLD, TEXT_PRIMARY, tipHTML, fmtPct } from "./chartTheme.js";

  export let buckets = [];
  export let total = 0;
  export let subject = "installs";
  export let height = 150;

  $: series = [{ name: subject, data: buckets.map((b) => b.count) }];
  $: options = {
    plotOptions: {
      bar: {
        columnWidth: "58%",
        borderRadius: 3,
        borderRadiusApplication: "end",
        dataLabels: { position: "top" },
      },
    },
    colors: [GOLD],
    fill: { opacity: 0.55 },
    xaxis: { categories: buckets.map((b) => b.label) },
    yaxis: { show: false, min: 0, forceNiceScale: true },
    grid: { show: false, padding: { top: 14, left: 0, right: 0, bottom: 0 } },
    dataLabels: {
      enabled: true,
      offsetY: -18,
      style: { colors: [TEXT_PRIMARY], fontSize: "11px" },
      formatter: (v) => (v ? v : ""),
    },
    tooltip: {
      custom: ({ dataPointIndex }) => {
        const b = buckets[dataPointIndex];
        return b
          ? tipHTML(b.label, [
              [subject, `${b.count} of ${total} (${fmtPct(b.count, total)})`],
            ])
          : "";
      },
    },
  };
</script>

<ApexChart type="bar" {series} {options} {height} />
