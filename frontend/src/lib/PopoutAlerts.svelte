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
  // Pushed up to the popout shell so "Hide when 0 triggers" can hide the title.
  export let hasContent = false;

  // Look configured on the Manage Overlays page; palette hash until it loads.
  let style = null;
  $: color = style?.font_color || catColor(category);
  $: alertBg = style ? rgba(style.bg_color, style.bg_opacity) : "transparent";
  $: fontSize = (style?.font_size || 16) + "px";
  $: fontFamily = style?.font_family || "inherit";

  // How long an alert stays on screen — the overlay's "Time Shown" setting
  // (seconds; default the classic 10). A long window turns this overlay into a
  // standing to-do list: buff requests sit until their early-end condition
  // clears them. The Go side retains history at least this long, so a slow
  // poll can't drop one.
  $: showMs = (style?.alert_seconds || 10) * 1000;
  // "Include Stopwatch": a ticking m:ss age on each line.
  $: stopwatch = !!style?.alert_stopwatch;
  // Sanity bound only — the window's own height (overflow: hidden) is the
  // real display cap, so sizing the overlay decides how many lines show.
  const MAX_SHOWN = 30;
  // A cleared alert (an early-end condition matched) triple-flashes, then
  // fades out slowly enough to stay readable: three 0.5s pulses + a 2.5s fade
  // (see .alert.cleared). Per-overlay "Flash if Ended Early" turns the whole
  // exit off — cleared alerts just disappear.
  const CLEAR_TOTAL_MS = 4000;
  $: flashCleared = style ? style.flash_ended_early !== false : true;

  let alerts = [];
  let now = Date.now();
  let pollTimer, animReq, offTriggers;
  let polling = false,
    pollAgain = false;

  // Newest first, this category only: live ones inside their display window,
  // cleared ones only while their flash-and-fade plays (and only if this
  // overlay wants it).
  $: shown = alerts
    .filter(
      (a) =>
        (a.category || "Default") === category &&
        (a.cleared_at_ms
          ? flashCleared && now - a.cleared_at_ms < CLEAR_TOTAL_MS
          : now - a.at_ms < showMs),
    )
    .sort((a, b) => b.at_ms - a.at_ms)
    .slice(0, MAX_SHOWN);
  $: hasContent = shown.length > 0;

  // Fade each alert out over the last third of its life. A cleared alert
  // holds full strength — its flash is the exit.
  function alertOpacity(a) {
    if (a.cleared_at_ms) return 1;
    const age = now - a.at_ms;
    const fadeFrom = showMs * 0.66;
    if (age <= fadeFrom) return 1;
    return Math.max(0, 1 - (age - fadeFrom) / (showMs - fadeFrom));
  }

  // m:ss for the stopwatch readout.
  function fmtSw(ms) {
    const s = Math.max(0, Math.floor(ms / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
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
    <!-- a.color: a per-trigger tint from the customization layer wins over
         the overlay's font color. -->
    <div
      class="alert"
      class:cleared={!!a.cleared_at_ms}
      style="color:{a.color || color}; opacity:{alertOpacity(a)}; background:{alertBg};
             font-size:{fontSize}; font-family:{fontFamily}"
      transition:fly|local={{ y: -6, duration: 160 }}
    >
      {a.text}{#if stopwatch && !a.cleared_at_ms}<span class="sw"
          >{fmtSw(now - a.at_ms)}</span
        >{/if}
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
    /* Long alerts wrap onto more lines rather than truncating. */
    white-space: normal;
    overflow-wrap: break-word;
    min-width: 0;
    /* Heavy shadow: alert text sits directly on the game with no backdrop. */
    text-shadow:
      0 1px 2px rgba(0, 0, 0, 0.95),
      0 0 4px rgba(0, 0, 0, 0.8),
      0 0 8px rgba(0, 0, 0, 0.5);
  }
  /* An early-end condition answered this alert: three quick pulses, then a
     slow fade — long enough to read what was answered before it goes. */
  .alert.cleared {
    animation:
      clearflash 0.5s ease-in-out 3,
      clearfade 2.5s linear 1.5s forwards;
  }
  @keyframes clearflash {
    0%,
    100% {
      filter: brightness(1);
    }
    50% {
      filter: brightness(2.2);
    }
  }
  @keyframes clearfade {
    to {
      opacity: 0;
    }
  }
  /* The "Include Stopwatch" age readout, subordinate to the alert text. */
  .sw {
    margin-left: 6px;
    font-size: 0.72em;
    font-weight: 600;
    opacity: 0.85;
    font-variant-numeric: tabular-nums;
    font-family: var(--font-mono);
  }
</style>
