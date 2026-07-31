<script>
  // Content of a Special Overlay: one section of the active raid card
  // (Assignments / Debuffs / Clerics), rendered with the same shared
  // components the Raids tab uses. Polls the timers board for the active raid;
  // shows a quiet placeholder when no raid is running.
  //
  // Two sources, and only two:
  //   1. the active raid card — a real, mob-anchored raid;
  //   2. the server's GHOST raid (ghost_raid) — raid chat with no identified
  //      target. This is what makes these overlays work during events with no
  //      single boss (Plane of Sky, Halls of Testing, Ring War), before a
  //      batphone lands, and while clearing trash after a kill. It carries a
  //      zone like a real card, so the in-zone gate below applies unchanged.
  //
  // Deliberately NOT a source: the lingering recap of a just-completed raid.
  // These overlays sit on top of the game and must describe the fight in front
  // of you; a dead boss's frozen tank list and chain is worse than an empty
  // panel. The recap belongs on the Raids tab, which keeps it for its five
  // minutes. When a chain is still rolling after a kill, the server's ghost
  // picks it up within a call or two and these go live again on their own.
  import { onMount, onDestroy } from "svelte";
  import { GetTimers, GetCurrentZone } from "../../bindings/FuseBridge/app.js";
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
      const data = await GetTimers();
      // Priority: a live mob raid (an interrupt's assignments belong to that
      // fight), then the event raid (Sky / HoT / Ring War), then the ghost —
      // assembled from the whole guild's chat, so it carries the tank lists
      // and debuffs that a locally-revived card never had.
      card =
        pickActive(data) ||
        (data && data.event_raid) ||
        (data && data.ghost_raid) ||
        null;
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
