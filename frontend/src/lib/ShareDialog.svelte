<script>
  // Recipient picker for sending a share (trigger or markers). Loads the
  // server's directory of recently-seen clients; each row shows the display
  // name plus its short share id, with a "linked" tag for Discord-verified
  // senders (anonymous names are self-reported).
  import { onMount } from "svelte";
  import {
    GetShareDirectory,
    GetShareIdentity,
  } from "../../bindings/FuseBridge/app.js";

  export let title = "Share";
  export let previewLines = []; // what's being sent, shown above the picker
  export let onSend; // async (addr) => void
  export let onClose;

  let contacts = null; // null = loading
  let ident = null;
  let selected = "";
  let sending = false;
  let msg = "";
  let err = "";

  onMount(async () => {
    try {
      ident = await GetShareIdentity();
    } catch {
      ident = null;
    }
    try {
      contacts = (await GetShareDirectory()) || [];
    } catch (e) {
      contacts = [];
      err = String(e);
    }
  });

  async function send() {
    if (!selected || sending) return;
    sending = true;
    err = "";
    try {
      await onSend(selected);
      msg = "Sent!";
      setTimeout(() => onClose && onClose(), 900);
    } catch (e) {
      err = String(e);
      sending = false;
    }
  }
</script>

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="sh-overlay" on:click|self={() => onClose && onClose()}>
  <div class="sh-dlg">
    <div class="sh-title">{title}</div>
    {#if previewLines.length}
      <div class="sh-preview">
        {#each previewLines as l}<div class="sh-pline">{l}</div>{/each}
      </div>
    {/if}

    {#if contacts === null}
      <div class="sh-note">Loading recipients…</div>
    {:else if !contacts.length}
      <div class="sh-note">
        No recipients found — nobody else has been online recently.
      </div>
    {:else}
      <label class="sh-label" for="sh-to">Send to</label>
      <select id="sh-to" class="sh-in" bind:value={selected}>
        <option value="" disabled>Choose a recipient…</option>
        {#each contacts as c (c.addr)}
          <option value={c.addr}
            >{c.discord ? `${c.name} (${c.discord})` : c.name}</option
          >
        {/each}
      </select>
    {/if}

    {#if err}<div class="sh-err">{err}</div>{/if}
    {#if msg}<div class="sh-ok">{msg}</div>{/if}

    <div class="sh-btns">
      <button class="sh-btn" on:click={() => onClose && onClose()}
        >Cancel</button
      >
      <button
        class="sh-btn sh-send"
        disabled={!selected || sending}
        on:click={send}>{sending ? "Sending…" : "Send"}</button
      >
    </div>

    {#if ident && ident.registered}
      <div class="sh-me">
        Your share ID: {ident.name ? ident.name : "(no character)"}#{ident.addr}
      </div>
    {/if}
  </div>
</div>

<style>
  .sh-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 200;
  }
  .sh-dlg {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    width: 380px;
    max-width: 92vw;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .sh-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .sh-preview {
    background: rgba(255, 255, 255, 0.04);
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 8px 10px;
    max-height: 130px;
    overflow-y: auto;
  }
  .sh-pline {
    font-size: 12px;
    color: var(--text-secondary);
    word-break: break-word;
  }
  .sh-pline:first-child {
    color: var(--text-primary);
    font-weight: 600;
  }
  .sh-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .sh-in {
    background: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 6px 8px;
    font-size: 13px;
    /* The dropdown list itself is UA-rendered; without a dark color-scheme it
       paints white behind our light text. */
    color-scheme: dark;
  }
  .sh-in option {
    background: var(--bg-secondary);
    color: var(--text-primary);
  }
  .sh-note {
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }
  .sh-err {
    font-size: 12px;
    color: #ff6b6b;
  }
  .sh-ok {
    font-size: 12px;
    color: var(--success);
  }
  .sh-btns {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .sh-btn {
    background: none;
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 5px;
    padding: 5px 14px;
    font-size: 12.5px;
    cursor: pointer;
  }
  .sh-btn:hover {
    background: rgba(255, 255, 255, 0.06);
  }
  .sh-send {
    border-color: var(--accent);
    color: var(--accent);
  }
  .sh-send:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .sh-me {
    font-size: 11px;
    color: var(--text-muted);
    text-align: right;
  }
</style>
