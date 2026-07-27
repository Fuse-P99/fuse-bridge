<script>
  // Attendance-log copy dialog. The server builds the exact messages the bot
  // would have posted to the raid channel; this just hands them over one
  // clipboard-sized piece at a time, in paste order.
  //
  // Chunks are separate Discord messages by necessity (20 /who lines each, the
  // same split postWhoLinesChunked uses), so there is deliberately no "copy
  // everything" button — pasting them as one message would break the DKP bot's
  // parsing and blow the character limit.
  import { onMount } from "svelte";
  import { Clipboard } from "@wailsio/runtime";
  import {
    GetRaidChannels,
    SendAttendance,
  } from "../../bindings/FuseBridge/app.js";

  export let load; // async () => AttendanceSet
  export let onClose;
  export let heading = "Attendance Logs";
  // Autosend target identity. raidID > 0 sends a completed raid's snapshot;
  // otherwise zone drives a live capture. Autosend is hidden when neither is set.
  export let raidID = 0;
  export let zone = "";

  let loading = true;
  let err = "";
  let set = null;
  let copied = {}; // chunk index → true, so the user can track their place

  // Autosend. GetRaidChannels returns [] for non-officers (the endpoint is
  // officer-gated), which is also what hides the whole section.
  let channels = [];
  let pickedChannel = "";
  let sending = false;
  let sentMsg = "";

  onMount(async () => {
    try {
      set = await load();
      if (set && set.error) err = set.error;
    } catch (e) {
      err = String(e);
    }
    loading = false;
    try {
      channels = (await GetRaidChannels()) || [];
    } catch {
      channels = [];
    }
  });

  async function autosend() {
    if (!pickedChannel || sending) return;
    sending = true;
    sentMsg = "";
    err = "";
    try {
      const n = await SendAttendance(pickedChannel, raidID, zone);
      const ch = channels.find((c) => c.id === pickedChannel);
      sentMsg = `Sent ${n} message${n === 1 ? "" : "s"} to #${ch ? ch.name : "the raid channel"}.`;
    } catch (e) {
      err = String(e);
    }
    sending = false;
  }

  async function copyChunk(i, text) {
    try {
      await Clipboard.SetText(text);
    } catch {
      try {
        await navigator.clipboard.writeText(text);
      } catch {
        err = "Could not access the clipboard.";
        return;
      }
    }
    copied = { ...copied, [i]: true };
  }

  // "13 seconds" / "2m 05s" — plain seconds get unreadable past a minute or so.
  function secs(n) {
    n = Math.abs(Math.round(n));
    if (n < 90) return `${n} second${n === 1 ? "" : "s"}`;
    const m = Math.floor(n / 60);
    return `${m}m ${String(n % 60).padStart(2, "0")}s`;
  }

  // A snapshot describes itself relative to the kill; a live set relative to now.
  $: summary = !set
    ? ""
    : set.snapshot
      ? set.offset_secs < 0
        ? `Taken ${secs(set.offset_secs)} before ${set.mob || "the mob"} died`
        : set.offset_secs > 0
          ? `Taken ${secs(set.offset_secs)} after ${set.mob || "the mob"} died`
          : `Taken as ${set.mob || "the mob"} died`
      : set.age_secs > 0
        ? `As of ${secs(set.age_secs)} ago`
        : "As of just now";

  $: whoCount = set ? (set.chunks || []).filter((c) => c.kind === "who").length : 0;
