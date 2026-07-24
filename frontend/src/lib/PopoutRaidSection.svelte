<script>
  // Content of a Special Overlay: one section of the active raid card
  // (Assignments / Debuffs / Clerics), rendered with the same shared
  // components the Raids tab uses. Polls the timers board for the active raid;
  // shows a quiet placeholder when no raid is running.
  import { onMount, onDestroy } from "svelte";
  import {
    GetTimers,
    GetCurrentZone,
  } from "../../bindings/FuseBridge/app.js";
  import RaidAssignments from "./RaidAssignments.svelte";
  import RaidDebuffs from "./RaidDebuffs.svelte";
  import RaidClerics from "./RaidClerics.svelte";

  export let section; // "assign" | "debuffs" | "clerics"
  // Pushed up to the popout shell so "Hide when 0 triggers" can hide the title.
  export let hasContent = false;

  let card = null;
  let myZone = "";
  let pollTimer;

  function pickActive(d) {
    for (const m of (d && d.mobs) || []) {
      if (m.is_raid && m.raid && m.raid.status !== "complete") return m.raid;
    }
    return null;
  }

  async function poll() {
    try {
      card = pickActive(await GetTimers());
    } catch {
      /* keep last card */
    }
    try {
      // GetCurrentZone tracks zone-entry lines and /who — unlike the /loc
      // position, it stays correct for players who never run /loc.
      myZone = (await GetCurrentZone()) || "";
    } catch {
      /* keep last zone */
    }
  }

  // These overlays only render when the player is standing in the raid's zone.
  // Unknown raid zone (mob not in the DB) fails open; unknown player zone
  // hides — you're not in the raid zone if we can't place you in any zone.
  $: inRaidZone =
    !!card &&
    (!card.zone ||
      (myZone && myZone.toLowerCase() === card.zone.toLowerCase()));
  $: hasContent = !!card && inRaidZone;

  onMount(() => {
    poll();
    pollTimer = setInterval(poll, 5000);
  });
  onDestroy(() => clearInterval(pollTimer));
</script>

<div class="praid">
  {#if card && inRaidZone}
    {#if section === "assign"}
      <RaidAssignments {card} />
    {:else if section === "debuffs"}
      <RaidDebuffs {card} />
    {:else}
      <RaidClerics {card} />
    {/if}
  {:else}
    <div class="idle"></div>
  {/if}
</div>

<style>
  .praid {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;
    padding: 8px 10px 14px;
    display: flex;
    flex-direction: column;
  }
  .idle {
    margin: auto;
    color: var(--text-muted);
    font-size: 12px;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
</style>
