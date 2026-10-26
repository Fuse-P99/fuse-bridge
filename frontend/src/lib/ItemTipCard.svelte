<script>
  // The item hover card — ONE view for every place an item appears: the
  // Magelo sheet, the quest walkthrough (Characters tab + editor), raid
  // loot cards, and the Search tab's Items and Raids panes. Panes own hover
  // tracking, positioning, and holder loading; this owns what the card
  // looks like, so a change here lands on every surface at once.
  //
  // Layout: name in the Magelo hover's gold header, the wiki-style stats on
  // their own slightly lifted mono panel (the Items-pane styling), then the
  // money facts (DKP med/mean, drop rate, recent sales, quest pricing) and
  // the held-by footer in the Magelo hover's plain styling.
  import { tipStats, tipMoney } from "./itemTip.js";

  export let name;
  export let item = null; // MageloItem, or null when not in the item DB
  export let holders = []; // the user's own characters holding it
  export let x = 0;
  export let y = 0;
  export let fallback = "Not in the item DB yet.";

  $: stats = item ? tipStats(item) : [];
  $: money = item ? tipMoney(item) : [];
</script>

<div class="tip" style="left:{x}px;top:{y}px">
  <div class="tip-name">{name}</div>
  {#if item}
    {#if stats.length}
      <div class="tip-stats">
        {#each stats as l}<div class="tip-stat">{l}</div>{/each}
      </div>
    {/if}
    {#each money as l}<div class="tip-line">{l}</div>{/each}
  {:else}
    <div class="tip-line tip-dim">{fallback}</div>
  {/if}
  {#if holders.length}
    <div class="tip-rule"></div>
    <div
      class="tip-line tip-dim"
      title={holders.map((h) => `${h.char}: ${h.where}`).join("\n")}
    >
      Held by: {holders
        .map((h) => h.char + (h.count > 1 ? ` ×${h.count}` : ""))
        .join(", ")}
    </div>
  {/if}
</div>

<style>
  .tip {
    position: fixed;
    z-index: 500;
    width: 260px;
    background: rgba(10, 12, 18, 0.97);
    border: 1px solid var(--accent-dim, var(--border));
    border-radius: 5px;
    padding: 8px 10px;
    pointer-events: none;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.6);
  }
  .tip-name {
    font-size: 12.5px;
    font-weight: 700;
    color: var(--accent);
    margin-bottom: 4px;
  }
  /* The stats panel: its own faint ground and hairline border, so the wiki
     facts read as a block apart from the money lines below. */
  .tip-stats {
    background: rgba(255, 255, 255, 0.04);
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 4px;
    padding: 5px 7px;
    margin: 2px 0 6px;
  }
  .tip-stat {
    font-size: 11px;
    color: var(--text-secondary);
    font-family: var(--font-mono);
    line-height: 1.45;
  }
  .tip-line {
    font-size: 11px;
    color: var(--text-primary);
    line-height: 1.5;
  }
  .tip-rule {
    height: 1px;
    margin: 5px 0 4px;
    background: rgba(255, 255, 255, 0.14);
  }
  .tip-dim {
    color: var(--text-muted);
    font-style: italic;
  }
</style>
