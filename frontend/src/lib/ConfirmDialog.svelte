<script>
  // The app's one confirmation modal. Mounted once at the root; everything else
  // asks through confirmDialog() in confirm.js. Styled to match the existing
  // .overlay/.modal boxes rather than the OS dialog window.confirm produces.
  import { onMount, tick } from "svelte";
  import { confirmState, settleConfirm } from "./confirm.js";

  let confirmBtn;

  // Enter confirms, Escape cancels — the same keys the native dialog answered
  // to, so muscle memory survives the swap.
  function onKeydown(e) {
    if (!$confirmState) return;
    if (e.key === "Escape") {
      e.preventDefault();
      settleConfirm(false);
    } else if (e.key === "Enter") {
      e.preventDefault();
      settleConfirm(true);
    }
  }

  // Focus the confirm button when the dialog appears so the keyboard path works
  // without a click first.
  onMount(() => {
    const unsub = confirmState.subscribe(async (s) => {
      if (!s) return;
      await tick();
      confirmBtn && confirmBtn.focus();
    });
    return unsub;
  });
</script>

<svelte:window on:keydown={onKeydown} />

{#if $confirmState}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="overlay" on:click|self={() => settleConfirm(false)}>
    <div class="modal" role="dialog" aria-modal="true">
      <div class="modal-title">{$confirmState.title}</div>
      <div class="msg">{$confirmState.message}</div>
      {#if $confirmState.detail}
        <div class="detail">{$confirmState.detail}</div>
      {/if}
      <div class="modal-actions">
        <button
          class="btn"
          class:danger={$confirmState.danger}
          class:save={!$confirmState.danger}
          bind:this={confirmBtn}
          on:click={() => settleConfirm(true)}
        >
          {$confirmState.confirmLabel}
        </button>
        <button class="btn" on:click={() => settleConfirm(false)}>
          {$confirmState.cancelLabel}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    /* Above every other modal: a confirmation is always raised from one. */
    z-index: 200;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .modal {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 14px;
    width: 340px;
    max-width: 88vw;
    display: flex;
    flex-direction: column;
    gap: 7px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .msg {
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-primary);
  }
  .detail {
    font-size: 11px;
    line-height: 1.5;
    color: var(--text-muted);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 4px;
  }
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    font-family: inherit;
    padding: 3px 10px;
  }
  .btn:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  .btn:focus-visible {
    outline: 1px solid var(--accent-dim);
    outline-offset: 1px;
  }
  .btn.save {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .btn.danger {
    color: #e05c5c;
    border-color: rgba(224, 92, 92, 0.5);
  }
  .btn.danger:hover {
    color: #ff7a7a;
    border-color: #e05c5c;
  }
</style>
