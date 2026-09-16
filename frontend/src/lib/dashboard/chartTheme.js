// Shared ApexCharts theme for the officer settings dashboard — the app's
// tokens (style.css) and palette (lib/catColor.js) expressed as chart
// options, so every chart on the page reads as part of the app rather than
// a library default. ApexCharts renders SVG, which matters here: the shell is
// CSS-zoomed (App.svelte), and canvas hit-testing drifts under zoom.
import { PALETTE } from "../catColor.js";

export const GOLD = "#c8a951";
export const TRACK = "rgba(255,255,255,0.06)";
export const SURFACE = "#1a1d2e"; // --bg-panel: the gap color between segments
export const TEXT = "#7880a0"; // --text-secondary
export const TEXT_PRIMARY = "#e0e2ea";
export const TEXT_MUTED = "#4d5270";
export const BORDER = "#252836";
export const FONT =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif';

export function baseOptions() {
  return {
    chart: {
      background: "transparent",
      toolbar: { show: false },
      zoom: { enabled: false },
      fontFamily: FONT,
      foreColor: TEXT,
      parentHeightOffset: 0,
      redrawOnParentResize: true,
      animations: {
        enabled: true,
        easing: "easeout",
        speed: 450,
        animateGradually: { enabled: false },
        dynamicAnimation: { enabled: true, speed: 300 },
      },
    },
    theme: { mode: "dark" },
    colors: [GOLD, ...PALETTE.slice(1)],
    grid: { show: false, padding: { top: 0, right: 8, bottom: 0, left: 0 } },
    stroke: { width: 0 },
    states: {
      hover: { filter: { type: "lighten", value: 0.06 } },
      active: {
        allowMultipleDataPointsSelection: false,
        filter: { type: "none" },
      },
    },
    legend: {
      labels: { colors: TEXT },
      fontSize: "11px",
      fontFamily: FONT,
      markers: { size: 4, shape: "square", strokeWidth: 0 },
      itemMargin: { horizontal: 8, vertical: 2 },
    },
    dataLabels: {
      enabled: false,
      style: { fontSize: "11px", fontWeight: 600, colors: [TEXT_PRIMARY] },
      dropShadow: { enabled: false },
    },
    tooltip: {
      theme: "dark",
      followCursor: false,
      marker: { show: false },
      fillSeriesColor: false,
    },
    xaxis: {
      axisBorder: { show: false },
      axisTicks: { show: false },
      tooltip: { enabled: false },
      labels: { style: { colors: TEXT, fontSize: "10.5px" } },
    },
    yaxis: { labels: { style: { colors: TEXT, fontSize: "11px" } } },
  };
}

export function pct(on, n) {
  return n > 0 ? Math.round((on / n) * 100) : 0;
}
export function fmtPct(on, n) {
  return n > 0 ? pct(on, n) + "%" : "—";
}

// tipText builds a chart hover in the app tooltip's own markup (lib/tooltip.js
// — rendered from text nodes, never HTML): a gold title, a label/value grid,
// an optional note. Labels come from server data (trigger names are
// user-authored), so the one sequence that means something there, "**", is
// dropped from them.
const plain = (s) => String(s ?? "").replace(/\*\*/g, "");
export function tipText(title, rows = [], note = "") {
  const lines = [`**${plain(title)}**`];
  for (const r of rows) {
    if (r && r.length === 2) lines.push(`|${plain(r[0])}|${plain(r[1])}`);
  }
  if (note) lines.push(plain(note));
  return lines.join("\n");
}

// deepMerge(a, b, …): plain objects merge recursively, everything else
// (arrays, functions, primitives) is replaced by the later value.
const isObj = (v) =>
  v && typeof v === "object" && !Array.isArray(v) && !(v instanceof Date);
export function deepMerge(...objs) {
  const out = {};
  for (const o of objs) {
    if (!isObj(o)) continue;
    for (const [k, v] of Object.entries(o)) {
      out[k] = isObj(v) && isObj(out[k]) ? deepMerge(out[k], v) : v;
    }
  }
  return out;
}
