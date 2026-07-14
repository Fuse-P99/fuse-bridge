<script>
  import { onMount, onDestroy } from "svelte";
  import { slide } from "svelte/transition";
  import {
    IsAdminMode,
    IsOfficer,
    IsLinked,
    IsUpgrading,
    GetBatphones,
    GetVoiceSpeaker,
  } from "../wailsjs/go/main/App";
  import { EventsOn } from "../wailsjs/runtime/runtime";
  import { linked } from "./lib/linkState.js";
  import { activeTab } from "./lib/nav.js";
  import GeneralTab from "./tabs/GeneralTab.svelte";
  import RelayTab from "./tabs/RelayTab.svelte";
  import CharactersTab from "./tabs/CharactersTab.svelte";
  import ZonesTab from "./tabs/ZonesTab.svelte";
  import MapTab from "./tabs/MapTab.svelte";
  import RaidsTab from "./tabs/RaidsTab.svelte";
  import ClientsTab from "./tabs/ClientsTab.svelte";
  import { scale } from "./lib/scale.js";

  let isAdmin = false;
  let isOfficer = false;
  let upgrading = false;
  let batphones = [];
  let bpTimer;
  let speaker = { name: "", speaking: false }; // current raid-voice speaker
  let voiceTimer;

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

  // Poll the current raid-voice speaker for the footer indicator (linked only).
  async function pollVoiceSpeaker() {
    if (!$linked) {
      speaker = { name: "", speaking: false };
      return;
    }
    try {
      speaker = (await GetVoiceSpeaker()) || { name: "", speaking: false };
    } catch {
      speaker = { name: "", speaking: false };
    }
  }

  const baseTabs = [
    { id: "general", label: "General" },
    { id: "relay", label: "Relay" },
    { id: "characters", label: "Characters" },
    { id: "zones", label: "Zones" },
    { id: "map", label: "Map" },
    { id: "raids", label: "Raids" },
  ];

  onMount(async () => {
    isAdmin = await IsAdminMode();
    linked.set(await IsLinked());
    upgrading = await IsUpgrading();
    EventsOn("upgrading", () => {
      upgrading = true;
    });
    // The Go client fires this when the player spams /loc (5 within 5s).
    EventsOn("open-map", () => activeTab.set("map"));
    await pollBatphones();
    bpTimer = setInterval(pollBatphones, 8000);
    await pollVoiceSpeaker();
    voiceTimer = setInterval(pollVoiceSpeaker, 1500);
  });
  onDestroy(() => {
    clearInterval(bpTimer);
    clearInterval(voiceTimer);
  });

  // Officer status reveals the Clients tab too; it needs a linked member, so
  // resolve it once linking is confirmed (covers linking after initial mount).
  let officerChecked = false;
  $: if ($linked && !officerChecked) {
    officerChecked = true;
    IsOfficer().then((v) => (isOfficer = v));
  }

  // Clients tab is visible to admins OR officers; everything else stays admin-only.
  $: tabs =
    isAdmin || isOfficer
      ? [...baseTabs, { id: "clients", label: "Clients" }]
      : baseTabs;

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

    {#if batphones.length}
      <div class="batphone-bar" transition:slide|local>
        {#each batphones as b}
          <div class="bp-line">
            <span class="bp-tag">BATPHONE</span>
            {b.text}
          </div>
        {/each}
      </div>
    {/if}

    <main class="tab-content">
      {#if $activeTab === "general"}
        <GeneralTab />
      {:else if $activeTab === "relay"}
        <RelayTab />
      {:else if $activeTab === "characters"}
        <CharactersTab />
      {:else if $activeTab === "zones"}
        <ZonesTab />
      {:else if $activeTab === "map"}
        <MapTab />
      {:else if $activeTab === "raids"}
        <RaidsTab />
      {:else if $activeTab === "clients"}
        <ClientsTab />
      {/if}
    </main>

    {#if $linked}
      <footer class="app-footer">
        {#if speaker.speaking && speaker.name}
          <span class="af-name">{speaker.name}</span>
        {/if}
        <svg
          class="af-spk"
          class:live={speaker.speaking}
          viewBox="0 0 24 24"
          width="15"
          height="15"
          aria-hidden="true"
          title="Raid voice"
        >
          <path d="M4 9v6h4l5 4V5L8 9H4z" fill="currentColor" />
          <path
            d="M16.5 8.5a4.5 4.5 0 0 1 0 7M18.8 6a8 8 0 0 1 0 12"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
          />
        </svg>
      </footer>
    {/if}
  </div>
{/if}

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

  .app-footer {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    padding: 5px 12px;
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
  }
  .af-name {
    font-size: 12px;
    color: var(--text-primary);
    white-space: nowrap;
    max-width: 60%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* Speaker icon: grey when idle, white when someone is speaking. */
  .af-spk {
    color: var(--text-muted);
    flex-shrink: 0;
    transition: color 0.15s;
  }
  .af-spk.live {
    color: var(--text-primary);
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
    font-size: 13px;
    color: var(--text-primary);
  }
  .bp-tag {
    color: #e3a008;
    font-weight: 800;
    font-size: 10px;
    letter-spacing: 0.08em;
    margin-right: 8px;
  }
</style>
