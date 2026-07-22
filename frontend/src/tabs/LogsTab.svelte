<script>
  import { onMount, onDestroy, tick } from "svelte";
  import { fade } from "svelte/transition";
  import {
    GetLogSearchCharacters,
    SearchLogs,
    GetLogContext,
    GetLogArchiveSettings,
    SaveLogArchiveSettings,
    BrowseArchiveDir,
  } from "../../bindings/FuseBridge/app.js";

  let chars = [];
  let character = "";
  let query = "";
  let includeArchived = false;

  // Default range: the last hour up to now.
  const HOUR = 3600 * 1000;
  let startMs = Date.now() - HOUR;
  let endMs = Date.now();

  let results = null; // { hits, total, truncated, files }
  let searching = false;
  let context = null; // expanded log view: { file, header, lines, center }
  let showRange = false; // Configure Time Range popover
  let mounted = false;

  // Manage Logs (archival) modal.
  let manageOpen = false;
  let arch = { enabled: false, dir: "", size_mb: 50, delete_days: 0 };
  let delEnabled = false;
  let delDays = 30;
  const SIZE_OPTIONS = [20, 50, 100];

  async function openManage() {
    try {
      arch = await GetLogArchiveSettings();
    } catch {
      arch = { enabled: false, dir: "", size_mb: 50, delete_days: 0 };
    }
    delEnabled = (arch.delete_days || 0) > 0;
    delDays = arch.delete_days > 0 ? arch.delete_days : 30;
    manageOpen = true;
  }
  async function browseArchive() {
    try {
      const d = await BrowseArchiveDir();
      if (d) arch.dir = d;
    } catch {
      /* dialog cancelled */
    }
  }
  async function saveManage() {
    try {
      await SaveLogArchiveSettings({
        enabled: arch.enabled,
        dir: arch.dir,
        size_mb: Number(arch.size_mb) || 50,
        delete_days: delEnabled
          ? Math.max(1, Math.round(Number(delDays) || 0))
          : 0,
      });
    } catch {
      /* keep the modal open on failure */
      return;
    }
    manageOpen = false;
  }

  const QUICK = [
    { label: "1 hour", ms: HOUR },
    { label: "1 day", ms: 24 * HOUR },
    { label: "1 week", ms: 7 * 24 * HOUR },
    { label: "1 month", ms: 30 * 24 * HOUR },
    { label: "1 year", ms: 365 * 24 * HOUR },
  ];

  function pad(n) {
    return String(n).padStart(2, "0");
  }
  // epoch ms → value for <input type="datetime-local"> (local time, minute res).
  function toLocalInput(ms) {
    const d = new Date(ms);
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }
  function fromLocalInput(s) {
    const t = new Date(s).getTime();
    return Number.isNaN(t) ? Date.now() : t;
  }
  function fmtStamp(ms) {
    return new Date(ms).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
    });
  }
  function fmtTime(ms) {
    if (!ms) return "";
    return new Date(ms).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    });
  }
  function baseName(p) {
    return (p || "").split(/[\\/]/).pop();
  }

  $: rangeLabel = `${fmtStamp(startMs)} → ${fmtStamp(endMs)}`;
  $: placeholder = character ? `Search ${character}'s logs…` : "Search logs…";

  function pickQuick(ms) {
    const now = Date.now();
    endMs = now;
    startMs = now - ms;
  }
  function onStartInput(e) {
    startMs = fromLocalInput(e.target.value);
  }
  function onEndInput(e) {
    endMs = fromLocalInput(e.target.value);
  }

  async function loadChars() {
    try {
      chars = (await GetLogSearchCharacters()) || [];
    } catch {
      chars = [];
    }
    if (!character) {
      const cur = chars.find((c) => c.current);
      character = cur ? cur.name : chars[0] ? chars[0].name : "";
    }
  }

  // Debounced search whenever a search input changes.
  let searchTimer;
  function scheduleSearch() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(runSearch, 300);
  }
  async function runSearch() {
    const q = query.trim();
    if (!q || !character) {
      results = null;
      return;
    }
    searching = true;
    try {
      results = await SearchLogs(
        character,
        q,
        Math.round(startMs),
        Math.round(endMs),
        includeArchived,
      );
    } catch {
      results = null;
    }
    searching = false;
  }

  // Re-run when any parameter changes (mounted guard avoids an initial fire).
  $: searchKey = JSON.stringify([
    character,
    query,
    startMs,
    endMs,
    includeArchived,
  ]);
  $: if (mounted && searchKey) scheduleSearch();

  async function openContext(hit) {
    try {
      context = await GetLogContext(hit.file, hit.line_no);
    } catch {
      context = null;
      return;
    }
    await tick();
    document.getElementById("ctx-center")?.scrollIntoView({ block: "center" });
  }
  function backToResults() {
    context = null;
  }

  // Split a line into plain/highlighted segments around the query (safe: text
  // nodes only, no HTML injection from log content).
  function segs(line, q) {
    const needle = (q || "").trim();
    if (!needle) return [{ t: line, hit: false }];
    const out = [];
    const lower = line.toLowerCase();
    const nl = needle.toLowerCase();
    let i = 0;
    while (i < line.length) {
      const idx = lower.indexOf(nl, i);
      if (idx < 0) {
        out.push({ t: line.slice(i), hit: false });
        break;
      }
      if (idx > i) out.push({ t: line.slice(i, idx), hit: false });
      out.push({ t: line.slice(idx, idx + needle.length), hit: true });
      i = idx + needle.length;
    }
    return out;
  }

  function closeRange(e) {
    // Close the popover on an outside click.
    if (showRange && !e.target.closest(".range-wrap")) showRange = false;
  }

  onMount(async () => {
    await loadChars();
    mounted = true;
  });
  onDestroy(() => clearTimeout(searchTimer));
