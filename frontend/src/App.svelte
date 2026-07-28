<script>
  import { onMount, onDestroy } from "svelte";
  import { slide } from "svelte/transition";
  import {
    IsAdminMode,
    IsOfficer,
    IsLinked,
    IsUpgrading,
    GetBatphones,
    GetGameClock,
  } from "../bindings/FuseBridge/app.js";
  import { Events } from "@wailsio/runtime";
  import { linked } from "./lib/linkState.js";
  import { activeTab } from "./lib/nav.js";
  import GeneralTab from "./tabs/GeneralTab.svelte";
  import CharactersTab from "./tabs/CharactersTab.svelte";
  import ZonesTab from "./tabs/ZonesTab.svelte";
  import MapTab from "./tabs/MapTab.svelte";
  import RaidsTab from "./tabs/RaidsTab.svelte";
  import TriggersTab from "./tabs/TriggersTab.svelte";
  import LogsTab from "./tabs/LogsTab.svelte";
  import ClientsTab from "./tabs/ClientsTab.svelte";
  import QuestEditor from "./lib/QuestEditor.svelte";
  import ShareInbox from "./lib/ShareInbox.svelte";
  import ConfirmDialog from "./lib/ConfirmDialog.svelte";
  import {
    GetShareInbox,
    GetSpeakers,
    GetAudioSettings,
    SetAudioVolume,
    SetAudioMuted,
  } from "../bindings/FuseBridge/app.js";
  import { scale } from "./lib/scale.js";

  let isAdmin = false;
  let isOfficer = false;
  let upgrading = false;
  let batphones = [];
  let bpTimer;

  // ── share inbox (footer, far right): pending user-to-user shares ────────────
  let shareCount = 0;
  let inboxOpen = false;
  let offShareInbox;

  // ── footer audio: master volume + quick mute ────────────────────────────────
  // Lives in the footer because it's needed WHILE something is blaring, which
  // is exactly when you don't want to go hunting for the Timers tab.
  let audioVol = 100;
  let audioMuted = false;
  async function loadAudio() {
    try {
      const a = await GetAudioSettings();
      audioVol = a.volume;
      audioMuted = a.muted;
    } catch {
      /* leave the defaults */
    }
  }
  function onVolInput() {
    SetAudioVolume(Number(audioVol));
  }
  function toggleMute() {
    audioMuted = !audioMuted;
    SetAudioMuted(audioMuted);
  }

  // ── voice footer indicator: who's talking in the guild VC + head count ──────
  let voice = { speakers: [], in_channel: 0, channel_url: "" };
  let offSpeakers;
  function normVoice(v) {
    return {
      speakers: (v && v.speakers) || [],
      in_channel: (v && v.in_channel) || 0,
      channel_url: (v && v.channel_url) || "",
    };
  }

  // ── footer clocks: Local (this PC), Server (US/Eastern, the game server's
  // "Earth Time"), and Game (the aggregated EQ in-game clock) ─────────────────
  let clockNow = Date.now();
  let gameClock = null;
  let clockTimer, clockFetchTimer;

  async function refreshGameClock() {
    try {
      gameClock = await GetGameClock();
    } catch {
      /* keep the last anchor */
    }
  }

  function fmtLocal(ms) {
    return new Date(ms).toLocaleTimeString([], {
      hour: "numeric",
      minute: "2-digit",
    });
  }
  // The in-game clock is hour-resolution, so display the hour only ("10 PM").
  // The anchor still lets us extrapolate WHICH hour it currently is and flip it
  // at the right instant between the hour-resolution /time samples.
  function fmtGame(c, ms) {
    if (!c || !c.have_game) return "—";
    const R = c.ms_per_game_hour || 180000;
    const gh = c.anchor_game_hour + (ms - c.anchor_earth_ms) / R;
    const h = ((Math.floor(gh) % 24) + 24) % 24;
    const ampm = h < 12 ? "AM" : "PM";
    let h12 = h % 12;
    if (h12 === 0) h12 = 12;
    return `${h12} ${ampm}`;
  }
  // Progress (0..1) through the current in-game hour — drives the pie that fills
  // up and resets when the hour flips.
  function gameFraction(c, ms) {
    if (!c || !c.have_game) return 0;
    const R = c.ms_per_game_hour || 180000;
    const gh = c.anchor_game_hour + (ms - c.anchor_earth_ms) / R;
    return ((gh % 1) + 1) % 1;
  }

  $: localStr = fmtLocal(clockNow);
  $: gameStr = fmtGame(gameClock, clockNow);
  $: gameFrac = gameFraction(gameClock, clockNow);

  // Banners expire after ~5 minutes server-side, so minute resolution is all
  // that's useful here; under a minute reads as "just now" rather than "0m ago".
  // Clamped at zero because sent_at is the server's clock, not ours.
  function bpAge(sentAt, now) {
    if (!sentAt) return "";
    const secs = Math.max(0, Math.floor((now - sentAt) / 1000));
    if (secs < 60) return "just now";
    return `${Math.floor(secs / 60)}m ago`;
  }

  async function pollBatphones() {
    if (!$linked) {
      batphones = [];
      return;
    }
    try {
      batphones = (await GetBatphones()) || [];
    } catch {
      batphones = [];
    }
  }

  // Ordered with the tabs non-guild-members can use up front (General,
  // Characters, Map work even unlinked). Gated tabs are admin/officer-only
  // for now (Timers is in small-group testing).
  const allTabs = [
    { id: "general", label: "General" },
    { id: "logs", label: "Logs" },
    { id: "characters", label: "Characters" },
    { id: "zones", label: "Zones" },
    { id: "map", label: "Map" },
    { id: "raids", label: "Raids" },
    { id: "triggers", label: "Timers", gated: true },
    { id: "clients", label: "Clients", gated: true },
    // admin, not gated: Quests writes the shared item and mob tables, so it
    // stays with admin mode rather than opening to every officer. The server
    // enforces officer-only on the writes regardless.
    { id: "quests", label: "Quests", admin: true },
  ];

  onMount(async () => {
    isAdmin = await IsAdminMode();
    linked.set(await IsLinked());
    upgrading = await IsUpgrading();
    Events.On("upgrading", () => {
      upgrading = true;
    });
    // A failed update backs out of the upgrade screen instead of hanging on
    // the spinner; the error itself shows in the General tab's activity feed.
    Events.On("upgrade-failed", () => {
      upgrading = false;
    });
    await pollBatphones();
    bpTimer = setInterval(pollBatphones, 8000);
    await refreshGameClock();
    clockTimer = setInterval(() => (clockNow = Date.now()), 1000);
    clockFetchTimer = setInterval(refreshGameClock, 20000);
    // Share inbox badge: seed from the Go-side cache, then live-update off the
    // poller's event (payload is the pending count).
    try {
      shareCount = ((await GetShareInbox()) || []).length;
    } catch {
      shareCount = 0;
    }
    offShareInbox = Events.On("share-inbox", (ev) => {
      const d = ev && ev.data;
      shareCount = Array.isArray(d) ? (d[0] ?? 0) : (d ?? 0);
    });
    loadAudio();
    // Voice speaking indicator: seed from the Go cache, then live-update.
    try {
      voice = normVoice(await GetSpeakers());
    } catch {
      /* leave empty */
    }
    offSpeakers = Events.On("speakers", (ev) => {
      const d = ev && ev.data;
      const v = Array.isArray(d) ? d[0] : d;
      if (v) voice = normVoice(v);
    });
  });
  onDestroy(() => {
    clearInterval(bpTimer);
    clearInterval(clockTimer);
    clearInterval(clockFetchTimer);
    if (offShareInbox) offShareInbox();
    if (offSpeakers) offSpeakers();
  });

  // Officer status reveals the Clients tab too; it needs a linked member, so
  // resolve it once linking is confirmed (covers linking after initial mount).
  let officerChecked = false;
  $: if ($linked && !officerChecked) {
    officerChecked = true;
    IsOfficer().then((v) => (isOfficer = v));
  }

  $: tabs = allTabs.filter(
    (t) => (!t.gated || isAdmin || isOfficer) && (!t.admin || isAdmin),
  );

  // If the current tab disappears (e.g. admin toggling), fall back to General.
  $: if (!tabs.find((t) => t.id === $activeTab)) activeTab.set("general");
