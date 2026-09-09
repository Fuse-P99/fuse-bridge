<script>
  // Thin Svelte wrapper around an ApexCharts instance: renders once on mount,
  // pushes prop changes through updateOptions, destroys on unmount. Every
  // chart on the dashboard is one of these with a recipe from the sibling
  // components (PercentBars, Donut, Histogram, RankBars, StackedBar).
  import { onMount, onDestroy } from "svelte";
  import ApexCharts from "apexcharts";
  import { baseOptions, deepMerge } from "./chartTheme.js";

  export let type = "bar";
  export let series = [];
  export let options = {};
  export let height = 200;

  let el;
  let chart = null;

  onMount(() => {
    chart = new ApexCharts(
      el,
      deepMerge(baseOptions(), options, { chart: { type, height }, series }),
    );
    chart.render();
  });
  // Any prop change re-applies the full option set (cheap for charts this
  // size, and it keeps series/categories/tooltips in step with each other).
  $: if (chart)
    chart.updateOptions(
      deepMerge(baseOptions(), options, { chart: { type, height }, series }),
      false,
      true,
    );
  onDestroy(() => {
    if (chart) {
      chart.destroy();
      chart = null;
    }
  });
</script>

<div bind:this={el} class="apx" style="min-height:{height}px"></div>

<style>
  .apx {
    width: 100%;
  }
</style>
