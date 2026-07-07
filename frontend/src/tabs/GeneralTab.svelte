<script>
  import { onMount, onDestroy } from "svelte";
  import {
    GetStatus,
    GetSettings,
    SaveSettings,
    GetAutoStart,
    SetAutoStart,
    BrowseEQDirectory,
    GetAvailableUpdate,
    StartUpdate,
  } from "../../wailsjs/go/main/App";
  import AccountLink from "../lib/AccountLink.svelte";

  let status = {
    eq_running: false,
    configured: false,
    log_file: "",
    connected: false,
    activity: [],
    version: "",
  };
  let autoStart = false;
  let eqDir = "";
  let useMiddlemand = false;
  let updateVersion = "";
  let updating = false;
  let interval;
  let updateInterval;

  async function refresh() {
    status = await GetStatus();
  }

  async function checkUpdate() {
    try {
      updateVersion = (await GetAvailableUpdate()) || "";
    } catch {
      updateVersion = "";
    }
  }

  async function doUpdate() {
    updating = true;
    await StartUpdate(); // flips the app to the upgrade screen, then restarts
  }

  onMount(async () => {
    autoStart = await GetAutoStart();
    const s = await GetSettings();
    eqDir = s.eq_directory || "";
    useMiddlemand = !!s.use_middlemand;
    await refresh();
    interval = setInterval(refresh, 2000);
    checkUpdate();
    updateInterval = setInterval(checkUpdate, 10 * 60 * 1000);
  });

  onDestroy(() => {
    clearInterval(interval);
    clearInterval(updateInterval);
  });

  async function toggleAutoStart() {
    await SetAutoStart(!autoStart);
    autoStart = !autoStart;
  }

  async function toggleMiddlemand() {
    // Re-fetch so we never clobber settings changed on another tab.
    const s = await GetSettings();
    s.use_middlemand = !useMiddlemand;
    await SaveSettings(s);
    useMiddlemand = s.use_middlemand;
  }

  let dirError = "";
  async function browseDir() {
    const result = await BrowseEQDirectory();
    if (result === "INVALID") {
      dirError =
        "That folder doesn't look like an EverQuest install (no eqgame.exe found). Pick the folder containing eqgame.exe.";
      return;
    }
    if (result) {
      eqDir = result;
      dirError = "";
    }
  }

  function baseName(p) {
    if (!p) return "None";
    return p.replace(/.*[\\/]/, "");
  }
</script>