</script>

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="overlay" on:click|self={onClose}>
  <div class="modal" role="dialog" aria-modal="true">
    <div class="modal-title">{heading}</div>

    {#if loading}
      <div class="note">Building attendance log…</div>
    {:else if err}
      <div class="err">{err}</div>
    {:else if set}
      <div class="head">
        <span class="count">{set.players} Fuse player{set.players === 1 ? "" : "s"}</span>
        <span class="when">{summary}</span>
      </div>
      {#if set.zone}
        <div class="zone">{set.zone}</div>
      {/if}

      <div class="note">
        Paste these into the raid channel <strong>in order</strong>, as separate
        messages.
      </div>

      <div class="chunks">
        {#each set.chunks as c, i}
          <button
            class="chunk"
            class:done={copied[i]}
            class:presence={c.kind === "presence"}
            on:click={() => copyChunk(i, c.text)}
          >
            <span class="ck">{copied[i] ? "✓" : i + 1}</span>
            <span class="clabel">{c.label}</span>
            {#if c.lines}
              <span class="cmeta">{c.lines} lines</span>
            {:else if c.kind === "presence"}
              <span class="cmeta">missed raiders</span>
            {/if}
            <span class="cact">{copied[i] ? "Copied" : "Copy"}</span>
          </button>
        {/each}
      </div>

      {#if whoCount > 1}
        <div class="note dim">
          {whoCount} messages of /who lines — Discord and the DKP bot both choke on
          a single block that long.
        </div>
      {/if}

      {#if channels.length}
        <div class="auto">
          <div class="auto-title">Autosend</div>
          <div class="auto-row">
            <select class="auto-sel" bind:value={pickedChannel} disabled={sending}>
              <option value="">Pick a raid channel…</option>
              {#each channels as c (c.id)}
                <option value={c.id}>#{c.name}</option>
              {/each}
            </select>
            <button
              class="btn send"
              disabled={!pickedChannel || sending}
              on:click={autosend}
            >
              {sending ? "Sending…" : "Send"}
            </button>
          </div>
          {#if sentMsg}
            <div class="auto-ok">{sentMsg}</div>
          {:else}
            <div class="note dim">
              Posts all {set.chunks.length} messages in order, paced like the bot's
              own attendance post.
            </div>
          {/if}
        </div>
      {/if}
    {/if}

    <div class="modal-actions">
      <button class="btn" on:click={onClose}>Close</button>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 120;
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
    width: 460px;
    max-width: 92vw;
    max-height: 85vh;
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
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    flex-wrap: wrap;
  }
  .count {
    font-size: 15px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .when {
    font-size: 11px;
    color: var(--accent);
  }
  .zone {
    font-size: 11px;
    color: var(--text-secondary);
    margin-top: -4px;
  }
  .note {
    font-size: 11px;
    line-height: 1.5;
    color: var(--text-secondary);
  }
  .note.dim {
    color: var(--text-muted);
  }
  .err {
    font-size: 12px;
    line-height: 1.5;
    color: #e05c5c;
  }
  .chunks {
    display: flex;
    flex-direction: column;
    gap: 4px;
    overflow-y: auto;
    padding: 2px 0;
  }
  .chunk {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    text-align: left;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-family: inherit;
    font-size: 12px;
    padding: 6px 9px;
    cursor: pointer;
  }
  .chunk:hover {
    border-color: var(--accent-dim);
  }
  .chunk.done {
    border-color: rgba(107, 191, 107, 0.5);
  }
  .chunk.done .ck {
    background: rgba(107, 191, 107, 0.25);
    color: #6bbf6b;
  }
  /* The presence block is functionally different from the /who blocks. */
  .chunk.presence .clabel {
    color: var(--accent);
    font-family: var(--font-mono);
  }
  .ck {
    flex-shrink: 0;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.07);
    color: var(--text-muted);
    font-size: 10px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .clabel {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cmeta {
    flex-shrink: 0;
    font-size: 10px;
    color: var(--text-muted);
  }
  .cact {
    flex-shrink: 0;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--accent);
  }
  .auto {
    border-top: 1px solid var(--border);
    padding-top: 8px;
    margin-top: 2px;
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .auto-title {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .auto-row {
    display: flex;
    gap: 6px;
  }
  .auto-sel {
    flex: 1;
    min-width: 0;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-family: inherit;
    font-size: 12px;
    padding: 4px 6px;
    outline: none;
  }
  .auto-sel:focus {
    border-color: var(--accent-dim);
  }
  .auto-ok {
    font-size: 11px;
    color: #6bbf6b;
  }
  .btn.send {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .btn:disabled {
    opacity: 0.45;
    cursor: default;
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
</style>
