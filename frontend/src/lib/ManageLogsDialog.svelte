<script>
  // The Manage Logs form. It lives here rather than on the Logs tab because two
  // surfaces open it — Search → Logs → Manage Logs, and the General tab's
  // "Archive logs" row when a folder has to be fixed before archiving can be
  // turned on — and both must show and write the same archival settings.
  import { createEventDispatcher } from "svelte";
  import {
    GetLogArchiveSettings,
    SaveLogArchiveSettings,
    BrowseArchiveDir,
  } from "../../bindings/FuseBridge/app.js";

  export let open = false;
  // Pre-tick "Archive my logs" on open. The General tab sets it after its own
  // enable was rejected, so the one thing still to fix — the folder — is the
  // only thing the reader has to touch before Save.
  export let startEnabled = false;

  const dispatch = createEventDispatcher();

  let arch = { enabled: false, dir: "", size_mb: 50, delete_days: 0 };
  let delEnabled = false;
  let delDays = 30;
  let saving = false;
  let err = "";
  const SIZE_OPTIONS = [20, 50, 100];

  // Load once per open, not on every reactive tick: re-reading Go mid-edit
  // would throw away the folder the reader is part way through typing.
  let wasOpen = false;
  $: if (open !== wasOpen) {
    wasOpen = open;
    if (open) load();
  }

  async function load() {
    err = "";
    try {
      arch = await GetLogArchiveSettings();
    } catch {
      arch = { enabled: false, dir: "", size_mb: 50, delete_days: 0 };
    }
    if (startEnabled) arch.enabled = true;
    delEnabled = (arch.delete_days || 0) > 0;
    delDays = arch.delete_days > 0 ? arch.delete_days : 30;
  }

  async function browseArchive() {
    try {
      const d = await BrowseArchiveDir();
      if (d) arch.dir = d;
    } catch {
      /* dialog cancelled */
    }
  }

  async function saveManage() {
    const next = {
      enabled: arch.enabled,
      dir: arch.dir,
      size_mb: Number(arch.size_mb) || 50,
      delete_days: delEnabled
        ? Math.max(1, Math.round(Number(delDays) || 0))
        : 0,
    };
    saving = true;
    try {
      await SaveLogArchiveSettings(next);
    } catch (e) {
      // Go proves the folder is writable before it saves anything, and the
      // rejection carries the sentence to show the reader. Nothing was
      // written, so keep the form open on the field that has to change.
      err = e?.message || String(e);
      return;
    } finally {
      saving = false;
    }
    err = "";
    open = false;
    dispatch("saved", next);
  }
</script>

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="overlay" on:click|self={() => (open = false)}>
    <div class="modal">
      <div class="modal-title">Manage Logs</div>
      <div class="m-note">
        Archiving moves your older, oversized log files out of the EQ Logs
        folder during a quiet period (after the game has been idle a while, so
        it never interferes with live logging). The character currently being
        played is never touched.
      </div>

      <label class="tgl">
        <input type="checkbox" bind:checked={arch.enabled} />
        <span class="tgl-track"><span class="tgl-knob"></span></span>
        <span class="tgl-label">Archive my logs</span>
      </label>

      {#if arch.enabled}
        <div class="m-sep"></div>

        <label class="m-label" for="ml-dir">Archive location</label>
        <div class="m-inline">
          <input id="ml-dir" class="in" bind:value={arch.dir} />
          <button class="btn" on:click={browseArchive}>Browse…</button>
        </div>

        <label class="m-label" for="ml-size">Archive logs larger than</label>
        <select id="ml-size" class="in sel" bind:value={arch.size_mb}>
          {#each SIZE_OPTIONS as mb}
            <option value={mb}>{mb} MB</option>
          {/each}
        </select>

        <div class="m-sep"></div>
        <label class="tgl">
          <input type="checkbox" bind:checked={delEnabled} />
          <span class="tgl-track"><span class="tgl-knob"></span></span>
          <span class="tgl-label">Auto-delete old archived logs</span>
        </label>
        {#if delEnabled}
          <div class="m-inline">
            <span class="m-label">Delete after</span>
            <input class="in num" type="number" min="1" bind:value={delDays} />
            <span class="m-label">days</span>
          </div>
        {/if}
      {/if}

      {#if err}
        <div class="m-err">{err}</div>
      {/if}

      <div class="modal-actions">
        <button class="btn save" disabled={saving} on:click={saveManage}
          >{saving ? "Saving…" : "Save"}</button
        >
        <button class="btn" on:click={() => (open = false)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  /* Manage Logs modal */
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 50;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .modal {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    width: 400px;
    max-width: 90%;
    max-height: 85%;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 9px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 6px;
  }
  .m-note {
    color: var(--text-muted);
    font-size: 11.5px;
    line-height: 1.5;
  }
  .m-label {
    font-size: 11px;
    color: var(--text-secondary);
    white-space: nowrap;
  }
  .m-inline {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .m-inline .in {
    flex: 1;
    min-width: 0;
  }
  .m-inline .in.num {
    flex: 0 0 auto;
  }
  .m-sep {
    height: 1px;
    background: var(--border);
    margin: 4px 0;
  }
  /* Why the save was refused, in the dialog rather than a toast: the field it
     names is right above it and is what has to change. */
  .m-err {
    font-size: 11px;
    color: #ef4444;
    line-height: 1.4;
  }

  /* toggle switch (reused for enable + auto-delete) */
  .tgl {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }
  .tgl input {
    display: none;
  }
  .tgl-track {
    position: relative;
    width: 30px;
    height: 16px;
    border-radius: 8px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    transition: background 0.15s;
    flex-shrink: 0;
  }
  .tgl-knob {
    position: absolute;
    top: 1px;
    left: 1px;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--text-muted);
    transition:
      transform 0.15s,
      background 0.15s;
  }
  .tgl input:checked + .tgl-track {
    background: var(--accent-dim);
    border-color: var(--accent-dim);
  }
  .tgl input:checked + .tgl-track .tgl-knob {
    transform: translateX(14px);
    background: var(--accent);
  }
  .tgl-label {
    color: var(--text-secondary);
    font-size: 12px;
  }

  /* base button + inputs (component-scoped, copied from the Logs tab so the
     dialog looks the same wherever it is mounted) */
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 3px 10px;
  }
  .btn:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  .btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .btn:disabled:hover {
    color: var(--text-secondary);
    border-color: var(--border);
  }
  .btn.save {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .in {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 5px 8px;
    outline: none;
    width: 100%;
  }
  .in:focus {
    border-color: var(--accent-dim);
  }
  .in.num {
    width: 80px;
  }
  .in.sel {
    width: auto;
    min-width: 120px;
  }
</style>
