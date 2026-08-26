<script>
  // Overlay gate for the Raid DPS board. The board itself (RaidDPS) shows
  // whatever fight the server is parsing — right for a card on the Raids tab,
  // wrong for a translucent strip over the game: without a gate it lit up for
  // anyone with the overlay enabled anywhere in Norrath (Ring War made this
  // obvious — an event raid generates parse data for the whole guild).
  //
  // So the overlay follows the same rules as the other raid overlays
  // (PopoutRaidSection): a raid must be live — a mob card, the event raid
  // (Sky / HoT / Ring War), or the server's ghost — AND the player must be
  // standing in its zone. Unmounting the board when the gate closes also
  // stops its GetRaidDPS polling.
  import { onMount, onDestroy } from "svelte";
  import { GetTimers, GetCurrentZone } from "../../bindings/FuseBridge/app.js";
  import RaidDPS from "./RaidDPS.svelte";

  // Pushed up to the popout shell so "Hide when 0 triggers" can hide the title.
  export let hasContent = false;
  // The fight the board is naming, for the shell's "Raid DPS - Target" title.
  export let targetName = "";

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

  // Unknown raid zone (mob not in the DB) fails open; unknown player zone
  // hides — you're not in the raid zone if we can't place you in any zone.
  $: inRaidZone =
    !!card &&
    (!card.zone ||
      (myZone && myZone.toLowerCase() === card.zone.toLowerCase()));
  let boardHas = false;
  $: hasContent = !!card && inRaidZone && boardHas;
  // Gate closed → the board unmounts; don't leave a stale name in the title.
  $: if (!(card && inRaidZone) && targetName) targetName = "";

  onMount(() => {
    poll();
    pollTimer = setInterval(poll, 5000);
  });
  onDestroy(() => clearInterval(pollTimer));
</script>

<div class="praid">
  {#if card && inRaidZone}
    <!-- No card prop: the overlay always wants the live fight, same as before.
         showLabel={false}: the popout's title bar already says "Raid DPS". -->
    <RaidDPS showLabel={false} bind:hasAny={boardHas} bind:targetName />
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
