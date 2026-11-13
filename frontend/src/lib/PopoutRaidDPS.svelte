<script>
  // Overlay gate for the Raid DPS board. The board itself (RaidDPS) shows
  // whatever fight the server is parsing — right for a card on the Raids tab,
  // wrong for a translucent strip over the game: without a gate it lit up for
  // anyone with the overlay enabled anywhere in Norrath (Ring War made this
  // obvious — an event raid generates parse data for the whole guild).
  //
  // Opening gate: a raid card must be live (mob card, event raid, or the
  // server's ghost), the player's zone and the card's zone must both be KNOWN
  // and match, AND the server must return a live board.
  //
  // Once open it LATCHES: the card gate is ignored (a single failed timers
  // fetch nulls the card, which used to blink the overlay off mid-fight) and
  // RaidDPS holds its last board through empty/failed answers. It closes only
  // on: the kill (final board past its hold), the player leaving the zone the
  // board latched in, or no new damage past the hold — and a held trash mob
  // switches to the raid mob when one is named. Unmounting the board when
  // neither latch nor gate holds also stops its GetRaidDPS polling.
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

  // Same normalisation as PopoutRaidSection: "The Plane of Sky" and
  // "plane of sky" are one zone.
  const zoneKey = (z) =>
    (z || "")
      .toLowerCase()
      .replace(/^the\s+/, "")
      .replace(/[^a-z0-9]+/g, "");

  async function poll() {
    try {
      // An empty reply (failed fetch, unverified, no raid) nulls the card.
      // Harmless while latched — the latch ignores the card gate; the gate
      // only controls OPENING.
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

  // Strict: both zones must be known. An empty card zone no longer fails open
  // — the server now journals a target with no zone, so an empty zone is a
  // data bug to surface, not a reason to show the overlay everywhere. An
  // unknown player zone stays closed: we can't place you in the raid zone.
  $: gateOpen =
    !!card &&
    !!card.zone &&
    !!myZone &&
    zoneKey(myZone) === zoneKey(card.zone);

  let boardHas = false;
  let latched = false;
  let latchZone = "";

  // Latch / unlatch in ONE function so the two transitions are exclusive per
  // run: a fresh latch can't be cleared in the same pass, and Svelte sees no
  // cycle (assignments inside a function aren't tracked as dependencies, so
  // this re-runs only when the gate, the board or the player's zone changes).
  // An unknown player zone (empty) is not "left the zone" — hold the board.
  function updateLatch(open, has, zone) {
    if (latched) {
      const leftZone = !!zone && zoneKey(zone) !== zoneKey(latchZone);
      if (leftZone || !has) {
        latched = false;
        latchZone = "";
      }
    } else if (open && has) {
      latched = true;
      latchZone = card.zone;
    }
  }
  $: updateLatch(gateOpen, boardHas, myZone);

  // These must run AFTER updateLatch in the same pass so they see the new
  // latched value. Svelte orders reactive statements by tracked assignments,
  // so the reset below goes through a function: written inline, its
  // `boardHas = false` would hoist it (and `mounted`) above updateLatch.
  $: mounted = latched || gateOpen;
  // Board unmounted → don't leave a stale name in the title, and drop the
  // bound hasAny: an unmounted child never resets it, and a stale true would
  // latch the instant the gate reopens, before the new board has answered.
  function clearBoard() {
    if (targetName) targetName = "";
    if (boardHas) boardHas = false;
  }
  $: if (!mounted) clearBoard();
  $: hasContent = boardHas && mounted;

  onMount(() => {
    poll();
    pollTimer = setInterval(poll, 5000);
  });
  onDestroy(() => clearInterval(pollTimer));
</script>

<div class="praid">
  {#if mounted}
    <!-- No card prop: the overlay always wants the live fight, same as before.
         latch: hold the board through empty/failed answers (see RaidDPS).
         showLabel={false}: the popout's title bar already says "Raid DPS". -->
    <RaidDPS latch showLabel={false} bind:hasAny={boardHas} bind:targetName />
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
