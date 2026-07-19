<script>
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    GetTriggerState,
    DismissTimer,
  } from "../../bindings/FuseBridge/app.js";
  import { catColor } from "./catColor.js";

  export let category = "Default";

  let timers = [];
  let now = Date.now();
  let pollTimer, animReq, offTriggers;
  // Overlap guard: the engine pushes "triggers-changed" the instant a trigger
  // fires, so polls can arrive faster than they complete. If one is in flight,
  // remember to re-poll after it so we never settle on stale state.
  let polling = false,
    pollAgain = false;

  $: color = catColor(category);
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
      <div class="tbar">
        <div
          class="tbar-fill"
          style="width:{barFrac(t) * 100}%; background:{color}"
        ></div>
        <span class="tbar-name">{t.name}</span>
        <span class="tbar-time">{fmtRemain(t.ends_at_ms - now)}</span>
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
    /* Empty portion is fully transparent — only the fill shows over the game. */
    background: transparent;
    border-radius: 4px;
    overflow: hidden;
    max-height: 20px;
  }
  .tbar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    opacity: 0.82;
  }
  .tbar-name,
  .tbar-time {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    font-size: 11.5px;
    font-weight: 600;
    color: #fff;
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
    font-family: var(--font-mono);
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
