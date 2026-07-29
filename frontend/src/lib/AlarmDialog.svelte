<script>
  // Set / edit / delete one reminder on the server-wide timers board.
  //
  // Opened from the bell beside a board entry. Two shapes, driven by `kind`:
  // a "lead" alarm fires a chosen number of minutes before a predicted moment,
  // while an "occurrence" alarm (the earthquake — the one thing here nobody can
  // schedule) fires when it has happened, so the lead control is meaningless
  // and hidden rather than shown greyed out.
  import { createEventDispatcher } from "svelte";
  import {
    SetWorldAlarm,
    DeleteWorldAlarm,
    TestWorldAlarm,
    GetTriggerMediaFiles,
    AddTriggerMediaFile,
  } from "../../bindings/FuseBridge/app.js";

  export let alarmKey = "";
  export let label = "";
  export let kind = "lead"; // "lead" | "occurrence"
  export let existing = null; // the saved alarm, or null for a new one

  const dispatch = createEventDispatcher();

  let leadMin = existing ? Math.round((existing.lead_ms || 0) / 60000) : 5;
  let sound = existing?.sound || "";
  let speak = existing ? !!existing.speak : true;
  let speakText = existing?.speak_text || "";
  let repeat = existing ? !!existing.repeat : true;
  let sounds = [];
  let err = "";
  let busy = false;

  const LEAD_PRESETS = [0, 1, 2, 5, 10, 15, 30];

  async function loadSounds() {
    try {
      sounds = (await GetTriggerMediaFiles()) || [];
    } catch {
      sounds = [];
    }
  }
  loadSounds();

  function build() {
    return {
      key: alarmKey,
      label,
      lead_ms: kind === "occurrence" ? 0 : Math.max(0, leadMin) * 60000,
      sound,
      speak,
      speak_text: speakText.trim(),
      repeat,
      fired_for: 0,
    };
  }

  async function addSound() {
    try {
      const name = await AddTriggerMediaFile();
      if (name) {
        await loadSounds();
        sound = name;
      }
    } catch (e) {
      err = String(e).replace(/^Error:\s*/i, "");
    }
  }

  async function save() {
    busy = true;
    err = "";
    try {
      await SetWorldAlarm(build());
      dispatch("saved");
    } catch (e) {
      // The Go side returns plain-language reasons ("choose a sound,
      // text-to-speech, or both"), so show them as written.
      err = String(e).replace(/^Error:\s*/i, "");
    }
    busy = false;
  }

  async function remove() {
    busy = true;
    try {
      await DeleteWorldAlarm(alarmKey);
      dispatch("saved");
    } catch (e) {
      err = String(e).replace(/^Error:\s*/i, "");
    }
    busy = false;
  }

  // Preview uses the form as it stands, not what's saved — the point is to hear
  // the change you just made.
  const test = () => TestWorldAlarm(build());
