<script>
  // The app's one confirmation modal. Mounted once at the root; everything else
  // asks through confirmDialog() in confirm.js. Styled to match the existing
  // .overlay/.modal boxes rather than the OS dialog window.confirm produces.
  import { onMount, tick } from "svelte";
  import { confirmState, settleConfirm } from "./confirm.js";
  // This modal is mounted outside .shell (so it survives the upgrade screen),
  // which means it misses the `zoom` App.svelte puts on the shell for the
  // text-size buttons. Apply the same factor here or the dialog stays at 100%
  // while the rest of the app is at 120/140%.
  import { scale } from "./scale.js";

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
    <!-- zoom scales the box for layout, so the overlay's flex centering still
         works; max-width has to be divided back out because vw inside a zoomed
         element resolves against the unscaled viewport. -->
    <div
      class="modal"
      role="dialog"
      aria-modal="true"
      style="zoom:{$scale}; max-width:calc(90vw / {$scale})"
    >
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
    padding: 16px 18px;
    width: 380px;
    display: flex;
    flex-direction: column;
    gap: 9px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal-title {
    font-size: 13px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .msg {
    font-size: 14px;
    line-height: 1.45;
    color: var(--text-primary);
  }
  .detail {
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-muted);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 6px;
  }
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 12px;
    font-family: inherit;
    padding: 6px 14px;
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
