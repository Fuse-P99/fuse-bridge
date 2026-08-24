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
    GetTriggerMediaFilesGrouped,
    AddTriggerMediaFile,
    PlayTriggerMediaSample,
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

  // Sounds grouped by origin: the repo's Basic Sounds first (expanded), the
  // GINA-package audio and the user's own files collapsed until opened.
  const GROUP_ORDER = [
    ["library", "Basic Sounds"],
    ["gina", "From Gina"],
    ["personal", "Personal"],
  ];
  let groups = { library: [], gina: [], personal: [] };
  let open = { library: true, gina: false, personal: false };

  async function loadSounds() {
    try {
      const list = (await GetTriggerMediaFilesGrouped()) || [];
      const g = { library: [], gina: [], personal: [] };
      for (const f of list) (g[f.source] || g.personal).push(f.name);
      groups = g;
      sounds = list.map((f) => f.name);
      // Editing an alarm whose sound lives in a collapsed group: open that
      // group so the selection is visible.
      if (sound) {
        for (const [key] of GROUP_ORDER) {
          if (g[key].includes(sound)) {
            open = { ...open, [key]: true };
            break;
          }
        }
      }
    } catch {
      groups = { library: [], gina: [], personal: [] };
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
        sound = name;
        await loadSounds();
        // A hand-added file is personal — make sure its group is open so the
        // fresh selection isn't hidden.
        open = { ...open, personal: true };
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

    <div class="al-row al-row-top">
      <span class="al-lbl">Sound</span>
      <div class="al-soundcol">
        <div class="al-sndlist">
          <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
          <div
            class="al-snditem"
            class:sel={sound === ""}
            on:click={() => (sound = "")}
          >
            No sound
          </div>
          {#each GROUP_ORDER as [key, title]}
            {#if groups[key].length}
              <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
              <div class="al-sndhead" on:click={() => (open[key] = !open[key])}>
                <span class="al-sndchev">{open[key] ? "▾" : "▸"}</span>
                {title}
                <span class="al-sndcnt">({groups[key].length})</span>
              </div>
              {#if open[key]}
                {#each groups[key] as s}
                  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
                  <div
                    class="al-snditem al-indent"
                    class:sel={sound === s}
                    on:click={() => (sound = s)}
                  >
                    <span class="al-sndname">{s}</span>
                    <button
                      class="al-play"
                      title="Preview"
                      aria-label="Preview {s}"
                      on:click|stopPropagation={() => PlayTriggerMediaSample(s)}
                      >▶</button
                    >
                  </div>
                {/each}
              {/if}
            {/if}
          {/each}
        </div>
        <button class="al-btn al-addfile" on:click={addSound}>Add file…</button>
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
  .al-text {
    background: var(--bg-input, rgba(0, 0, 0, 0.25));
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 4px 8px;
    flex: 1 1 auto;
    min-width: 0;
  }
  /* Grouped sound picker: Basic Sounds / From Gina / Personal. */
  .al-row-top {
    align-items: flex-start;
  }
  .al-row-top .al-lbl {
    padding-top: 4px;
  }
  .al-soundcol {
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex: 1 1 auto;
    min-width: 0;
  }
  .al-sndlist {
    background: var(--bg-input, rgba(0, 0, 0, 0.25));
    border: 1px solid var(--border);
    border-radius: 4px;
    max-height: 200px;
    overflow-y: auto;
    padding: 3px;
  }
  .al-sndhead {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    padding: 4px 6px;
    user-select: none;
  }
  .al-sndhead:hover {
    color: var(--text-primary);
  }
  .al-sndchev {
    width: 12px;
  }
  .al-sndcnt {
    color: var(--text-muted);
    font-weight: 400;
    text-transform: none;
  }
  .al-snditem {
    display: flex;
    align-items: center;
    border-radius: 3px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 3px 6px;
    gap: 6px;
  }
  .al-snditem:hover {
    background: rgba(255, 255, 255, 0.06);
  }
  .al-snditem.sel {
    background: rgba(227, 160, 8, 0.15);
    color: #e3a008;
    font-weight: 600;
  }
  .al-indent {
    margin-left: 14px;
  }
  .al-sndname {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .al-play {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 10px;
    padding: 0 3px;
    opacity: 0;
  }
  .al-snditem:hover .al-play {
    opacity: 1;
  }
  .al-play:hover {
    color: var(--text-primary);
  }
  .al-addfile {
    align-self: flex-start;
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