</script>

<svelte:window on:click={closeRange} />

<div class="logs-tab">
  {#if context}
    <!-- expanded context view -->
    <div class="ctx-bar">
      <button class="btn back" on:click={backToResults}>← Back</button>
      <span class="ctx-header" title={context.file}>{context.header || baseName(context.file)}</span>
    </div>
    <div class="ctx-body">
      {#each context.lines as ln, i}
        <div
          class="ctx-line"
          class:center={i === context.center}
          id={i === context.center ? "ctx-center" : undefined}
        >
          {#if i === context.center}
            {#each segs(ln, query) as s}{#if s.hit}<mark>{s.t}</mark>{:else}{s.t}{/if}{/each}
          {:else}
            {ln}
          {/if}
        </div>
      {/each}
      {#if !context.lines.length}
        <div class="empty">Could not load this log section.</div>
      {/if}
    </div>
  {:else}
    <div class="bar">
      <span class="title">Logs</span>
      <div class="char-row">
        <span class="char-label">Search logs for</span>
        <select class="in char-sel" bind:value={character}>
          {#each chars as c (c.name)}
            <option value={c.name}>
              {c.name}{c.class ? ` — ${c.class}` : ""}{c.current
                ? " (playing)"
                : ""}
            </option>
          {:else}
            <option value="">No character logs found</option>
          {/each}
        </select>
      </div>
      <button class="btn manage-btn" on:click={openManage}>Manage Logs</button>
    </div>

    <div class="controls">
      <div class="search-cell">
        <input
          class="in search-in"
          placeholder={placeholder}
          title={placeholder}
          bind:value={query}
        />
        {#if query.trim()}
          <span class="count" class:searching>
            {searching
              ? "…"
              : results
                ? `${results.truncated ? `${results.hits.length} of ` : ""}${results.total} result${results.total === 1 ? "" : "s"}`
                : "0 results"}
          </span>
        {/if}
      </div>

      <div class="range-wrap">
        <button
          class="btn range-btn"
          class:on={showRange}
          title="Configure the time range to search"
          on:click|stopPropagation={() => (showRange = !showRange)}
        >
          <span class="range-disp">{rangeLabel}</span>
          <span class="range-cfg">Configure Time Range</span>
        </button>
        {#if showRange}
          <div class="range-pop" transition:fade={{ duration: 100 }}>
            <div class="rp-quick">
              {#each QUICK as q}
                <button class="rp-q" on:click={() => pickQuick(q.ms)}
                  >{q.label}</button
                >
              {/each}
            </div>
            <div class="rp-grid">
              <label class="rp-label" for="rp-start">Start</label>
              <input
                id="rp-start"
                class="in"
                type="datetime-local"
                value={toLocalInput(startMs)}
                on:input={onStartInput}
              />
              <label class="rp-label" for="rp-end">End</label>
              <input
                id="rp-end"
                class="in"
                type="datetime-local"
                value={toLocalInput(endMs)}
                on:input={onEndInput}
              />
            </div>
          </div>
        {/if}
      </div>

      <label class="arch" title="Also search archived logs in subfolders">
        <input type="checkbox" bind:checked={includeArchived} />
        <span class="arch-track"><span class="arch-knob"></span></span>
        <span class="arch-label">Include archived logs</span>
      </label>
    </div>

    <div class="results">
      {#if !query.trim()}
        <div class="empty">
          Enter a search term to scan {character || "your character"}'s logs.
        </div>
      {:else if searching && !results}
        <div class="empty">Searching…</div>
      {:else if results && results.hits.length}
        {#if results.truncated}
          <div class="trunc">
            Showing the first {results.hits.length} of {results.total} matches —
            narrow the time range or search to see the rest.
          </div>
        {/if}
        {#each results.hits as h (h.file + ":" + h.line_no)}
          <button class="hit" on:click={() => openContext(h)}>
            <span class="hit-time">{fmtTime(h.at_ms)}</span>
            <span class="hit-line">
              {#each segs(h.line, query) as s}{#if s.hit}<mark>{s.t}</mark
                  >{:else}{s.t}{/if}{/each}
            </span>
            {#if includeArchived && results.files > 1}
              <span class="hit-file" title={h.file}>{baseName(h.file)}</span>
            {/if}
          </button>
        {/each}
      {:else}
        <div class="empty">No matching log lines in this time range.</div>
      {/if}
    </div>
  {/if}

  {#if manageOpen}
    <div class="overlay" on:click|self={() => (manageOpen = false)}>
      <div class="modal">
        <div class="modal-title">Manage Logs</div>
        <div class="m-note">
          Archiving moves your older, oversized log files out of the EQ Logs
          folder during a quiet period (after the game has been idle a while, so
          it never interferes with live logging). The character currently being
          played is never touched.
        </div>

        <label class="tgl">
          <input type="checkbox" bind:checked={arch.enabled} />
          <span class="tgl-track"><span class="tgl-knob"></span></span>
          <span class="tgl-label">Archive my logs</span>
        </label>

        {#if arch.enabled}
          <div class="m-sep"></div>

          <label class="m-label" for="ml-dir">Archive location</label>
          <div class="m-inline">
            <input id="ml-dir" class="in" bind:value={arch.dir} />
            <button class="btn" on:click={browseArchive}>Browse…</button>
          </div>

          <label class="m-label" for="ml-size">Archive logs larger than</label>
          <select id="ml-size" class="in sel" bind:value={arch.size_mb}>
            {#each SIZE_OPTIONS as mb}
              <option value={mb}>{mb} MB</option>
            {/each}
          </select>

          <div class="m-sep"></div>
          <label class="tgl">
            <input type="checkbox" bind:checked={delEnabled} />
            <span class="tgl-track"><span class="tgl-knob"></span></span>
            <span class="tgl-label">Auto-delete old archived logs</span>
          </label>
          {#if delEnabled}
            <div class="m-inline">
              <span class="m-label">Delete after</span>
              <input
                class="in num"
                type="number"
                min="1"
                bind:value={delDays}
              />
              <span class="m-label">days</span>
            </div>
          {/if}
        {/if}

        <div class="modal-actions">
          <button class="btn save" on:click={saveManage}>Save</button>
          <button class="btn" on:click={() => (manageOpen = false)}>Cancel</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .logs-tab {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }

  .bar {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
    flex-shrink: 0;
  }
  .title {
    color: var(--accent);
    font-weight: 600;
    font-size: 13px;
  }
  .char-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .char-label {
    color: var(--text-secondary);
    font-size: 11px;
    white-space: nowrap;
  }
  .char-sel {
    width: auto;
    min-width: 180px;
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
    flex-wrap: wrap;
  }
  .search-cell {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 220px;
  }
  .search-in {
    flex: 1;
    min-width: 0;
  }
  .count {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
    font-family: var(--font-mono);
  }
  .count.searching {
    opacity: 0.7;
  }

  /* time range control + popover */
  .range-wrap {
    position: relative;
  }
  .range-btn {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .range-disp {
    color: var(--text-primary);
    font-size: 11px;
    white-space: nowrap;
  }
  .range-cfg {
    color: var(--accent);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    border-left: 1px solid var(--border);
    padding-left: 8px;
  }
  .range-pop {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    z-index: 40;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 260px;
  }
  .rp-quick {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }
  .rp-q {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 3px 9px;
  }
  .rp-q:hover {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .rp-grid {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 6px 8px;
    align-items: center;
  }
  .rp-label {
    font-size: 11px;
    color: var(--text-secondary);
  }

  /* archived toggle */
  .arch {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    cursor: pointer;
    white-space: nowrap;
  }
  .arch input {
    display: none;
  }
  .arch-track {
    position: relative;
    width: 30px;
    height: 16px;
    border-radius: 8px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    transition: background 0.15s;
    flex-shrink: 0;
  }
  .arch-knob {
    position: absolute;
    top: 1px;
    left: 1px;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--text-muted);
    transition:
      transform 0.15s,
      background 0.15s;
  }
  .arch input:checked + .arch-track {
    background: var(--accent-dim);
    border-color: var(--accent-dim);
  }
  .arch input:checked + .arch-track .arch-knob {
    transform: translateX(14px);
    background: var(--accent);
  }
  .arch-label {
    color: var(--text-secondary);
    font-size: 11px;
  }

  /* results */
  .results {
    flex: 1;
    overflow-y: auto;
    padding: 4px 0;
  }
  .empty {
    color: var(--text-muted);
    font-size: 12px;
    text-align: center;
    padding: 28px 14px;
  }
  .trunc {
    color: var(--text-muted);
    font-size: 11px;
    padding: 6px 14px;
    font-style: italic;
  }
  .hit {
    display: flex;
    align-items: baseline;
    gap: 10px;
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    border-bottom: 1px solid var(--border);
    color: var(--text-primary);
    cursor: pointer;
    font-size: 11.5px;
    padding: 5px 14px;
    font-family: var(--font-mono);
  }
  .hit:hover {
    background: rgba(255, 255, 255, 0.04);
  }
  .hit-time {
    color: var(--text-muted);
    font-size: 10.5px;
    white-space: nowrap;
    flex-shrink: 0;
    min-width: 118px;
  }
  .hit-line {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .hit-file {
    color: var(--text-muted);
    font-size: 10px;
    white-space: nowrap;
    flex-shrink: 0;
  }
  mark {
    background: var(--accent);
    color: #1a1400;
    border-radius: 2px;
    padding: 0 1px;
  }

  /* context view */
  .ctx-bar {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
    flex-shrink: 0;
  }
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 3px 10px;
  }
  .btn:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  .btn.on {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .btn.back {
    color: var(--accent);
  }
  .ctx-header {
    color: var(--text-muted);
    font-size: 11px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ctx-body {
    flex: 1;
    overflow: auto;
    padding: 6px 0;
    font-family: var(--font-mono);
    font-size: 11.5px;
    line-height: 1.5;
  }
  .ctx-line {
    padding: 0 14px;
    white-space: pre-wrap;
    word-break: break-word;
    color: var(--text-secondary);
  }
  .ctx-line.center {
    background: rgba(200, 169, 81, 0.14);
    color: var(--text-primary);
    border-left: 2px solid var(--accent);
    padding-left: 12px;
  }

  /* base inputs (scoped per component) */
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
  .in.num {
    width: 80px;
  }
  .in.sel {
    width: auto;
    min-width: 120px;
  }

  .manage-btn {
    margin-left: auto;
  }

  /* Manage Logs modal */
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 50;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .modal {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    width: 400px;
    max-width: 90%;
    max-height: 85%;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 9px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 6px;
  }
  .btn.save {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .m-note {
    color: var(--text-muted);
    font-size: 11.5px;
    line-height: 1.5;
  }
  .m-label {
    font-size: 11px;
    color: var(--text-secondary);
    white-space: nowrap;
  }
  .m-inline {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .m-inline .in {
    flex: 1;
    min-width: 0;
  }
  .m-inline .in.num {
    flex: 0 0 auto;
  }
  .m-sep {
    height: 1px;
    background: var(--border);
    margin: 4px 0;
  }

  /* toggle switch (reused for enable + auto-delete) */
  .tgl {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }
  .tgl input {
    display: none;
  }
  .tgl-track {
    position: relative;
    width: 30px;
    height: 16px;
    border-radius: 8px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    transition: background 0.15s;
    flex-shrink: 0;
  }
  .tgl-knob {
    position: absolute;
    top: 1px;
    left: 1px;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--text-muted);
    transition:
      transform 0.15s,
      background 0.15s;
  }
  .tgl input:checked + .tgl-track {
    background: var(--accent-dim);
    border-color: var(--accent-dim);
  }
  .tgl input:checked + .tgl-track .tgl-knob {
    transform: translateX(14px);
    background: var(--accent);
  }
  .tgl-label {
    color: var(--text-secondary);
    font-size: 12px;
  }
</style>