<div class="general">
  <!-- Status panel -->
  <div class="panel">
    <div class="panel-body">
      <div class="status-rows">
        <div class="status-row">
          <span class="label">EverQuest</span>
          <span
            class="dot"
            class:green={status.configured}
            class:red={!status.configured}
          ></span>
          <span
            class="badge-text"
            class:green={status.configured}
            class:red={!status.configured}
          >
            {status.configured ? "Configured" : "Not Found"}
          </span>
          {#if !status.configured}
            <span class="hint">Set EverQuest directory below…</span>
          {/if}
        </div>
        <div class="status-row">
          <span class="label">Log File</span>
          <span class="mono dim">{baseName(status.log_file)}</span>
        </div>
        <div class="status-row">
          <span class="label">Server</span>
          <span
            class="dot"
            class:green={status.connected}
            class:red={!status.connected}
          ></span>
          <span
            class="badge-text"
            class:green={status.connected}
            class:red={!status.connected}
          >
            {status.connected ? "Connected" : "Not connected"}
          </span>
        </div>
        <div class="status-row">
          <span class="label">Version</span>
          <span class="dim">Fuse Bridge v{status.version}</span>
          {#if updateVersion}
            <button
              class="btn update-btn"
              disabled={updating}
              on:click={doUpdate}
            >
              {updating ? "Updating…" : `Update to v${updateVersion}`}
            </button>
          {/if}
        </div>
      </div>
      <img class="app-icon" src="/FuseIcon2.png" alt="Fuse Bridge" />
    </div>
  </div>

  <div class="sep" />

  <!-- Account linking -->
  <AccountLink />

  <div class="sep" />

  <!-- Startup / directory -->
  <label class="check-label">
    <input type="checkbox" checked={autoStart} on:change={toggleAutoStart} />
    Start automatically with Windows
  </label>

  <label
    class="check-label"
    title="Runs the built-in P99 login proxy (p99-login-middlemand) and points eqhost.txt at it. Fixes the 'server list fails to populate' login issue. Unchecking restores your original eqhost.txt."
  >
    <input
      type="checkbox"
      checked={useMiddlemand}
      on:change={toggleMiddlemand}
    />
    Use middlemand (login fix — filters server list to P99, manages eqhost.txt)
  </label>

  <div class="dir-row">
    <span class="label">EQ Directory</span>
    <div class="dir-box">
      <span class="mono dim dir-path" title={eqDir}>{eqDir || "Not set"}</span>
      <button class="btn" on:click={browseDir}>Browse…</button>
    </div>
  </div>
  {#if dirError}
    <div class="dir-error">{dirError}</div>
  {/if}

  <div class="sep" />

  <!-- Activity log -->
  <div class="log-wrap">
    <div class="section-label">Activity</div>
    <div class="log">
      {#each status.activity as line}
        <div class="log-line">{line}</div>
      {/each}
    </div>
  </div>
</div>

<style>
  .general {
    display: flex;
    flex-direction: column;
    height: 100%;
    padding: 14px 16px;
    gap: 0;
  }

  .panel {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px 14px;
  }

  .panel-body {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .status-rows {
    display: flex;
    flex-direction: column;
    gap: 7px;
    flex: 1;
  }

  .app-icon {
    width: 72px;
    height: 72px;
    flex-shrink: 0;
    opacity: 0.9;
  }

  .status-row {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
  }

  .label {
    color: var(--text-muted);
    min-width: 82px;
  }
  .dim {
    color: var(--text-secondary);
  }
  .mono {
    font-family: var(--font-mono);
    font-size: 11px;
  }

  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .dot.green {
    background: var(--success);
    box-shadow: 0 0 4px var(--success);
  }
  .dot.red {
    background: var(--error);
  }

  .badge-text {
    font-size: 12px;
  }
  .badge-text.green {
    color: var(--success);
  }
  .badge-text.red {
    color: var(--error);
  }
  .hint {
    font-style: italic;
    font-size: 11px;
    color: var(--text-muted);
    margin-left: 8px;
  }

  .sep {
    height: 1px;
    background: var(--border);
    margin: 10px 0;
  }

  .check-label {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    font-size: 12px;
    color: var(--text-secondary);
    margin-bottom: 10px;
  }
  .check-label input {
    accent-color: var(--accent);
  }

  .dir-row {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 12px;
  }

  .dir-error {
    margin-top: 4px;
    font-size: 12px;
    color: #ef4444;
  }

  .update-btn {
    margin-left: 10px;
    background: #e3a008;
    border: none;
    border-radius: 4px;
    color: #1a1a1a;
    cursor: pointer;
    font-size: 11px;
    font-weight: 700;
    padding: 3px 10px;
  }
  .update-btn:hover:not(:disabled) {
    background: #f0b429;
  }
  .update-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .dir-box {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 4px 8px;
    min-width: 0;
  }

  .dir-path {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 11px;
  }

  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 11px;
    padding: 3px 10px;
    white-space: nowrap;
    transition:
      border-color 0.15s,
      color 0.15s;
    flex-shrink: 0;
  }
  .btn:hover {
    border-color: var(--accent);
    color: var(--accent);
  }

  .log-wrap {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }

  .section-label {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-muted);
    margin-bottom: 5px;
  }

  .log {
    flex: 1;
    overflow-y: auto;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 7px 10px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    line-height: 1.55;
  }

  .log-line {
    white-space: pre-wrap;
    word-break: break-all;
  }
</style>
