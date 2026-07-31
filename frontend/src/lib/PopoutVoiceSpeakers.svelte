<script>
  // Voice Speakers special overlay: who is talking in the guild voice channel
  // right now (avatar + name per speaker, most recent first) and the channel
  // head count. Data comes from the Go speaker poller — seeded via GetSpeakers,
  // live-updated by the app-wide "speakers" event. Linked clients only: the
  // poller never has data for unlinked installs, so this just shows quiet.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { GetSpeakers } from "../../bindings/FuseBridge/app.js";

  export let hasContent = false; // drives the "Hide When 0" title-bar mode

  let voice = { speakers: [], in_channel: 0 };
  let off;

  function norm(v) {
    return {
      speakers: (v && v.speakers) || [],
      in_channel: (v && v.in_channel) || 0,
    };
  }
  $: hasContent = voice.speakers.length > 0;

  onMount(async () => {
    try {
      voice = norm(await GetSpeakers());
    } catch {
      /* leave empty */
    }
    off = Events.On("speakers", (ev) => {
      const d = ev && ev.data;
      const v = Array.isArray(d) ? d[0] : d;
      if (v) voice = norm(v);
    });
  });
  onDestroy(() => off && off());
</script>

<div class="vs">
  <div class="vs-head">
    <span class="vs-ico">🔊</span>
    <span class="vs-label">Voice</span>
    <span class="vs-count" title="People in the voice channel"
      >({voice.in_channel})</span
    >
  </div>
  {#if voice.speakers.length}
    <div class="vs-list">
      {#each voice.speakers as s (s.id)}
        <div class="vs-row">
          {#if s.avatar}
            <img
              class="vs-av"
              src={s.avatar}
              alt={s.name}
              on:error={(e) => (e.currentTarget.style.display = "none")}
            />
          {:else}
            <span class="vs-av vs-letter">{(s.name || "?").slice(0, 1)}</span>
          {/if}
          <span class="vs-name">{s.name}</span>
        </div>
      {/each}
    </div>
  {:else}
    <div class="vs-quiet">Nobody speaking</div>
  {/if}
</div>

<style>
  .vs {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 6px 8px;
    overflow-y: auto;
    /* Readable over the game even on a transparent background. */
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
  .vs-head {
    display: flex;
    align-items: center;
    gap: 5px;
    flex-shrink: 0;
  }
  .vs-ico {
    font-size: 12px;
  }
  .vs-label {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-secondary);
  }
  .vs-count {
    font-size: 11px;
    color: var(--text-muted);
    font-family: var(--font-mono);
  }
  .vs-list {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .vs-row {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }
  .vs-av {
    width: 18px;
    height: 18px;
    border-radius: 50%;
    object-fit: cover;
    border: 1px solid var(--success);
    box-shadow: 0 0 5px rgba(76, 175, 80, 0.6);
    flex-shrink: 0;
  }
  .vs-letter {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--bg-panel);
    color: var(--text-secondary);
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
  }
  .vs-name {
    font-size: 12.5px;
    font-weight: 600;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .vs-quiet {
    font-size: 11px;
    font-style: italic;
    color: var(--text-muted);
  }
</style>
