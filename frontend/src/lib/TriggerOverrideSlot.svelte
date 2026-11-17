<script>
  // One action slot (On Match / Timer Ending / Timer Ended) of the "My
  // Customization" panel: tri-state pickers for the slot's alert text, speech,
  // and sound. Each channel is { mode: "pkg" | "custom" | "off", value } —
  // "pkg" follows the package, "custom" replaces it, "off" silences/hides it.
  // The parent serializes these into a TriggerOverride for SetTriggerOverride.
  //
  // Alert text rewords or suppresses only — when the package shows no text in
  // this slot the row explains that instead of offering to add one (what the
  // guild's alert overlays SAY stays officer-decided). Speech and sound may
  // add as well as replace: audio is purely local. intr is the slot's
  // interrupt-speech flag: null = the package's, true/false = overridden
  // (the checkbox always shows the effective value; the Go side folds a
  // package-equal value back to null at save).
  export let ov; // { text: {mode,value}, tts: {mode,value}, sound: {mode,value}, intr }
  export let pkg; // package TriggerActionUI {use_text, display_text, use_tts, tts_text, play_media, media_file}
  export let mediaFiles = []; // available audio file names
  export let onAdd = null; // async () => addedFileName | ""
  export let onSample = null; // (name) => void

  // The package's effective values, resolved the way the engine resolves them
  // (TTS falls back to the alert text; a channel with its checkbox off is "").
  $: pkgText = pkg?.use_text ? (pkg.display_text || "").trim() : "";
  $: pkgTTS = pkg?.use_tts
    ? (pkg.tts_text || "").trim() || (pkg.display_text || "").trim()
    : "";
  $: pkgSound = (pkg?.play_media && pkg.media_file) || "";

  // Keep an assigned-but-not-local file selectable so saving doesn't drop it.
  $: soundOpts =
    ov.sound.value && !mediaFiles.includes(ov.sound.value)
      ? [ov.sound.value, ...mediaFiles]
      : mediaFiles;

  async function add() {
    if (!onAdd) return;
    const n = await onAdd();
    if (n) {
      ov.sound.value = n;
      ov = ov;
    }
  }
  function sample() {
    const f = ov.sound.mode === "custom" ? ov.sound.value : pkgSound;
    if (onSample && f) onSample(f);
  }
</script>

<div class="slot">
  <div class="row">
    <span class="lbl">Alert text</span>
    {#if pkgText}
      <select class="in sel" bind:value={ov.text.mode}>
        <option value="pkg">Package text</option>
        <option value="custom">Custom…</option>
        <option value="off">Hidden</option>
      </select>
    {:else}
      <span class="none">no package alert in this slot</span>
    {/if}
  </div>
  {#if pkgText && ov.text.mode === "pkg"}
    <div class="pkgval" title="The package text">“{pkgText}”</div>
  {/if}
  {#if pkgText && ov.text.mode === "custom"}
    <input class="in" placeholder={pkgText} bind:value={ov.text.value} />
  {/if}

  <div class="row">
    <span class="lbl">Speech</span>
    <select class="in sel" bind:value={ov.tts.mode}>
      <option value="pkg">{pkgTTS ? "Package speech" : "None (package)"}</option>
      <option value="custom">Custom…</option>
      <option value="off">Silent</option>
    </select>
  </div>
  {#if ov.tts.mode === "pkg" && pkgTTS}
    <div class="pkgval" title="The package speech">“{pkgTTS}”</div>
  {/if}
  {#if ov.tts.mode === "custom"}
    <input
      class="in"
      placeholder={pkgTTS || "Text to speak (supports ${1} captures)"}
      bind:value={ov.tts.value}
    />
  {/if}
  {#if ov.tts.mode === "custom" || (ov.tts.mode === "pkg" && pkgTTS)}
    <label
      class="intr"
      title="Cut off any speech already playing when this speaks"
    >
      <input
        type="checkbox"
        checked={ov.intr === null || ov.intr === undefined
          ? !!pkg?.tts_interrupt
          : ov.intr}
        on:change={(e) => {
          ov.intr = e.target.checked;
          ov = ov;
        }}
      />
      Interrupt other speech
    </label>
  {/if}

  <div class="row">
    <span class="lbl">Sound</span>
    <select class="in sel" bind:value={ov.sound.mode}>
      <option value="pkg">{pkgSound ? pkgSound : "None (package)"}</option>
      <option value="custom">Custom…</option>
      <option value="off">Silent</option>
    </select>
  </div>
  {#if ov.sound.mode === "custom"}
    <div class="media-row">
      <select class="in media-sel" bind:value={ov.sound.value}>
        <option value="">— none —</option>
        {#each soundOpts as m (m)}
          <option value={m}>{m}</option>
        {/each}
      </select>
      <button
        type="button"
        class="btn media-btn"
        title="Sample this sound"
        aria-label="Sample sound"
        disabled={!ov.sound.value}
        on:click={sample}
      >
        <svg viewBox="0 0 24 24" width="13" height="13" fill="currentColor">
          <path d="M8 5v14l11-7z" />
        </svg>
      </button>
      <button type="button" class="btn media-btn" on:click={add}>Add file…</button
      >
    </div>
  {/if}
</div>

<style>
  .slot {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .lbl {
    color: var(--text-secondary);
    font-size: 12px;
    width: 64px;
    flex-shrink: 0;
  }
  .sel {
    flex: 1;
    min-width: 0;
  }
  .none {
    color: var(--text-muted);
    font-size: 11px;
    font-style: italic;
  }
  /* The package value the "pkg" mode follows, shown so "keep it" is an
     informed choice rather than a mystery. */
  .pkgval {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: 72px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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
  .intr {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-left: 72px;
    color: var(--text-secondary);
    font-size: 11px;
    cursor: pointer;
  }
  .intr input {
    accent-color: var(--accent);
  }
</style>
