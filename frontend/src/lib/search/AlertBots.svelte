<script>
  // The "bots with alerts for this batphone" panel — shared by the Bots
  // sub-tab and the batphone bar's Show Bots dropdown, so both surfaces stay
  // identical by construction. Self-contained: renders the rows, runs the
  // claim flow (confirm → claim → credentials modal), and dispatches
  // "changed" after a successful claim so parents can refresh their data.
  //
  // The bot list arrives already filtered by King Ak'Anon — per-member access
  // roles AND the post-quake triplets hiding both happen server-side there.
  import { onMount, onDestroy, createEventDispatcher } from "svelte";
  import { ClaimGuildBot } from "../../../bindings/FuseBridge/app.js";
  import { classAbbr } from "../classAbbr.js";
  import BotCredsModal from "./BotCredsModal.svelte";

  export let bp = null; // BatphoneBots payload { mob, sent_at_ms, bots }

  const dispatch = createEventDispatcher();
  let now = Date.now();
  let tick;
  let inflight = "";
  let err = "";
  let detail = null;

  onMount(() => (tick = setInterval(() => (now = Date.now()), 1000)));
  onDestroy(() => clearInterval(tick));

  function isClaimed(b) {
    return !!b.claimed_by && b.claim_expires_ms > now;
  }

  async function claim(b) {
    // No confirm step: this panel only exists mid-batphone, where seconds
    // matter — one click claims and the login modal opens. The Discord
    // announcement disclosure lives on the button's hover title instead.
    if (inflight) return;
    inflight = b.name;
    err = "";
    try {
      detail = await ClaimGuildBot(b.name);
      // Reflect the claim locally right away (the server-side caches lag a
      // few seconds); parents refresh their own copies off the event.
      b.claimed_by = detail.claimed_by || "you";
      b.claim_expires_ms = detail.claim_expires_ms || Date.now() + 300000;
      bp = bp;
      dispatch("changed");
    } catch (e) {
      err = String(e?.message || e);
    }
    inflight = "";
  }
</script>

{#if bp && bp.mob && bp.bots && bp.bots.length}
  <div class="bp-panel">
    <div class="bp-head">
      <span class="bp-tag">BATPHONE</span>
      <span class="bp-mob">{bp.mob}</span>
      <span class="bp-sub">bots with alerts for this fight</span>
      {#if err}<span class="bp-err">{err}</span>{/if}
    </div>
    <div class="bp-bots">
      {#each bp.bots as b (b.name)}
        <div class="bp-bot" class:claimed={isClaimed(b)}>
          <span class="bp-name">{b.name}</span>
          <span class="bp-meta"
            >{classAbbr(b.class)} · {b.park_zone || "unparked"}{#if b.park_note}
              · {b.park_note}{/if}</span
          >
          {#if isClaimed(b)}
            <span class="bp-claimed">claimed by {b.claimed_by}</span>
          {:else}
            <button
              class="act"
              disabled={!!inflight}
              title="Claims for 5 minutes — announced in the bots-data Discord channel under your name."
              on:click={() => claim(b)}
              >{inflight === b.name ? "Claiming…" : "Claim"}</button
            >
          {/if}
        </div>
      {/each}
    </div>
  </div>
{/if}

{#if detail}
  <BotCredsModal
    {detail}
    title="Claimed — Bot Login"
    onClose={() => (detail = null)}
  />
{/if}

<style>
  .bp-panel {
    flex-shrink: 0;
    margin: 0 12px 8px;
    border: 1px solid #e3a008;
    background: rgba(227, 160, 8, 0.1);
    border-radius: 6px;
    padding: 8px 10px;
  }
  .bp-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    margin-bottom: 6px;
  }
  .bp-tag {
    color: #e3a008;
    font-weight: 800;
    font-size: 10px;
    letter-spacing: 0.08em;
  }
  .bp-mob {
    font-weight: 700;
    color: var(--text-primary);
    font-size: 13px;
  }
  .bp-sub {
    color: var(--text-muted);
    font-size: 11px;
  }
  .bp-err {
    color: var(--error, #e05c5c);
    font-size: 11px;
    margin-left: auto;
  }
  .bp-bots {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .bp-bot {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
  }
  .bp-bot.claimed {
    opacity: 0.65;
  }
  .bp-name {
    font-weight: 600;
    color: var(--text-primary);
    min-width: 90px;
  }
  .bp-meta {
    color: var(--text-secondary);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .bp-claimed {
    color: var(--text-muted);
    font-size: 11px;
  }
  .act {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 2px 8px;
    font-size: 11px;
    cursor: pointer;
  }
  .act:hover:not(:disabled) {
    border-color: var(--border-hover);
    color: var(--text-primary);
  }
  .act:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>
