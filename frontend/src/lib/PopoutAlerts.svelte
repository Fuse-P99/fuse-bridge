<script>
  import { onMount, onDestroy } from "svelte";
  import { fly } from "svelte/transition";
  import { Events } from "@wailsio/runtime";
  import {
    GetTriggerState,
    GetCategoryStyle,
  } from "../../bindings/FuseBridge/app.js";
  import { catColor, rgba } from "./catColor.js";

  export let category = "Default";

  // Look configured on the Manage Overlays page; palette hash until it loads.
  let style = null;
  $: color = style?.font_color || catColor(category);
  $: alertBg = style ? rgba(style.bg_color, style.bg_opacity) : "transparent";
  $: fontSize = (style?.font_size || 16) + "px";
  $: fontFamily = style?.font_family || "inherit";

  // How long an alert stays on screen. The Go side keeps a longer history so a
  // slow poll can't drop one; the overlay decides what's still worth showing.
  const SHOW_MS = 10000;
  const MAX_SHOWN = 6;

  let alerts = [];
  let now = Date.now();
  let pollTimer, animReq, offTriggers;
  let polling = false,
    pollAgain = false;

  // Newest first, this category only, still inside its display window.
  $: shown = alerts
    .filter(
      (a) => (a.category || "Default") === category && now - a.at_ms < SHOW_MS,
    )
    .sort((a, b) => b.at_ms - a.at_ms)
    .slice(0, MAX_SHOWN);

  // Fade each alert out over the last third of its life.
  function alertOpacity(a) {
    const age = now - a.at_ms;
    const fadeFrom = SHOW_MS * 0.66;
    if (age <= fadeFrom) return 1;
    return Math.max(0, 1 - (age - fadeFrom) / (SHOW_MS - fadeFrom));
  }

  async function poll() {
    if (polling) {
      pollAgain = true;
      return;
    }
    polling = true;
    try {
      const s = await GetTriggerState();
      alerts = s.alerts || [];
      // Same beat as the data, so a style edit shows within a second.
      style = await GetCategoryStyle("alerts", category);
    } catch {
      /* keep last */
    }
    polling = false;
    if (pollAgain) {
      pollAgain = false;
      poll();
    }
  }

  function animLoop() {
    now = Date.now();
    animReq = requestAnimationFrame(animLoop);
  }

  onMount(async () => {
    await poll();
    // Push: an alert must appear the instant it fires — the interval is only a
    // safety net, and is also what drives expiry when nothing is firing.
    offTriggers = Events.On("triggers-changed", poll);
    pollTimer = setInterval(poll, 1000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (offTriggers) offTriggers();
    if (animReq) cancelAnimationFrame(animReq);
  });
</script>

<div class="palerts">
  {#each shown as a (a.id)}
    <div
      class="alert"
      style="color:{color}; opacity:{alertOpacity(a)}; background:{alertBg};
             font-size:{fontSize}; font-family:{fontFamily}"
      transition:fly|local={{ y: -6, duration: 160 }}
    >
      {a.text}
    </div>
  {/each}
</div>

<style>
  /* Newest at the top; nothing is drawn when the category is quiet, so the
     overlay is invisible over the game until something fires. */
  .palerts {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 5px 8px 14px;
    overflow: hidden;
  }
  /* Color, size, family, and backdrop come from the category style, inline. */
  .alert {
    font-weight: 700;
    line-height: 1.25;
    text-align: center;
    border-radius: 4px;
    padding: 0 6px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    /* Heavy shadow: alert text sits directly on the game with no backdrop. */
    text-shadow:
      0 1px 2px rgba(0, 0, 0, 0.95),
      0 0 4px rgba(0, 0, 0, 0.8),
      0 0 8px rgba(0, 0, 0, 0.5);
  }
</style>
