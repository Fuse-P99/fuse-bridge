<script>
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    GetTriggerState,
    DismissTimer,
    GetCategoryStyle,
  } from "../../bindings/FuseBridge/app.js";
  import { catColor, rgba } from "./catColor.js";

  export let category = "Default";

  // Look configured on the Manage Overlays page. Until it loads, fall back to
  // the palette hash so the bars never flash an unstyled color.
  let style = null;
  $: color = style?.bar_color || catColor(category);
  $: fillOpacity = style ? style.bar_opacity : 0.82;
  $: trackBg = style ? rgba(style.bg_color, style.bg_opacity) : "transparent";
  $: fontColor = style?.font_color || "#fff";
  $: fontSize = (style?.font_size || 12) + "px";
  $: fontFamily = style?.font_family || "inherit";
  // The countdown stays monospaced unless a font was chosen, so the digits
  // don't jitter as they tick down.
  $: timeFamily = style?.font_family || "var(--font-mono)";
  // Bars are capped so they don't stretch to fill a tall window; the cap has to
  // clear the text or a larger font size gets clipped.
  $: barHeight = Math.max(20, Math.round((style?.font_size || 12) * 1.7));

  let timers = [];
  let now = Date.now();
  let pollTimer, animReq, offTriggers;
  // Overlap guard: the engine pushes "triggers-changed" the instant a trigger
  // fires, so polls can arrive faster than they complete. If one is in flight,
  // remember to re-poll after it so we never settle on stale state.
  let polling = false,
    pollAgain = false;

  // Timers for this category only, soonest-ending first.
  $: active = timers
    .filter((t) => (t.category || "Default") === category && t.ends_at_ms > now)
    .sort((a, b) => a.ends_at_ms - b.ends_at_ms);

  function fmtRemain(ms) {
    const s = Math.max(0, Math.ceil(ms / 1000));
    const m = Math.floor(s / 60);
    return `${String(m).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
  }
  function barFrac(t) {
    const total = t.ends_at_ms - t.started_at_ms;
    if (total <= 0) return 0;
    return Math.max(0, Math.min(1, (t.ends_at_ms - now) / total));
  }

  async function poll() {
    if (polling) {
      pollAgain = true;
      return;
    }
    polling = true;
    try {
      const s = await GetTriggerState();
      timers = s.timers || [];
      // Picked up on the same beat, so a style edit in Manage Overlays shows
      // here within a second without any extra plumbing.
      style = await GetCategoryStyle("timers", category);
    } catch {
      /* keep last */
    }
    polling = false;
    if (pollAgain) {
      pollAgain = false;
      poll();
    }
  }

  async function dismiss(t) {
    timers = timers.filter((x) => x.id !== t.id); // optimistic
    try {
      await DismissTimer(t.id);
    } catch {
      /* poll will re-sync */
    }
  }

  function animLoop() {
    now = Date.now();
    animReq = requestAnimationFrame(animLoop);
  }

  onMount(async () => {
    await poll();
    // Push: refresh the instant a trigger fires. The interval stays as a safety
    // net (missed event, or a change with no event).
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

<div class="ptimers">
  {#if active.length === 0}
    <!-- <div class="idle">No active timers</div> -->
  {:else}
    {#each active as t (t.id)}
      <div
        class="tbar"
        style="background:{trackBg}; color:{fontColor}; font-size:{fontSize};
               font-family:{fontFamily}; max-height:{barHeight}px"
      >
        <div
          class="tbar-fill"
          style="width:{barFrac(t) * 100}%; background:{color}; opacity:{fillOpacity}"
        ></div>
        <span class="tbar-name">{t.name}</span>
        <span class="tbar-time" style="font-family:{timeFamily}"
          >{fmtRemain(t.ends_at_ms - now)}</span
        >
        <button
          class="tbar-trash"
          title="Dismiss this timer"
          aria-label="Dismiss timer"
          on:click={() => dismiss(t)}
        >
          <svg
            viewBox="0 0 24 24"
            width="12"
            height="12"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" />
          </svg>
        </button>
      </div>
    {/each}
  {/if}
</div>

<style>
  /* Bars share the available height and shrink vertically as the window gets
     smaller, so every running timer stays visible. */
  .ptimers {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 5px 6px 14px;
    overflow: hidden;
  }
  .tbar {
    position: relative;
    flex: 1 1 0;
    min-height: 12px;
    border-radius: 4px;
    overflow: hidden;
    /* background / max-height / font come from the category style, inline. */
  }
  .tbar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
  }
  /* Color, size, and family are inherited from .tbar, which carries the
     category's configured style. */
  .tbar-name,
  .tbar-time {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    font-weight: 600;
    text-shadow:
      0 1px 2px rgba(0, 0, 0, 0.9),
      0 0 3px rgba(0, 0, 0, 0.7);
    white-space: nowrap;
  }
  .tbar-name {
    left: 8px;
    max-width: 68%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tbar-time {
    right: 8px;
    transition: right 0.12s;
  }
  .tbar:hover .tbar-time {
    right: 28px;
  }
  .tbar-trash {
    position: absolute;
    right: 5px;
    top: 50%;
    transform: translateY(-50%);
    background: none;
    border: none;
    color: #fff;
    cursor: pointer;
    padding: 1px;
    display: inline-flex;
    align-items: center;
    opacity: 0;
    transition: opacity 0.12s;
    filter: drop-shadow(0 1px 1px rgba(0, 0, 0, 0.85));
  }
  .tbar:hover .tbar-trash {
    opacity: 1;
  }
  .tbar-trash:hover {
    color: #ff8a8a;
  }
  .idle {
    margin: auto;
    color: var(--text-muted);
    font-size: 12px;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
</style>
