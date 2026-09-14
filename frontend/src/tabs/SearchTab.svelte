<script>
  // The Search tab: a thin shell over its sub-tabs. Logs is the old Logs tab
  // unchanged; the others are search surfaces over server data (Bots via the
  // King Ak'Anon proxy, Parses/Raids over the permanent history tables, Items
  // over the item DB). Panes render with {#if} so switching destroys them —
  // LogsTab installs <svelte:window> key handlers that must unmount with it.
  //
  // Member-only sub-tabs are HIDDEN (not error-stated) for anyone the server
  // can't verify — unlinked and offboarded alike. IsMemberVerified is a real
  // server round trip (a local token alone proves nothing once revoked);
  // every member endpoint still re-checks on its own.
  import { onMount } from "svelte";
  import { searchView } from "../lib/nav.js";
  import { linked } from "../lib/linkState.js";
  import { IsMemberVerified } from "../../bindings/FuseBridge/app.js";
  import LogsTab from "./LogsTab.svelte";
  import BotsPane from "../lib/search/BotsPane.svelte";
  import ParsesPane from "../lib/search/ParsesPane.svelte";
  import RaidsPane from "../lib/search/RaidsPane.svelte";
  import ItemsPane from "../lib/search/ItemsPane.svelte";
  import MembersPane from "../lib/search/MembersPane.svelte";

  const PAGES = [
    { id: "logs", label: "Logs" },
    { id: "bots", label: "Bots", gated: true },
    { id: "parses", label: "Parses", gated: true },
    { id: "raids", label: "Raids", gated: true },
    { id: "items", label: "Items" },
    { id: "members", label: "Members", gated: true },
  ];

  let memberOk = false;
  async function checkMember() {
    try {
      memberOk = await IsMemberVerified();
    } catch {
      memberOk = false;
    }
  }
  onMount(checkMember);
  let prevLinked = $linked;
  $: if ($linked !== prevLinked) {
    prevLinked = $linked;
    checkMember();
  }

  $: pages = PAGES.filter((p) => !p.gated || memberOk);
  // If the active sub-tab just got hidden, land somewhere that exists.
  $: if (!pages.find((p) => p.id === $searchView)) searchView.set("logs");
</script>

<div class="search-tab">
  <div class="subtabs">
    {#each pages as p (p.id)}
      <button
        class="subtab"
        class:on={$searchView === p.id}
        on:click={() => searchView.set(p.id)}>{p.label}</button
      >
    {/each}
  </div>
  <div class="pane">
    {#if $searchView === "bots"}
      <BotsPane />
    {:else if $searchView === "parses"}
      <ParsesPane />
    {:else if $searchView === "raids"}
      <RaidsPane />
    {:else if $searchView === "items"}
      <ItemsPane />
    {:else if $searchView === "members"}
      <MembersPane />
    {:else}
      <LogsTab />
    {/if}
  </div>
</div>

<style>
  .search-tab {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }
  .subtabs {
    display: flex;
    gap: 2px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 2px;
    margin: 8px 12px 8px;
    align-self: flex-start;
    flex-shrink: 0;
  }
  .subtab {
    border: none;
    background: transparent;
    color: var(--text-secondary);
    font-size: 11px;
    padding: 3px 10px;
    border-radius: 3px;
    cursor: pointer;
  }
  .subtab.on {
    background: var(--bg-secondary);
    color: var(--accent);
  }
  .pane {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
  .pane > :global(*) {
    flex: 1;
    min-height: 0;
  }
</style>
