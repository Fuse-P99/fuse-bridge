<script>
  import { onMount, onDestroy } from "svelte";
  import {
    GetStatus,
    GetSettings,
    GetIniSettings,
    SaveSettings,
    GetAutoStart,
    SetAutoStart,
    BrowseEQDirectory,
    GetAvailableUpdate,
    StartUpdate,
    GetAutomations,
    SetAutomations,
    GetCharNames,
    GetViewAs,
    SetViewAs,
    GetThreatDebug,
  } from "../../bindings/FuseBridge/app.js";
  import AccountLink from "../lib/AccountLink.svelte";
  import BoatRecorder from "../lib/BoatRecorder.svelte";
  import { linked } from "../lib/linkState.js";

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
  let loggingOn = false;
  let badWordFilter = false;
  let settings;

  // Forwarded-message toggles (moved here from the old Relay tab).
  const fwdOptions = [
    { key: "guild_chat", label: "Guild Chat" },
    // No "Guild MOTD" entry: ShouldForward never returns true for MOTD lines
    // (a /get by any member is indistinguishable from an officer setting it),
    // so the checkbox controlled nothing. See filter.go.
    { key: "broadcasts", label: "GM Broadcasts" },
    { key: "server_messages", label: "Server Messages" },
    { key: "quake_messages", label: "Quake Messages" },
    { key: "engage_messages", label: "Engage Messages" },
    { key: "who_output", label: "/who output" },
    { key: "character_locations", label: "Character Locs" },
    { key: "bind_location", label: "Bind location" },
    { key: "slain_messages", label: "Slain Messages" },
    { key: "resist_messages", label: "Resist Messages" },
    { key: "proc_messages", label: "Proc Messages" },
    { key: "interrupt_messages", label: "Spell Interrupts" },
    { key: "discipline_messages", label: "Disciplines" },
    // These three forward no raw lines — the client aggregates locally and
    // posts numbers — but they're the categories of play being reported, so
    // they belong where a member looks to see what their client sends.
    { key: "melee_info", label: "Melee Info" },
    { key: "spell_info", label: "Spell Info" },
    { key: "pet_info", label: "Pet Info" },
    { key: "share_map_position", label: "Share Map Position" },
    { key: "game_time", label: "Game Time (/time)" },
    { key: "world_timers", label: "Boats & Zone Events" },
  ];
  let appSettings = {};
  let settingsLoaded = false;

  async function onFwdChange(key, val) {
    // Re-fetch so we never clobber settings changed elsewhere.
    const s = await GetSettings();
    s[key] = val;
    // Proc counting needs resist lines (a proc that resists still counts), so
    // keep the two coupled: enabling Proc enables Resist; disabling Resist
    // disables Proc.
    if (key === "proc_messages" && val) s.resist_messages = true;
    if (key === "resist_messages" && !val) s.proc_messages = false;
    await SaveSettings(s);
    appSettings = s;
  }

  // ── Automations (linked members only; stored server-side per member) ────────
  let autoLoaded = false;
  let autoAdd = false;
  let autoSwap = false;
  let autoMissed = false;
  let autoMain = "";
  let mainChoices = []; // local chars (no bots/filtered) ∩ member's rostered toons
  let autoErr = "";

  // Open to any linked member (the server gates on member auth only).
  let autoInit = false;
  $: if ($linked && !autoInit) {
    autoInit = true;
    loadAutomations();
  }

  async function loadAutomations() {
    try {
      const a = (await GetAutomations()) || {};
      autoAdd = !!a.add_tracking;
      autoSwap = !!a.swap_bot;
      autoMissed = !!a.add_missed;
      autoMain = a.main_toon || "";
      const rostered = new Set(
        (a.rostered_toons || []).map((t) => t.toLowerCase()),
      );
      const chars = (await GetCharNames("", true, true)) || [];
      mainChoices = chars
        .map((c) => c.name)
        .filter((n) => rostered.has(n.toLowerCase()));
    } catch {
      /* unlinked or offline — section stays in loading state */
    } finally {
      autoLoaded = true;
    }
  }

  async function toggleAutomation(key) {
    const nextAdd = key === "add" ? !autoAdd : autoAdd;
    const nextSwap = key === "swap" ? !autoSwap : autoSwap;
    const nextMissed = key === "missed" ? !autoMissed : autoMissed;
    // The tracking/swap pair being off unsets the main character so it can be
    // re-picked next time. "Add if missed" is independent and needs no main.
    const nextMain = !nextAdd && !nextSwap ? "" : autoMain;
    autoErr = "";
    try {
      await SetAutomations(nextAdd, nextSwap, nextMissed, nextMain);
      autoAdd = nextAdd;
      autoSwap = nextSwap;
      autoMissed = nextMissed;
      autoMain = nextMain;
    } catch (e) {
      autoErr = "Couldn't save — " + e;
    }
  }

  async function pickMain(e) {
    const v = e.target.value;
    if (!v) return;
    autoErr = "";
    try {
      await SetAutomations(autoAdd, autoSwap, autoMissed, v);
      autoMain = v;
    } catch (err) {
      autoErr = "Couldn't save — " + err;
      e.target.value = "";
    }
  }

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

  // DPS & Threat calibration dump → clipboard (Admin Settings).
  let threatDebugCopied = false;
  async function copyThreatDebug() {
    try {
      const text = await GetThreatDebug();
      await navigator.clipboard.writeText(text || "");
      threatDebugCopied = true;
      setTimeout(() => (threatDebugCopied = false), 4000);
    } catch {
      /* clipboard unavailable — nothing sensible to do */
    }
  }

  // ── View as (admin preview) ────────────────────────────────────────────────
  // Gated on real_admin, NOT the (preview-suppressed) IsAdminMode — entering a
  // preview must never hide the way back out. A full reload after switching
  // re-derives every cached linked/officer/admin state from the overridden
  // bindings, so the whole app repaints as the chosen persona.
  let viewAs = { level: "", label: "", real_admin: false };
  const VIEW_AS_OPTIONS = [
    { value: "", label: "Myself (off)" },
    { value: "unlinked", label: "Unlinked User" },
    { value: "noconfig", label: "Linked User — No Config" },
    { value: "linked", label: "Linked User" },
  ];
  async function applyViewAs(level) {
    try {
      await SetViewAs(level);
      window.location.reload();
    } catch (e) {
      console.error(e);
    }
  }

  onMount(async () => {
    autoStart = await GetAutoStart();
    try {
      viewAs = await GetViewAs();
    } catch {
      /* control stays hidden */
    }
    const s = await GetSettings();

    eqDir = s.eq_directory || "";
    useMiddlemand = !!s.use_middlemand;
    appSettings = s;
    settingsLoaded = true;
    await refresh();
    interval = setInterval(refresh, 2000);
    checkUpdate();
    updateInterval = setInterval(checkUpdate, 10 * 60 * 1000);
    await processIniSettings();
  });

  onDestroy(() => {
    clearInterval(interval);
    clearInterval(updateInterval);
  });

  async function toggleAutoStart() {
    await SetAutoStart(!autoStart);
    autoStart = !autoStart;
  }

  async function processIniSettings() {
    settings = await GetIniSettings();
    settings.forEach((element) => {
      element == "Log=TRUE" || element == "Log=True" || element == "Log=true"
        ? (loggingOn = true)
        : false;
      element == "BadWord=0" ? (badWordFilter = true) : false;
    });
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
      processIniSettings();
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
    <div class="dir-row">
      <span class="label">EQ Directory</span>
      <div class="dir-box">
        <span class="mono dim dir-path" title={eqDir}>{eqDir || "Not set"}</span
        >
        <button class="btn" on:click={browseDir}>Browse…</button>
      </div>
    </div>
    {#if dirError}
      <div class="dir-error">{dirError}</div>
    {/if}

    <div class="sep" />
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
            <span class="hint">Set EverQuest directory above…</span>
          {/if}
        </div>
        <div class="status-row">
          <span class="label">EQ Logging</span>
          <span class="dot" class:green={loggingOn} class:red={!loggingOn}
          ></span>
          <span
            class="badge-text"
            class:green={loggingOn}
            class:red={!loggingOn}
          >
            {loggingOn
              ? "Enabled"
              : "Logging Disabled (Set Log=TRUE in eqclient.ini)"}
          </span>
          {#if !status.configured}
            <span class="hint">Set EverQuest directory above…</span>
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
        <div class="status-row">
          <span class="label">Discord</span>
          <AccountLink />
        </div>
      </div>
      <img class="app-icon" src="/FuseIcon2.png" alt="Fuse Bridge" />
    </div>
  </div>

  <div class="sep" />

  <!-- Basic settings -->
  <div class="section-title">Basic Settings</div>
  <div class="opt-list">
    <label class="row">
      <input type="checkbox" checked={autoStart} on:change={toggleAutoStart} />
      <span class="row-label" class:checked={autoStart}
        >Start automatically</span
      >
    </label>
    <label
      class="row"
      title="Login fix — runs the built-in P99 login proxy (p99-login-middlemand), filters the server list to P99, and points eqhost.txt at it. Fixes the 'server list fails to populate' login issue. Unchecking restores your original eqhost.txt."
    >
      <input
        type="checkbox"
        checked={useMiddlemand}
        on:change={toggleMiddlemand}
      />
      <span class="row-label" class:checked={useMiddlemand}>Use middlemand</span
      >
    </label>
  </div>

  <!-- Admin Settings: deliberately gated on real_admin (the true setting),
       not IsAdminMode — every View-as persona suppresses admin, and this box
       is the one piece of admin UI that must survive its own preview. -->
  {#if viewAs.real_admin}
    <div class="section-title">Admin Settings</div>
    <div class="opt-list">
      <div class="row va-row" class:va-on={viewAs.level}>
        <span class="row-label">View as</span>
        <select
          class="va-sel"
          value={viewAs.level}
          on:change={(e) => applyViewAs(e.target.value)}
        >
          {#each VIEW_AS_OPTIONS as o}
            <option value={o.value}>{o.label}</option>
          {/each}
        </select>
        {#if viewAs.level}
          <span class="va-note">
            Previewing as <strong>{viewAs.label}</strong> — admin and officer
            features are hidden everywhere else until you switch back.
          </span>
        {/if}
      </div>
      <!-- Boat Trip Recorder: admin calibration tool for the Server Timers
           boat loops. Hidden while a View-as preview is active — the personas
           being imitated must not see admin tooling (the View-as control
           above is the section's only preview-surviving row). -->
      {#if !viewAs.level}
        <div class="va-tool">
          <BoatRecorder />
        </div>
        <!-- DPS & Threat calibration: one click copies everything the meter
             based its numbers on (gear, haste, model, raw inference, fights)
             so an off reading can be diagnosed from a paste. -->
        <div class="row">
          <button class="btn" on:click={copyThreatDebug}>
            Copy Threat Debug
          </button>
          {#if threatDebugCopied}
            <span class="va-note">Copied — paste it wherever you're debugging.</span>
          {/if}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Forwarded messages (moved from the old Relay tab) -->
  <div class="section-title">Forwarded Messages</div>
  {#if settingsLoaded}
    <div class="opt-list">
      {#each fwdOptions as opt}
        <label class="row">
          <input
            type="checkbox"
            checked={appSettings[opt.key]}
            on:change={(e) => onFwdChange(opt.key, e.target.checked)}
          />
          <span class="row-label" class:checked={appSettings[opt.key]}
            >{opt.label}</span
          >
        </label>
      {/each}
    </div>
  {/if}

  <!-- Automations (any linked member) -->
  {#if $linked}
    <div class="section-title">Automations</div>
    <div class="auto-note">
      ⚠ These settings are experimental — please make sure you are properly
      added to all raids by checking the raid channel in the Fuse DKP Discord
      server.
    </div>
    {#if autoLoaded}
      <div class="opt-list">
        <label
          class="row"
          title="Automatically add you to non-hourly raids while you're performing tracking or any other non-porter, non-idol role."
        >
          <input
            type="checkbox"
            checked={autoAdd}
            on:change={() => toggleAutomation("add")}
          />
          <span class="row-label" class:checked={autoAdd}
            >Add to logs while tracking</span
          >
        </label>
        <label
          class="row"
          title="If you're playing on a bot when a raid ends, your main is added to that raid's log once the bot appears in the attendance post."
        >
          <input
            type="checkbox"
            checked={autoSwap}
            on:change={() => toggleAutomation("swap")}
          />
          <span class="row-label" class:checked={autoSwap}
            >Swap toons on logs when on a bot</span
          >
        </label>
        <label
          class="row"
          title="When Fuse Bridge detects you were in the zone for a non-hourly raid that closed but you got missed on the logs it will add your active toon."
        >
          <input
            type="checkbox"
            checked={autoMissed}
            on:change={() => toggleAutomation("missed")}
          />
          <span class="row-label" class:checked={autoMissed}
            >Auto add to raids if missed</span
          >
        </label>
        {#if autoAdd || autoSwap}
          <div class="auto-main">
            {#if autoMain}
              <span class="auto-main-label">Main character:</span>
              <span class="auto-main-name">{autoMain}</span>
            {:else}
              <span class="auto-main-label"
                >Select your 'main' character, we'll use this when adding you to
                any logs.</span
              >
              <select class="auto-select" on:change={pickMain}>
                <option value="">— choose —</option>
                {#each mainChoices as n (n)}
                  <option value={n}>{n}</option>
                {/each}
              </select>
            {/if}
          </div>
        {/if}
        {#if autoErr}
          <div class="auto-err">{autoErr}</div>
        {/if}
      </div>
    {/if}
  {/if}

  <!-- Activity log -->
  <div class="log-wrap">
    <div class="section-title">Activity</div>
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
    overflow-y: auto;
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
    font-weight: 600;
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

  /* Section headers + option rows (shared styling, ported from the Relay tab). */
  .section-title {
    color: var(--accent);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    margin-bottom: 6px;
  }

  .opt-list {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 16px;
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 2px 24px;
    margin-bottom: 12px;
  }
  @media (max-width: 480px) {
    .opt-list {
      grid-template-columns: 1fr;
    }
  }

  /* View-as control (Admin Settings) */
  .va-row {
    grid-column: 1 / -1;
    cursor: default;
    flex-wrap: wrap;
  }
  .va-row.va-on {
    background: rgba(227, 160, 8, 0.08);
  }
  .va-sel {
    background: var(--bg-input, rgba(0, 0, 0, 0.25));
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 4px 8px;
  }
  .va-note {
    color: #e3a008;
    font-size: 11px;
    line-height: 1.4;
  }
  .va-tool {
    grid-column: 1 / -1;
    padding: 4px;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 4px;
    border-radius: 4px;
    cursor: pointer;
    transition: background 0.1s;
  }
  .row:hover {
    background: rgba(255, 255, 255, 0.03);
  }
  .row input[type="checkbox"] {
    accent-color: var(--accent);
    width: 14px;
    height: 14px;
    flex-shrink: 0;
  }
  .row-label {
    font-size: 11px;
    color: var(--text-secondary);
    transition: color 0.15s;
  }
  .row-label.checked {
    color: var(--text-primary);
  }

  /* Automations */
  .auto-note {
    background: rgba(227, 160, 8, 0.1);
    border: 1px solid rgba(227, 160, 8, 0.55);
    border-radius: 6px;
    color: var(--text-secondary);
    font-size: 11px;
    line-height: 1.5;
    padding: 7px 10px;
    margin-bottom: 8px;
  }
  .auto-main {
    grid-column: 1 / -1;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 4px 2px;
    border-top: 1px solid var(--border);
    margin-top: 4px;
    flex-wrap: wrap;
  }
  .auto-main-label {
    font-size: 12px;
    color: var(--text-secondary);
  }
  .auto-main-name {
    font-size: 12px;
    font-weight: 600;
    color: var(--accent);
  }
  .auto-select {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 3px 8px;
    outline: none;
  }
  .auto-select:focus {
    border-color: var(--accent-dim);
  }
  .auto-err {
    grid-column: 1 / -1;
    font-size: 11px;
    color: #ef4444;
    padding: 2px 4px;
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

  /* Pinned to the bottom of the scrolling tab. The settings above scroll
     UNDER it, which is why the background is opaque — sticky leaves the
     content behind it visible otherwise. Fixed height rather than flex:1 so
     it's the same size wherever the tab is scrolled to. */
  .log-wrap {
    display: flex;
    flex-direction: column;
    flex: 0 0 400px;
    height: 400px;
    min-height: 0;
    position: sticky;
    bottom: 0;
    z-index: 1;
    background: var(--bg-primary);
    padding-top: 8px;
    margin-top: 6px;
  }

  .log {
    flex: 1;
    min-height: 0;
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
