<script>
  // Thin Svelte wrapper around an ApexCharts instance: renders once on mount,
  // pushes prop changes through updateOptions, destroys on unmount. Every
  // chart on the dashboard is one of these with a recipe from the sibling
  // components (PercentBars, Donut, Histogram, RankBars, StackedBar).
  //
  // Hovers go through the app's own tooltip (lib/tooltip.js), not the
  // library's: ApexCharts draws its tooltip inside the chart box, so on the
  // top row or the leftmost bar it ran past the panel's edge and the scroll
  // container clipped it. The app tip is appended to document.body, anchored
  // to the cursor and clamped to the viewport — the same one every title=
  // in the app uses. The library still decides WHAT is hovered: its
  // tooltip.custom callback fires with the right series/point for every
  // chart type, so that is the hook; its own box is hidden by CSS.
  import { onMount, onDestroy } from "svelte";
  import ApexCharts from "apexcharts";
  import { baseOptions, deepMerge } from "./chartTheme.js";
  import { tipShow, tipMove, tipHide, tipVisible } from "../tooltip.js";

  export let type = "bar";
  export let series = [];
  export let options = {};
  export let height = 200;
  // tip(seriesIndex, dataPointIndex) → tooltip text (chartTheme.js tipText),
  // or "" for nothing. Without it the chart has no hover.
  export let tip = null;

  let el;
  let chart = null;
  let mx = 0;
  let my = 0;
  let shown = ""; // text currently up, so a same-point move only repositions

  function onMove(e) {
    mx = e.clientX;
    my = e.clientY;
    if (shown) tipMove(mx, my);
  }
  function onLeave() {
    shown = "";
    tipHide();
  }
  function hover({ seriesIndex, dataPointIndex }) {
    const text = (tip && tip(seriesIndex, dataPointIndex)) || "";
    // Re-show when the point changed OR the tip was taken down under us (a
    // scroll or click hides it; the library won't call again until the
    // cursor moves onto a point, which is exactly when this runs).
    if (text !== shown || (text && !tipVisible())) {
      shown = text;
      if (text) tipShow(text, mx, my);
      else tipHide();
    }
    return "";
  }

  $: full = deepMerge(
    baseOptions(),
    options,
    { chart: { type, height }, series },
    tip ? { tooltip: { enabled: true, custom: hover } } : {},
  );

  onMount(() => {
    chart = new ApexCharts(el, full);
    chart.render();
  });
  // Any prop change re-applies the full option set (cheap for charts this
  // size, and it keeps series/categories/tooltips in step with each other).
  $: if (chart) chart.updateOptions(full, false, true);
  onDestroy(() => {
    onLeave();
    if (chart) {
      chart.destroy();
      chart = null;
    }
  });
</script>

<!-- Capture phase, so the cursor is current before the library's own
     listener (deeper in the SVG) asks for the hover. -->
<div
  bind:this={el}
  class="apx"
  role="presentation"
  style="min-height:{height}px"
  on:mousemove|capture={onMove}
  on:mouseleave={onLeave}
></div>

<style>
  .apx {
    width: 100%;
  }
  /* The library's tooltip box: emptied by hover() and hidden here; the app
     tip stands in for it. */
  .apx :global(.apexcharts-tooltip) {
    display: none !important;
  }
</style>
