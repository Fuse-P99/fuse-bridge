<script>
  // Reusable "on this event, do these" fields: show an alert, speak it, and/or
  // play a sound. Used for a trigger's on-match action and for its timer-ending
  // and timer-ended actions. `action` is bound two-way to the parent's object.
  export let action; // { use_text, display_text, use_tts, tts_interrupt, tts_text, play_media, media_file }
  export let mediaFiles = []; // available audio file names
  export let onAdd = null; // async () => addedFileName | ""
  export let onSample = null; // (name) => void
  export let alertLabel = "Show alert";

  // Keep an assigned-but-not-local file selectable so saving doesn't drop it.
  $: opts =
    action.media_file && !mediaFiles.includes(action.media_file)
      ? [action.media_file, ...mediaFiles]
      : mediaFiles;

  async function add() {
    if (!onAdd) return;
    const n = await onAdd();
    if (n) {
      action.media_file = n;
      action.play_media = true;
      action = action;
    }
  }
  function sample() {
    if (onSample && action.media_file) onSample(action.media_file);
  }
</script>

<label class="f-chk">
  <input type="checkbox" bind:checked={action.use_text} />
  {alertLabel}
</label>
{#if action.use_text}
  <input
    class="in"
    placeholder="Alert text (supports $&#123;1&#125; captures)"
    bind:value={action.display_text}
  />
{/if}

<label class="f-chk">
  <input type="checkbox" bind:checked={action.use_tts} /> Speak (text to speech)
</label>
{#if action.use_tts}
  <input
    class="in"
    placeholder="Text to speak (supports $&#123;1&#125; captures)"
    bind:value={action.tts_text}
  />
  <label class="f-chk">
    <input type="checkbox" bind:checked={action.tts_interrupt} /> Interrupt speech
    already playing
  </label>
{/if}

<label class="f-chk">
  <input type="checkbox" bind:checked={action.play_media} /> Play sound
</label>
{#if action.play_media}
  <div class="media-row">
    <select class="in media-sel" bind:value={action.media_file}>
      <option value="">— none —</option>
      {#each opts as m (m)}
        <option value={m}>{m}</option>
      {/each}
    </select>
    <button
      type="button"
      class="btn media-btn"
      title="Sample this sound"
      aria-label="Sample sound"
      disabled={!action.media_file}
      on:click={sample}
    >
      <svg viewBox="0 0 24 24" width="13" height="13" fill="currentColor">
        <path d="M8 5v14l11-7z" />
      </svg>
    </button>
    <button type="button" class="btn media-btn" on:click={add}>Add file…</button>
  </div>
{/if}

<style>
  .f-chk {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
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
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 8px;
  }
  .btn:hover {
    color: var(--accent);
  }
  .media-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .media-sel {
    flex: 1;
    min-width: 0;
  }
  .media-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    white-space: nowrap;
  }
  .media-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
</style>