</script>

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="al-scrim" on:click={() => dispatch("close")}>
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="al-box" on:click|stopPropagation>
    <div class="al-title">
      {existing ? "Edit reminder" : "Set reminder"}
      <span class="al-for">{label}</span>
    </div>

    {#if kind === "occurrence"}
      <div class="al-note">
        An earthquake can't be predicted, so this fires as soon as one is
        reported rather than ahead of time.
      </div>
    {:else}
      <div class="al-row">
        <span class="al-lbl">Warn me</span>
        <div class="al-leads">
          {#each LEAD_PRESETS as m}
            <button
              class="al-chip"
              class:on={leadMin === m}
              on:click={() => (leadMin = m)}
              >{m === 0 ? "At the event" : `${m}m`}</button
            >
          {/each}
          <input
            class="al-num"
            type="number"
            min="0"
            max="240"
            bind:value={leadMin}
            aria-label="Minutes before"
          />
          <span class="al-unit">min before</span>
        </div>
      </div>
    {/if}

    <div class="al-row">
      <span class="al-lbl">Sound</span>
      <div class="al-inline">
        <select class="al-sel" bind:value={sound}>
          <option value="">No sound</option>
          {#each sounds as s}
            <option value={s}>{s}</option>
          {/each}
        </select>
        <button class="al-btn" on:click={addSound}>Add file…</button>
      </div>
    </div>
    {#if !sounds.length}
      <div class="al-hint">
        No audio files yet. "Add file…" copies an .mp3 or .wav into the client's
        media folder — the same library the triggers use, so anything already
        there shows up here.
      </div>
    {/if}

    <div class="al-row">
      <span class="al-lbl">Speak</span>
      <div class="al-inline">
        <label class="al-check">
          <input type="checkbox" bind:checked={speak} /> Read it aloud
        </label>
      </div>
    </div>
    {#if speak}
      <div class="al-row">
        <span class="al-lbl"></span>
        <input
          class="al-text"
          bind:value={speakText}
          placeholder={kind === "occurrence"
            ? "Earthquake"
            : leadMin === 0
              ? `${label} now`
              : `${label} in ${leadMin} minutes`}
          aria-label="Spoken text"
        />
      </div>
    {/if}

    <div class="al-row">
      <span class="al-lbl">Repeat</span>
      <div class="al-inline">
        <button class="al-chip" class:on={repeat} on:click={() => (repeat = true)}
          >Every time</button
        >
        <button
          class="al-chip"
          class:on={!repeat}
          on:click={() => (repeat = false)}>Once, then remove</button
        >
      </div>
    </div>

    {#if err}<div class="al-err">{err}</div>{/if}

    <div class="al-actions">
      <button class="al-btn" on:click={test}>Test</button>
      <span class="al-spacer"></span>
      {#if existing}
        <button class="al-btn al-del" on:click={remove} disabled={busy}
          >Delete</button
        >
      {/if}
      <button class="al-btn" on:click={() => dispatch("close")}>Cancel</button>
      <button class="al-btn al-save" on:click={save} disabled={busy}>Save</button>
    </div>
  </div>
</div>

<style>
  .al-scrim {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 60;
  }
  .al-box {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px 18px;
    width: min(520px, 92vw);
    max-height: 86vh;
    overflow-y: auto;
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.5);
  }
  .al-title {
    color: var(--text-primary);
    font-size: 14px;
    font-weight: 700;
    margin-bottom: 12px;
  }
  .al-for {
    color: #e3a008;
    font-weight: 600;
    margin-left: 8px;
  }
  .al-note,
  .al-hint {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.5;
    margin: 0 0 10px;
  }
  .al-hint {
    margin-left: 84px;
  }
  .al-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;
  }
  .al-lbl {
    flex: 0 0 74px;
    color: var(--text-secondary);
    font-size: 11px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .al-inline,
  .al-leads {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    flex: 1 1 auto;
    min-width: 0;
  }
  .al-chip {
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 3px 10px;
  }
  .al-chip:hover {
    color: var(--text-primary);
  }
  .al-chip.on {
    background: rgba(227, 160, 8, 0.15);
    border-color: #e3a008;
    color: #e3a008;
    font-weight: 600;
  }
  .al-num {
    background: var(--bg-input, rgba(0, 0, 0, 0.25));
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 3px 6px;
    width: 62px;
  }
  .al-unit {
    color: var(--text-muted);
    font-size: 11px;
  }
  .al-sel,
  .al-text {
    background: var(--bg-input, rgba(0, 0, 0, 0.25));
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 4px 8px;
  }
  .al-sel {
    flex: 1 1 auto;
    min-width: 0;
  }
  .al-text {
    flex: 1 1 auto;
    min-width: 0;
  }
  .al-check {
    align-items: center;
    color: var(--text-primary);
    display: flex;
    font-size: 12px;
    gap: 6px;
  }
  .al-err {
    color: var(--error, #ff8a8a);
    font-size: 11px;
    margin: 6px 0 0 84px;
  }
  .al-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 16px;
  }
  .al-spacer {
    flex: 1 1 auto;
  }
  .al-btn {
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 12px;
    padding: 5px 12px;
  }
  .al-btn:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.06);
    color: var(--text-primary);
  }
  .al-btn:disabled {
    cursor: default;
    opacity: 0.5;
  }
  .al-save {
    border-color: #e3a008;
    color: #e3a008;
  }
  .al-del {
    border-color: #8a3b3b;
    color: #ff8a8a;
  }
</style>
