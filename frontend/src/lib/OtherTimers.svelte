<script>
  // "Other Timers" — event/raid countdown bars sourced from the local trigger
  // engine, filtered by the shared-package folder that owns them. Today that
  // means the Ring War waves + Narandi timers on the event raid card; the
  // filter table is deliberately data-driven so mob AE timers for standard
  // raids can join later without touching the plumbing.
  //
  // Used two ways:
  //   - inside RaidCardView (card passed in): filters by the card's event_key.
  //   - as the "Other Timers" special overlay (no card): polls GetTimers to
  //     find the live event raid and uses its key.
  // Hidden entirely (hasAny=false) when nothing matches — an empty timer
  // panel is dead space on the card and a translucent nothing as an overlay.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { GetTriggerState, GetTimers } from "../../bindings/FuseBridge/app.js";

  export let card = null;
  export let showLabel = true;
  export let hasAny = false;

  // Folder filters per event key. Each entry is a list of alternatives; an
  // alternative is a chain of lowercase substrings that must appear, in
  // order, across the timer's folder path. ("5 - Ring War" matches
  // ["ring war"].)
  const FOLDER_FILTERS = {
    ringwar: [["ring war"]],
    // Future: sky/hot island- or wing-specific timer folders, and mob-keyed
    // AE folders for standard raids.
  };

  let timers = [];
  let now = Date.now();
  let liveEventKey = ""; // popout mode: from GetTimers().event_raid
  let pollTimer, dataTimer, animReq, offTriggers;
  let polling = false,
    pollAgain = false;

  $: filterKey = card ? card.event_key || "" : liveEventKey;
  $: chains = FOLDER_FILTERS[filterKey] || [];

  function pathMatches(path, chain) {
    const parts = (path || []).map((p) => (p || "").toLowerCase());
    let i = 0;
    for (const want of chain) {
      for (; i < parts.length; i++) {
        if (parts[i].includes(want)) break;
      }
      if (i >= parts.length) return false;
      i++;
    }
    return true;
  }

  $: active = chains.length
    ? timers
        .filter(
          (t) =>
            t.ends_at_ms > now && chains.some((c) => pathMatches(t.path, c)),
        )
        .sort((a, b) => a.ends_at_ms - b.ends_at_ms)
    : [];
  $: hasAny = active.length > 0;

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

  // Popout mode only: resolve which event raid is live (its key picks the
  // folder filter). The card path skips this — the parent owns the card.
  async function pollContext() {
    if (card) return;
    try {
      const d = await GetTimers();
      liveEventKey = (d && d.event_raid && d.event_raid.event_key) || "";
    } catch {
      /* keep last */
    }
  }

  function animLoop() {
    now = Date.now();
    animReq = requestAnimationFrame(animLoop);
  }

  onMount(async () => {
    await poll();
    await pollContext();
    offTriggers = Events.On("triggers-changed", poll);
    pollTimer = setInterval(poll, 1000);
    if (!card) dataTimer = setInterval(pollContext, 5000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (dataTimer) clearInterval(dataTimer);
    if (offTriggers) offTriggers();
    if (animReq) cancelAnimationFrame(animReq);
  });
</script>

<div class="rc-col">
  {#if showLabel && hasAny}<div class="rc-label">Other Timers</div>{/if}
  {#each active as t (t.id)}
    <div class="obar">
      <div class="obar-fill" style="width:{barFrac(t) * 100}%"></div>
      <span class="obar-name">{t.name}</span>
      <span class="obar-time">{fmtRemain(t.ends_at_ms - now)}</span>
    </div>
  {/each}
</div>

<style>
  .rc-col {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }
  .rc-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e3a008;
    margin-bottom: 3px;
  }
  /* Same bar anatomy as the trigger timer overlays (PopoutTimers), fixed
     height and accent color — these bars live inside the raid card, not a
     styleable overlay category. */
  .obar {
    position: relative;
    height: 20px;
    border-radius: 4px;
    overflow: hidden;
    background: rgba(255, 255, 255, 0.05);
  }
  .obar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    background: rgba(79, 179, 169, 0.55);
  }
  .obar-name,
  .obar-time {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.7);
    white-space: nowrap;
  }
  .obar-name {
    left: 8px;
    max-width: 68%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .obar-time {
    right: 8px;
    font-variant-numeric: tabular-nums;
    font-family: var(--font-mono);
  }
</style>