</script>

{#if upgrading}
  <div class="upgrade">
    <img class="upgrade-icon" src="/FuseIcon2.png" alt="Fuse Bridge" />
    <div class="upgrade-title">Upgrading your Fuse Bridge client…</div>
    <div class="upgrade-sub">
      A new version is being installed. The app will restart automatically —
      this only takes a moment.
    </div>
    <div class="upgrade-spinner"></div>
  </div>
{:else}
  <div class="shell" style="zoom:{$scale}; height:calc(100vh / {$scale})">
    <nav class="tab-bar">
      {#each tabs as t}
        <button
          class="tab-btn"
          class:active={$activeTab === t.id}
          on:click={() => activeTab.set(t.id)}>{t.label}</button
        >
      {/each}

      <div class="size-btns">
        <button
          class="size-btn size-s"
          class:active={$scale === 1.0}
          on:click={() => ($scale = 1.0)}
          title="Small">A</button
        >
        <button
          class="size-btn size-m"
          class:active={$scale === 1.2}
          on:click={() => ($scale = 1.2)}
          title="Medium">A</button
        >
        <button
          class="size-btn size-l"
          class:active={$scale === 1.4}
          on:click={() => ($scale = 1.4)}
          title="Large">A</button
        >
      </div>
    </nav>

    {#if !$linked}
      <div class="unlinked-bar" transition:slide|local>
        <span class="ul-tag">NOT LINKED</span>
        Link your Discord account to enable zones, timers, raids and guildmate maps.
        <button class="ul-link" on:click={() => activeTab.set("general")}
          >Link now →</button
        >
      </div>
    {/if}

    {#if batphones.length}
      <div class="batphone-bar" transition:slide|local>
        {#each batphones as b}
          <div class="bp-line">
            <span class="bp-body">
              <span class="bp-tag">BATPHONE</span>
              {b.text}
            </span>
            {#if b.sent_at}
              <span class="bp-age">{bpAge(b.sent_at, clockNow)}</span>
            {/if}
          </div>
        {/each}
      </div>
    {/if}

    <main class="tab-content">
      {#if $activeTab === "general"}
        <GeneralTab />
      {:else if $activeTab === "characters"}
        <CharactersTab />
      {:else if $activeTab === "zones"}
        <ZonesTab />
      {:else if $activeTab === "map"}
        <MapTab />
      {:else if $activeTab === "raids"}
        <RaidsTab />
      {:else if $activeTab === "triggers"}
        <TriggersTab />
      {:else if $activeTab === "logs"}
        <LogsTab />
      {:else if $activeTab === "clients"}
        <ClientsTab />
      {:else if $activeTab === "quests"}
        <QuestEditor embedded />
      {/if}
    </main>

    <footer class="app-footer">
      <span class="clk"><span class="clk-l">Local</span>{localStr}</span>
      <span class="clk">
        <span class="clk-l">Game</span>{gameStr}
        {#if gameClock && gameClock.have_game}
          <span
            class="game-pie"
            style="background: conic-gradient(var(--accent) {gameFrac *
              360}deg, rgba(255, 255, 255, 0.12) 0)"
            title="Progress through the current game hour"
          ></span>
        {/if}
      </span>
      <span class="foot-right">
        {#if voice.channel_url || voice.in_channel > 0 || voice.speakers.length}
          <span class="vc-cluster">
            <!-- Named rather than a speaker icon: a 🔊 next to a volume slider
                 reads as "this is the volume", which it never was. Shown even
                 with the channel empty — that's when you want to join it. -->
            {#if voice.channel_url}
              <a
                class="vc-link"
                href={voice.channel_url}
                target="_blank"
                rel="noreferrer"
                title="Open the guild raid voice channel in Discord">#raid-voice</a
              >
            {:else}
              <span class="vc-link vc-link-off" title="Guild voice channel"
                >#raid-voice</span
              >
            {/if}
            {#each voice.speakers as s (s.id)}
              {#if s.avatar}
                <img
                  class="vc-av"
                  src={s.avatar}
                  alt={s.name}
                  title={s.name}
                  on:error={(e) => (e.currentTarget.style.display = "none")}
                />
              {:else}
                <span class="vc-av vc-letter" title={s.name}
                  >{(s.name || "?").slice(0, 1)}</span
                >
              {/if}
            {/each}
            {#if voice.speakers.length}
              <span class="vc-name">{voice.speakers[0].name}</span>
            {/if}
            {#if voice.in_channel > 0}
              <span class="vc-count" title="People in the voice channel"
                >({voice.in_channel})</span
              >
            {/if}
          </span>
        {/if}

        <!-- Master volume. Here because a trigger storm is the moment you need
             it, and hunting through tabs while it blares is the problem. -->
        <span class="vol" class:muted={audioMuted}>
          <button
            class="vol-btn"
            title={audioMuted
              ? "Audio muted — click to unmute"
              : "Mute all trigger audio (TTS + sounds)"}
            aria-label={audioMuted ? "Unmute audio" : "Mute audio"}
            on:click={toggleMute}
          >
            <svg
              viewBox="0 0 24 24"
              width="14"
              height="14"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path d="M11 5 6 9H2v6h4l5 4V5z" />
              {#if audioMuted}
                <line x1="23" y1="9" x2="17" y2="15" />
                <line x1="17" y1="9" x2="23" y2="15" />
              {:else}
                <path d="M15.5 8.5a5 5 0 0 1 0 7" />
              {/if}
            </svg>
          </button>
          <input
            class="vol-range"
            type="range"
            min="0"
            max="100"
            bind:value={audioVol}
            on:input={onVolInput}
            disabled={audioMuted}
            aria-label="Audio volume"
            title="Trigger audio volume"
          />
        </span>

        <button
          class="inbox-btn"
          class:has-mail={shareCount > 0}
          title="Shared with you — triggers and map markers other players sent"
          on:click={() => (inboxOpen = !inboxOpen)}
        >
          ✉
          {#if shareCount > 0}<span class="inbox-count">{shareCount}</span>{/if}
        </button>
      </span>
    </footer>

    {#if inboxOpen}
      <ShareInbox onClose={() => (inboxOpen = false)} />
    {/if}
  </div>
{/if}

<!-- App-wide confirmation modal — outside the upgrade branch so it's mounted
     whatever the app is showing. Driven by confirmDialog() in lib/confirm.js. -->
<ConfirmDialog />

<style>
  .upgrade {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 14px;
    height: 100vh;
    padding: 24px;
    text-align: center;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
      sans-serif;
    background: var(--bg-secondary);
  }
  .upgrade-icon {
    width: 72px;
    height: 72px;
    opacity: 0.95;
  }
  .upgrade-title {
    font-size: 17px;
    font-weight: 600;
    color: var(--text-primary);
  }
  .upgrade-sub {
    font-size: 13px;
    color: var(--text-secondary);
    max-width: 420px;
    line-height: 1.5;
  }
  .upgrade-spinner {
    margin-top: 6px;
    width: 22px;
    height: 22px;
    border: 3px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: upgrade-spin 0.8s linear infinite;
  }
  @keyframes upgrade-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .shell {
    position: relative; /* anchors the share-inbox panel to the window */
    display: flex;
    flex-direction: column;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
      sans-serif;
    font-size: 13px;
  }

  .tab-bar {
    display: flex;
    align-items: center;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
    -webkit-app-region: drag;
  }

  .tab-btn {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 13px;
    padding: 10px 20px;
    transition:
      color 0.15s,
      border-color 0.15s;
    -webkit-app-region: no-drag;
  }

  .tab-btn:hover {
    color: var(--text-primary);
  }
  .tab-btn.active {
    color: var(--accent);
    border-bottom-color: var(--accent);
  }

  .size-btns {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 10px;
    -webkit-app-region: no-drag;
  }

  .size-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-family: inherit;
    font-weight: 600;
    line-height: 1;
    padding: 2px 4px;
    border-radius: 3px;
    transition: color 0.15s;
  }
  .size-btn:hover {
    color: var(--text-primary);
  }
  .size-btn.active {
    color: var(--accent);
  }

  .size-s {
    font-size: 10px;
  }
  .size-m {
    font-size: 13px;
  }
  .size-l {
    font-size: 17px;
  }

  .tab-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .unlinked-bar {
    flex-shrink: 0;
    background: rgba(96, 165, 250, 0.12);
    border-bottom: 1px solid rgba(96, 165, 250, 0.55);
    padding: 7px 14px;
    font-size: 12.5px;
    color: var(--text-primary);
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .ul-tag {
    color: #60a5fa;
    font-weight: 800;
    font-size: 10px;
    letter-spacing: 0.08em;
  }
  .ul-link {
    margin-left: auto;
    background: none;
    border: 1px solid rgba(96, 165, 250, 0.55);
    color: #60a5fa;
    border-radius: 4px;
    padding: 2px 10px;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
  }
  .ul-link:hover {
    background: rgba(96, 165, 250, 0.15);
  }

  .batphone-bar {
    flex-shrink: 0;
    background: rgba(227, 160, 8, 0.16);
    border-bottom: 1px solid #e3a008;
    padding: 8px 14px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .bp-line {
    display: flex;
    align-items: baseline;
    gap: 10px;
    font-size: 13px;
    color: var(--text-primary);
  }
  .bp-body {
    flex: 1;
    min-width: 0;
  }
  .bp-age {
    flex-shrink: 0;
    font-size: 11px;
    color: #e3a008;
    opacity: 0.85;
    white-space: nowrap;
  }
  .bp-tag {
    color: #e3a008;
    font-weight: 800;
    font-size: 10px;
    letter-spacing: 0.08em;
    margin-right: 8px;
  }

  .app-footer {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 3px 12px;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
    font-size: 12px;
    font-family: Verdana, Geneva, Tahoma, sans-serif;
    color: var(--text-secondary);
    white-space: nowrap;
    overflow: hidden;
  }
  .clk {
    display: inline-flex;
    align-items: baseline;
    gap: 5px;
  }
  .clk-l {
    color: var(--text-muted);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
      sans-serif;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .game-pie {
    width: 11px;
    height: 11px;
    border-radius: 50%;
    flex-shrink: 0;
    align-self: center;
    box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.18);
  }

  /* Share-inbox button, far lower right of the footer. */
  /* Right side of the footer: voice speaking indicator + share inbox. The
     container carries the flex auto-margin so the inbox stays far right even
     when the voice cluster is hidden. */
  .foot-right {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
  }
  .vc-cluster {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
  }
  /* The channel by name, so it can't be mistaken for the volume control. */
  .vc-link {
    font-size: 11px;
    color: var(--text-secondary);
    text-decoration: none;
    white-space: nowrap;
  }
  a.vc-link:hover {
    color: var(--accent);
    text-decoration: underline;
  }
  .vc-link-off {
    color: var(--text-muted);
  }

  /* Master volume: mute button + slider, far right beside the inbox. */
  .vol {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .vol-btn {
    display: flex;
    align-items: center;
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    color: var(--text-secondary);
  }
  .vol-btn:hover {
    color: var(--text-primary);
  }
  .vol.muted .vol-btn {
    color: #e0645c;
  }
  .vol-range {
    width: 62px;
    height: 3px;
    accent-color: var(--accent);
    cursor: pointer;
  }
  .vol-range:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .vc-av {
    width: 16px;
    height: 16px;
    border-radius: 50%;
    object-fit: cover;
    border: 1px solid var(--success);
    box-shadow: 0 0 4px rgba(76, 175, 80, 0.55);
    flex-shrink: 0;
  }
  .vc-letter {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--bg-panel);
    color: var(--text-secondary);
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
  }
  .vc-name {
    font-size: 12px;
    color: var(--text-secondary);
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .vc-count {
    font-size: 11px;
    color: var(--text-muted);
  }
  .inbox-btn {
    position: relative;
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 14px;
    line-height: 1;
    padding: 0 2px;
  }
  .inbox-btn:hover {
    color: var(--text-primary);
  }
  .inbox-btn.has-mail {
    color: #e3a008;
  }
  .inbox-count {
    position: absolute;
    top: -5px;
    right: -7px;
    min-width: 13px;
    height: 13px;
    padding: 0 3px;
    border-radius: 7px;
    background: #dc2626;
    color: #fff;
    font-size: 9px;
    font-weight: 800;
    line-height: 13px;
    text-align: center;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
      sans-serif;
  }
</style>
