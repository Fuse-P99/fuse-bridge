<script>
  // Randoms special overlay: live /random results, grouped by the number being
  // rolled, ranked highest first. Answers the one question a roll-off asks —
  // who is on top right now.
  //
  // A roll-off shows its full ranked list while it's live, folds to a one-line
  // result 30s after the last roll, and disappears at 5 minutes. Ranking,
  // duplicate marking, the winner and the collapsed flag are all computed in
  // randoms.go, so this file only decides how they look.
  //
  // Data is parsed from the local log; nothing here is server sourced, so it
  // works for unlinked installs too. The collapse and the expiry ride the clock
  // rather than a log line, which is why this polls rather than relying on the
  // change event alone — a group going quiet produces no event.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { GetRandomRolls } from "../../bindings/FuseBridge/app.js";

  export let hasContent = false; // drives the "Hide When 0" title-bar mode

  let groups = [];
  let off, pollTimer;
  let polling = false;

  $: hasContent = groups.length > 0;

  async function poll() {
    if (polling) return;
    polling = true;
    try {
      groups = (await GetRandomRolls()) || [];
    } catch {
      /* keep the last good list */
    }
    polling = false;
  }

  onMount(async () => {
    await poll();
    off = Events.On("randoms-changed", poll);
    pollTimer = setInterval(poll, 1000);
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (off) off();
  });
</script>

<div class="rnd">
  {#each groups as g (g.min + "-" + g.max)}
    {#if g.collapsed}
      <!-- Settled: the whole roll-off in one line. The item flexes and clips so
           the winner can never be pushed out of view. -->
      <div class="rnd-done" title="{g.label} — won by {g.winner_name}">
        <span class="rnd-ico">🎲</span>
        <span class="rnd-range">{g.label}</span>
        {#if g.item}<span class="rnd-item">{g.item}</span>{/if}
        <span class="rnd-win">{g.winner_name}</span>
        <span class="rnd-wval">{g.winner_value}</span>
      </div>
    {:else}
      <div class="rnd-grp">
        <div class="rnd-head">
          <span class="rnd-ico">🎲</span>
          <span class="rnd-range">{g.label}</span>
          {#if g.item}
            <span class="rnd-item" title={g.item}>{g.item}</span>
          {/if}
        </div>
        {#each g.rolls as r (r.name + ":" + r.at + ":" + r.value)}
          <div
            class="rnd-row"
            class:lead={r.rank === 1}
            class:dead={r.superseded}
            title={r.superseded
              ? `${r.name} already rolled — this one doesn't count`
              : ""}
          >
            <!-- Superseded rolls hold the column but take no number. -->
            <span class="rnd-rank">{r.rank ? r.rank + "." : ""}</span>
            <span class="rnd-val">{r.value}</span>
            <span class="rnd-dash">-</span>
            <span class="rnd-name">{r.name}</span>
            {#if r.seq}<span class="rnd-seq">(#{r.seq})</span>{/if}
          </div>
        {/each}
      </div>
    {/if}
  {/each}
</div>

<style>
  .rnd {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 6px 8px;
    overflow-y: auto;
    /* Readable over the game even on a transparent background. */
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
  .rnd-grp {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .rnd-head {
    display: flex;
    align-items: baseline;
    gap: 5px;
    margin-bottom: 2px;
    min-width: 0;
  }
  .rnd-ico {
    font-size: 12px;
  }
  .rnd-range {
    flex-shrink: 0;
    font-size: 13px;
    font-weight: 700;
    letter-spacing: 0.04em;
    color: var(--accent);
    font-family: var(--font-mono);
  }
  /* Best-effort match from guild chat — present it as the label it is, not as
     something the overlay is certain of. */
  .rnd-item {
    min-width: 0;
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .rnd-row {
    display: flex;
    align-items: baseline;
    gap: 5px;
    min-width: 0;
    font-size: 12.5px;
    color: var(--text-secondary);
  }
  /* The current winner (or winners, on a tie) reads at a glance. */
  .rnd-row.lead {
    color: var(--text-primary);
    font-weight: 600;
  }
  .rnd-rank {
    flex-shrink: 0;
    min-width: 16px;
    text-align: right;
    color: var(--text-muted);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }
  .rnd-val {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }
  .rnd-row.lead .rnd-val {
    color: var(--accent);
  }
  .rnd-dash {
    flex-shrink: 0;
    color: var(--text-muted);
  }
  .rnd-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Which attempt this was, on the players who rolled more than once. */
  .rnd-seq {
    flex-shrink: 0;
    font-size: 11px;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }
  /* A re-roll: shown so nobody wonders where the number went, struck so it's
     unmistakably out of the running. */
  .rnd-row.dead {
    color: var(--text-muted);
    text-decoration: line-through;
    text-decoration-color: rgba(239, 83, 80, 0.75);
    text-decoration-thickness: 1px;
    opacity: 0.8;
  }
  .rnd-row.dead .rnd-val {
    color: var(--text-muted);
  }

  /* ── settled roll-off: one summary line ──────────────────────────────── */
  .rnd-done {
    display: flex;
    align-items: baseline;
    gap: 5px;
    min-width: 0;
    font-size: 12.5px;
    color: var(--text-secondary);
  }
  .rnd-done .rnd-item {
    /* Takes the slack and clips first, so the result never scrolls away. */
    flex: 1 1 auto;
    font-weight: 400;
    color: var(--text-secondary);
  }
  .rnd-win {
    flex-shrink: 0;
    margin-left: auto;
    font-weight: 600;
    color: var(--text-primary);
    max-width: 55%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .rnd-wval {
    flex-shrink: 0;
    color: var(--accent);
    font-weight: 700;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }
</style>
