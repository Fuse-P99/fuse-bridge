<script>
  // The member hover card — the counterpart to ItemTipCard behind a guild-chat
  // toon name, and deliberately built to look like it: same box, same gold
  // header, same plain lines. Pure presentation; the pane that shows it owns
  // the lookup and the positioning.
  //
  // Three states, because a hover card that goes blank while a request is in
  // flight reads as broken: looking up, found, and not a member.
  export let query = ""; // what was asked for — the header until the answer lands
  export let info = null; // MemberWhoInfo, or null when unknown/unrostered
  export let loading = false;
  // Where to draw it, the way ItemTipCard takes its position: the pane that
  // floats this card (the docked Guild Chat panel) sets them from
  // the cursor, already flipped and clamped to stay inside the window.
  export let x = 0;
  export let y = 0;

  // Raid attendance comes back as a percentage already; whole numbers are
  // what everyone quotes it in.
  const pct = (v) => `${Math.round(Number(v) || 0)}%`;
  // The FULL display name here: the chat panel writes no identity on its
  // lines at all, so this card is the only place it is ever spelled out.
  $: title = (info && (info.display_name || info.handle)) || query;
</script>

<div class="tip" style="left:{x}px;top:{y}px">
  <div class="tip-name">{title}</div>
  {#if loading}
    <div class="tip-line tip-dim">Looking up…</div>
  {:else if info}
    <div class="tip-line">DKP: {info.dkp || 0}</div>
    <div class="tip-line">
      RA: 30 - {pct(info.ra_30)} / Life - {pct(info.ra_life)}
    </div>
  {:else}
    <div class="tip-line tip-dim">Not a rostered member.</div>
  {/if}
</div>

<style>
  /* Kept in step with ItemTipCard's .tip family by hand: the two cards share a
     window and must read as one thing. */
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
  .tip-line {
    font-size: 11px;
    color: var(--text-primary);
    line-height: 1.5;
  }
  .tip-dim {
    color: var(--text-muted);
    font-style: italic;
  }
</style>
