<script>
  // Content of a Special Overlay: one section of the active raid card
  // (Assignments / Debuffs / Clerics), rendered with the same shared
  // components the Raids tab uses. Polls the timers board for the active raid;
  // shows a quiet placeholder when no raid is running.
  import { onMount, onDestroy } from "svelte";
  import {
    GetTimers,
    GetCurrentZone,
    GetLocalRaidTimers,
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

  // Most recent raid card of ANY status: the linger card still on the board,
  // else the newest completed raid (the server sorts those newest-first).
  function pickFallback(d) {
    for (const m of (d && d.mobs) || []) {
      if (m.raid) return m.raid;
    }
    const done = (d && d.completed_raids) || [];
    return done.length ? done[0] : null;
  }

  // A CH chain is "running" when at least 2 DIFFERENT clerics called their CH
  // key in the last minute (locally parsed from guild chat) — proof of a live
  // raid even when the tracker has no active raid.
  const CHAIN_WINDOW_MS = 60000;
  function chainRunning(lt) {
    const cutoff = Date.now() - CHAIN_WINDOW_MS;
    const clerics = new Set();
    for (const c of (lt && lt.ch) || []) {
      if (c.at_ms >= cutoff && c.cleric) clerics.add(c.cleric.toLowerCase());
    }
    return clerics.size >= 2;
  }

  async function poll() {
    let chainLive = false;
    try {
      chainLive = chainRunning(await GetLocalRaidTimers());
    } catch {
      /* treat as no chain */
    }
    try {
      const data = await GetTimers();
      let next = pickActive(data);
      if (!next && chainLive) {
        const fb = pickFallback(data);
        // Un-complete the fallback card so the CH/debuff bars keep running —
        // the chain says the fight is still on.
        if (fb) next = { ...fb, status: "active" };
      }
      card = next;
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
  // A live card in the right zone is not enough — the SECTION has to have
  // something in it. An assignments panel with no tanks called, or a debuffs
  // panel reading "None called yet", is a translucent strip over the game
  // telling you nothing. sectionHas is reported by the section component
  // itself, so this can't drift from what actually renders.
  //
  // No reset when the card goes away: the child stops updating sectionHas,
  // but the && in front of it already forces this false.
  let sectionHas = false;
  $: hasContent = !!card && inRaidZone && sectionHas;

  onMount(() => {
    poll();
    pollTimer = setInterval(poll, 5000);
  });
  onDestroy(() => clearInterval(pollTimer));
</script>

<div class="praid">
  {#if card && inRaidZone}
    <!-- showLabel={false}: the popout's own title bar already names the
         section, so the shared component's heading would just repeat it. -->
    {#if section === "assign"}
      <RaidAssignments {card} showLabel={false} bind:hasAny={sectionHas} />
    {:else if section === "debuffs"}
      <RaidDebuffs {card} showLabel={false} bind:hasAny={sectionHas} />
    {:else}
      <RaidClerics {card} showLabel={false} bind:hasAny={sectionHas} />
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
