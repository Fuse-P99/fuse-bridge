<script>
  import { onMount, onDestroy, tick } from "svelte";
  import { fade } from "svelte/transition";
  import { Clipboard } from "@wailsio/runtime";
  import { scale } from "../lib/scale.js";
  import {
    GetLogSearchCharacters,
    SearchLogs,
    GetLogContext,
    GetLogFile,
    GetLogRange,
    OpenLogFileWith,
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
  let showRange = false; // Time Range popover
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

  // ms: 0 is the "All Time" sentinel — the start becomes the epoch, so every
  // log line qualifies.
  const QUICK = [
    { label: "1 hour", ms: HOUR },
    { label: "1 day", ms: 24 * HOUR },
    { label: "1 week", ms: 7 * 24 * HOUR },
    { label: "1 month", ms: 30 * 24 * HOUR },
    { label: "1 year", ms: 365 * 24 * HOUR },
    { label: "All Time", ms: 0 },
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

  $: rangeLabel =
    startMs === 0 ? `All Time → ${fmtStamp(endMs)}` : `${fmtStamp(startMs)} → ${fmtStamp(endMs)}`;
  $: placeholder = context
    ? `Search within ${baseName(context.file)}…`
    : character
      ? `Search ${character}'s logs…`
      : "Search logs…";

  function pickQuick(ms) {
    const now = Date.now();
    endMs = now;
    startMs = ms ? now - ms : 0; // 0 = All Time (search from the epoch)
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
  // Not while a file is open: the box is searching that file, and re-running
  // the log-set search would churn and then hand back stale results on Back.
  $: if (mounted && searchKey && !context) scheduleSearch();

  async function openContext(hit) {
    try {
      context = await GetLogContext(hit.file, hit.line_no);
    } catch {
      context = null;
      return;
    }
    // The search that found this line carries into the file view, so the hit
    // is already highlighted and n/N walks its neighbours immediately.
    ctxQuery = query;
    ctxMatchIdx = 0;
    await tick();
    document.getElementById("ctx-center")?.scrollIntoView({ block: "center" });
  }
  function backToResults() {
    context = null;
    ctxQuery = "";
  }

  // ── expanded file view ──────────────────────────────────────────────────────
  // Inside a context the search box changes meaning: it searches THIS file,
  // not the log set. The controls that pick which logs to search are disabled
  // rather than hidden, so it's visible that they still hold their settings
  // and that Back returns to a result list built from them.
  let ctxQuery = "";
  let ctxMatchIdx = 0;
  let ctxLoading = false;
  let ctxBodyEl;

  $: ctxNeedle = ctxQuery.trim().toLowerCase();
  $: ctxMatches = (() => {
    if (!context || !ctxNeedle) return [];
    const out = [];
    const lines = context.lines || [];
    for (let i = 0; i < lines.length; i++) {
      if (lines[i].toLowerCase().includes(ctxNeedle)) out.push(i);
    }
    return out;
  })();
  $: if (ctxMatchIdx >= ctxMatches.length) ctxMatchIdx = 0;
  // The row the match navigation is currently parked on, so it can be marked
  // distinctly from the other matches.
  $: ctxCurrent = ctxMatches.length ? ctxMatches[ctxMatchIdx] : -1;

  function onSearchInput(e) {
    const v = e.target.value;
    if (context) {
      ctxQuery = v;
      ctxMatchIdx = 0;
      scrollToCtxMatch();
    } else {
      query = v;
    }
  }
  function stepMatch(d) {
    if (!ctxMatches.length) return;
    ctxMatchIdx = (ctxMatchIdx + d + ctxMatches.length) % ctxMatches.length;
    scrollToCtxMatch();
  }
  async function scrollToCtxMatch() {
    await tick();
    if (ctxCurrent < 0) return;
    ctxBodyEl
      ?.querySelector(".ctx-line.match-current")
      ?.scrollIntoView({ block: "center" });
  }

  // Scroll anchoring. The reader's position is captured as an ABSOLUTE file
  // line number plus its pixel offset from the top of the viewport — an index
  // into context.lines would be meaningless once a wider window replaces it,
  // which is exactly what makes the view jump.
  function captureAnchor() {
    if (!ctxBodyEl || !context) return null;
    const top = ctxBodyEl.getBoundingClientRect().top;
    for (const el of ctxBodyEl.querySelectorAll(".ctx-line")) {
      const r = el.getBoundingClientRect();
      if (r.bottom > top) {
        return { line: Number(el.dataset.ln), offset: r.top - top };
      }
    }
    return null;
  }
  function restoreAnchor(a) {
    if (!a || !a.line || !ctxBodyEl) return;
    const el = ctxBodyEl.querySelector(`.ctx-line[data-ln="${a.line}"]`);
    if (!el) return;
    const top = ctxBodyEl.getBoundingClientRect().top;
    ctxBodyEl.scrollTop += el.getBoundingClientRect().top - top - a.offset;
  }

  // Mirrors logWindowChunk / logWindowMax in logsearch_ui.go. A file at or
  // under MAX can be opened whole; past that the window walks in CHUNKs and
  // the far end is trimmed to hold the ceiling.
  const WIN_CHUNK = 30000;
  const WIN_MAX = 90000;

  $: ctxLast = context ? context.first + context.lines.length - 1 : 0;
  $: ctxCanLoadAll = !!context && !context.full && context.total <= WIN_MAX;
  $: ctxMoreAbove = context && !context.full ? context.first - 1 : 0;
  $: ctxMoreBelow = context && !context.full ? context.total - ctxLast : 0;

  // Every load routes through here so anchoring is impossible to forget: the
  // point of all of it is that the line you were reading does not move.
  async function reload(fn) {
    if (!context || ctxLoading) return;
    const anchor = captureAnchor();
    const around =
      anchor?.line ||
      (context.center >= 0 ? context.first + context.center : context.first);
    ctxLoading = true;
    try {
      const next = await fn(around);
      if (next && (next.lines || []).length) {
        context = next;
        clearSel(); // indices moved; a stale selection would point elsewhere
        await tick();
        restoreAnchor(anchor);
      }
    } catch {
      /* leave the existing window in place */
    } finally {
      ctxLoading = false;
    }
  }

  function loadEntireFile() {
    return reload((around) => GetLogFile(context.file, around));
  }

  // dir < 0 grows upward, > 0 downward. The opposite end is trimmed when the
  // span would pass the ceiling, so the window slides once it stops growing.
  function loadMore(dir) {
    const from0 = context.first;
    const to0 = ctxLast;
    let from = from0;
    let to = to0;
    if (dir < 0) {
      from = Math.max(1, from0 - WIN_CHUNK);
      if (to - from + 1 > WIN_MAX) to = from + WIN_MAX - 1;
    } else {
      to = Math.min(context.total, to0 + WIN_CHUNK);
      if (to - from + 1 > WIN_MAX) from = to - WIN_MAX + 1;
    }
    return reload((around) => GetLogRange(context.file, from, to, around));
  }

  // Windows' own "How do you want to open this file?" chooser — the honest
  // answer for a file this app will never render in full.
  async function openWithApp() {
    if (!context) return;
    let err = "";
    try {
      err = await OpenLogFileWith(context.file);
    } catch (e) {
      err = String(e);
    }
    if (err) {
      copyMsg = err; // same transient toast the copy path uses
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => (copyMsg = ""), 3500);
    }
  }

  function fmtCount(n) {
    return (n || 0).toLocaleString();
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

  // ── line selection + copy ───────────────────────────────────────────────────
  // One selection model serves both views — the results list and the expanded
  // context never render at the same time — keyed by row index into rowTexts.
  // Selection is per LINE, not per character: dragging across rows picks whole
  // log lines rather than a text range, which is why the rows opt out of native
  // text selection.
  let selLines = new Set(); // selected row indices
  let selAnchor = -1; // range anchor for drag and shift+click
  let dragSel = false; // a drag-select is in progress
  let dragMoved = false; // ...and the gesture was more than a plain click
  let cmenu = { visible: false, x: 0, y: 0 };
  let copyMsg = "";
  let copyTimer;

  // The raw text behind every row of the visible view, in display order.
  $: rowTexts = context
    ? context.lines || []
    : results && results.hits
      ? results.hits.map((h) => h.line)
      : [];
  // Switching views or re-running the search invalidates index-based selection.
  $: results, context, clearSel();

  function clearSel() {
    selLines = new Set();
    selAnchor = -1;
    cmenu = { visible: false, x: 0, y: 0 };
  }
  function rangeSet(a, b) {
    const s = new Set();
    for (let i = Math.min(a, b); i <= Math.max(a, b); i++) s.add(i);
    return s;
  }
  function selectAllLines() {
    selLines = new Set(rowTexts.map((_, i) => i));
    selAnchor = rowTexts.length ? 0 : -1;
  }

  function onRowDown(e, i) {
    if (e.button !== 0) return; // right-click is the contextmenu handler's job
    if (e.shiftKey && selAnchor >= 0) {
      selLines = rangeSet(selAnchor, i);
      dragMoved = true; // a selection gesture, so don't also open this hit
      e.preventDefault();
      return;
    }
    if (e.ctrlKey || e.metaKey) {
      const s = new Set(selLines);
      s.has(i) ? s.delete(i) : s.add(i);
      selLines = s;
      selAnchor = i;
      dragMoved = true;
      e.preventDefault();
      return;
    }
    // Plain press: arm a drag. A press that never leaves its own row stays an
    // ordinary click, so opening a hit still works.
    selAnchor = i;
    selLines = new Set([i]);
    dragSel = true;
    dragMoved = false;
  }
  function onRowEnter(i) {
    if (!dragSel) return;
    if (i !== selAnchor) dragMoved = true;
    selLines = rangeSet(selAnchor, i);
  }
  function endDrag() {
    dragSel = false;
  }
  // Only a press-and-release on one row opens it; a drag was a selection.
  function onRowClick(h) {
    if (dragMoved) {
      dragMoved = false;
      return;
    }
    openContext(h);
  }
  function onRowContext(e, i) {
    e.preventDefault();
    // Right-clicking outside the selection retargets it to just this row.
    if (!selLines.has(i)) {
      selLines = new Set([i]);
      selAnchor = i;
    }
    // The menu sits inside .shell (CSS zoom: $scale), which scales its
    // coordinate space — divide the viewport coords so it lands at the cursor.
    cmenu = { visible: true, x: e.clientX / $scale, y: e.clientY / $scale };
  }

  async function copySelection() {
    const idx = [...selLines].sort((a, b) => a - b);
    const text = idx
      .map((i) => rowTexts[i])
      .filter((t) => t != null)
      .join("\r\n"); // CRLF: these get pasted into Windows editors
    cmenu = { visible: false, x: 0, y: 0 };
    if (!text) return;
    let ok = true;
    try {
      await Clipboard.SetText(text);
    } catch {
      try {
        await navigator.clipboard.writeText(text);
      } catch {
        ok = false;
      }
    }
    copyMsg = ok
      ? `Copied ${idx.length} line${idx.length === 1 ? "" : "s"}`
      : "Could not reach the clipboard";
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (copyMsg = ""), 2500);
  }

  function onWindowClick(e) {
    closeRange(e);
    if (cmenu.visible) cmenu = { visible: false, x: 0, y: 0 };
  }
  function onKeydown(e) {
    if (e.key === "Escape") {
      cmenu = { visible: false, x: 0, y: 0 };
      return;
    }
    if (!(e.ctrlKey || e.metaKey) || manageOpen) return;
    // Never hijack these while the user is typing in a field.
    if (e.target?.closest?.("input, select, textarea")) return;
    if (e.key === "a" || e.key === "A") {
      e.preventDefault();
      selectAllLines();
    } else if ((e.key === "c" || e.key === "C") && selLines.size) {
      e.preventDefault();
      copySelection();
    }
  }

  onMount(async () => {
    await loadChars();
    mounted = true;
  });
  onDestroy(() => {
    clearTimeout(searchTimer);
    clearTimeout(copyTimer);
  });
</script>

<svelte:window
  on:click={onWindowClick}
  on:mouseup={endDrag}
  on:keydown={onKeydown}
/>

<div class="logs-tab">
  <div class="bar">
    {#if context}
      <button class="btn back" on:click={backToResults}>← Back</button>
    {/if}
    <span class="title">Logs</span>
    <div class="char-row">
      <span class="char-label">Search logs for</span>
      <!-- Disabled, not hidden, while a file is open: the setting still holds,
           and Back returns to a result list built from it. -->
      <select class="in char-sel" bind:value={character} disabled={!!context}>
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
    <button class="btn manage-btn" on:click={openManage} disabled={!!context}
      >Manage Logs</button
    >
  </div>

  <div class="controls" class:scoped={!!context}>
    <div class="search-cell">
      <input
        class="in search-in"
        placeholder={placeholder}
        title={placeholder}
        value={context ? ctxQuery : query}
        on:input={onSearchInput}
      />
      {#if context}
        {#if ctxNeedle}
          <span class="count"
            >{ctxMatches.length
              ? `${ctxMatchIdx + 1} of ${fmtCount(ctxMatches.length)}`
              : "0 matches"}</span
          >
          <button
            class="btn nav"
            on:click={() => stepMatch(-1)}
            disabled={!ctxMatches.length}
            title="Previous match">↑</button
          >
          <button
            class="btn nav"
            on:click={() => stepMatch(1)}
            disabled={!ctxMatches.length}
            title="Next match">↓</button
          >
        {/if}
      {:else if query.trim()}
        <span class="count" class:searching>
          {searching
            ? "…"
            : results
              ? `${results.truncated ? `${results.hits.length} of ` : ""}${results.total} result${results.total === 1 ? "" : "s"}`
              : "0 results"}
        </span>
      {/if}
      {#if selLines.size}
        <span class="count sel-count" title="Right-click a selected line to copy"
          >{selLines.size} selected</span
        >
      {/if}
    </div>

    <div class="range-wrap">
      <button
        class="btn range-btn"
        class:on={showRange}
        disabled={!!context}
        title={context
          ? "Time range applies to the log search — go Back to change it"
          : "Configure the time range to search"}
        on:click|stopPropagation={() => (showRange = !showRange)}
      >
        <span class="range-disp">{rangeLabel}</span>
        <span class="range-cfg">Time Range</span>
      </button>
      {#if showRange && !context}
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

    <label
      class="arch"
      class:off={!!context}
      title={context
        ? "Applies to the log search — go Back to change it"
        : "Also search archived logs in subfolders"}
    >
      <input
        type="checkbox"
        bind:checked={includeArchived}
        disabled={!!context}
      />
      <span class="arch-track"><span class="arch-knob"></span></span>
      <span class="arch-label">Include archived logs</span>
    </label>
  </div>

  {#if context}
    <!-- expanded file view -->
    <div class="ctx-bar">
      <span class="ctx-header" title={context.file}
        >{context.header || baseName(context.file)}</span
      >
      <span class="ctx-range">
        {#if context.full}
          all {fmtCount(context.total)} lines
        {:else}
          lines {fmtCount(context.first)}–{fmtCount(
            context.first + context.lines.length - 1,
          )} of {fmtCount(context.total)}
        {/if}
      </span>
      <span class="sel-hint">
        {#if selLines.size}
          {selLines.size} line{selLines.size === 1 ? "" : "s"} selected — right-click
          to copy
        {:else}
          Drag or Ctrl+A to select lines, right-click to copy
        {/if}
      </span>
    </div>
    <div class="ctx-body" bind:this={ctxBodyEl}>
      <!-- Top and bottom both, so the offer is wherever the reader hits the
           edge of the window and wants more. -->
      {#if context.lines.length && ctxMoreAbove > 0}
        <div class="load-row">
          {#if ctxCanLoadAll}
            <button class="btn load-btn" disabled={ctxLoading} on:click={loadEntireFile}
              >{ctxLoading
                ? "Loading…"
                : `↑ Load Entire File (${fmtCount(context.total)} lines)`}</button
            >
          {:else}
            <button class="btn load-btn" disabled={ctxLoading} on:click={() => loadMore(-1)}
              >{ctxLoading
                ? "Loading…"
                : `↑ Load ${fmtCount(Math.min(WIN_CHUNK, ctxMoreAbove))} more above`}</button
            >
            <span class="load-note"
              >{fmtCount(ctxMoreAbove)} earlier lines in this file</span
            >
          {/if}
        </div>
      {/if}
      {#each context.lines as ln, i}
        <!-- svelte-ignore a11y-no-static-element-interactions -->
        <div
          class="ctx-line"
          class:center={i === context.center}
          class:sel={selLines.has(i)}
          class:match-current={i === ctxCurrent}
          data-ln={context.first + i}
          id={i === context.center ? "ctx-center" : undefined}
          on:mousedown={(e) => onRowDown(e, i)}
          on:mouseenter={() => onRowEnter(i)}
          on:contextmenu={(e) => onRowContext(e, i)}
        >
          {#if ctxNeedle && ln.toLowerCase().includes(ctxNeedle)}
            {#each segs(ln, ctxQuery) as s}{#if s.hit}<mark>{s.t}</mark
                >{:else}{s.t}{/if}{/each}
          {:else}
            {ln}
          {/if}
        </div>
      {/each}
      {#if context.lines.length && ctxMoreBelow > 0}
        <div class="load-row">
          {#if ctxCanLoadAll}
            <button class="btn load-btn" disabled={ctxLoading} on:click={loadEntireFile}
              >{ctxLoading
                ? "Loading…"
                : `↓ Load Entire File (${fmtCount(context.total)} lines)`}</button
            >
          {:else}
            <button class="btn load-btn" disabled={ctxLoading} on:click={() => loadMore(1)}
              >{ctxLoading
                ? "Loading…"
                : `↓ Load ${fmtCount(Math.min(WIN_CHUNK, ctxMoreBelow))} more below`}</button
            >
            <span class="load-note"
              >{fmtCount(ctxMoreBelow)} later lines in this file</span
            >
          {/if}
        </div>
      {/if}
      {#if !context.lines.length}
        <div class="empty">Could not load this log section.</div>
      {/if}
    </div>
    <!-- Floating, because it's the escape hatch that stays reachable no matter
         where in the window the reader has scrolled to. -->
    <button
      class="open-with"
      on:click={openWithApp}
      title="Open this log in another application"
    >
      Open Full File
    </button>
  {:else}
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
        {#each results.hits as h, i (h.file + ":" + h.line_no)}
          <button
            class="hit"
            class:sel={selLines.has(i)}
            on:click={() => onRowClick(h)}
            on:mousedown={(e) => onRowDown(e, i)}
            on:mouseenter={() => onRowEnter(i)}
            on:contextmenu={(e) => onRowContext(e, i)}
          >
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

<!-- right-click menu for the selected log lines -->
{#if cmenu.visible}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div
    class="cmenu"
    style="left:{cmenu.x}px;top:{cmenu.y}px"
    on:click|stopPropagation
  >
    <button class="cmenu-item" on:click={copySelection}>
      Copy {selLines.size > 1 ? `${selLines.size} lines` : "line"}
    </button>
    <button class="cmenu-item" on:click={selectAllLines}>Select All</button>
  </div>
{/if}

{#if copyMsg}
  <div class="copy-toast" transition:fade={{ duration: 120 }}>{copyMsg}</div>
{/if}

<style>
  .logs-tab {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
    position: relative; /* anchors the floating Open Full File button */
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
  /* Rows are selected whole, so a drag must not start a native text selection. */
  .hit,
  .ctx-line {
    user-select: none;
  }
  .hit.sel,
  .hit.sel:hover,
  .ctx-line.sel {
    background: rgba(200, 169, 81, 0.16);
  }
  .sel-count {
    color: var(--accent);
  }
  .sel-hint {
    margin-left: auto;
    color: var(--text-muted);
    font-size: 10.5px;
    white-space: nowrap;
  }
  .cmenu {
    position: fixed;
    background: var(--bg-secondary);
    border: 1px solid var(--border-hover);
    border-radius: 4px;
    padding: 4px 0;
    z-index: 1000;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.6);
    min-width: 150px;
  }
  .cmenu-item {
    display: block;
    width: 100%;
    background: none;
    border: none;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 7px 14px;
    text-align: left;
  }
  .cmenu-item:hover {
    background: rgba(200, 169, 81, 0.1);
    color: var(--accent);
  }
  .copy-toast {
    position: fixed;
    left: 50%;
    bottom: 26px;
    transform: translateX(-50%);
    z-index: 1000;
    background: var(--bg-secondary);
    border: 1px solid var(--border-hover);
    border-radius: 5px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.6);
    color: var(--text-primary);
    font-size: 12px;
    padding: 7px 14px;
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
  /* The row n/N is parked on. Distinct from .center, which stays marked as the
     line that opened the view no matter where the reader has navigated to. */
  .ctx-line.match-current {
    background: rgba(120, 170, 255, 0.16);
    color: var(--text-primary);
  }

  /* controls, once scoped to one file */
  .btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .btn:disabled:hover {
    color: var(--text-secondary);
    border-color: var(--border);
  }
  .in:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .arch.off {
    opacity: 0.45;
    cursor: default;
  }
  .btn.nav {
    padding: 3px 7px;
  }

  .ctx-range {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .load-row {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 6px 14px 8px;
  }
  .load-btn {
    font-family: var(--font-sans, inherit);
  }
  .load-note {
    color: var(--text-muted);
    font-size: 11px;
    text-align: center;
  }

  /* Floating escape hatch. Sits above the scrolling lines and clear of the
     bottom load-row, so it's reachable at any scroll position. */
  .open-with {
    position: absolute;
    right: 16px;
    bottom: 16px;
    z-index: 2;
    background: var(--bg-panel);
    border: 1px solid var(--accent-dim);
    border-radius: 4px;
    color: var(--accent);
    cursor: pointer;
    font-size: 11px;
    padding: 6px 12px;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.45);
    opacity: 0.85;
  }
  .open-with:hover {
    opacity: 1;
    border-color: var(--accent);
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
