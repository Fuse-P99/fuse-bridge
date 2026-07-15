<script>
  import { onMount, onDestroy, tick } from "svelte";
  import { GetZones, GetToonIdentities } from "../../wailsjs/go/main/App";
  import { classAbbr } from "../lib/classAbbr.js";

  let zones = [];
  let selectedZone = "";
  let error = "";
  let interval;
  let identities = {}; // lower(toon name) -> Discord identity (Fuse members)
  let zonesLoaded = false; // guards pin-pruning until the first load returns

  // Pinned zones float to the top of the list. Persisted across restarts, but
  // a pin is dropped once its zone ages out of the live data (see prune below),
  // so a stale pin never resurfaces when that zone reappears later.
  const PIN_KEY = "fuse.pinnedZones";
  function loadPins() {
    try {
      return new Set(JSON.parse(localStorage.getItem(PIN_KEY) || "[]"));
    } catch {
      return new Set();
    }
  }
  function savePins() {
    try {
      localStorage.setItem(PIN_KEY, JSON.stringify([...pinnedZones]));
    } catch {}
  }
  let pinnedZones = loadPins();
  function togglePin(name) {
    const s = new Set(pinnedZones);
    s.has(name) ? s.delete(name) : s.add(name);
    pinnedZones = s;
    savePins();
  }

  function identityFor(name) {
    return identities[(name || "").toLowerCase()] || "";
  }

  // search
  let query = "";
  let matchIdx = 0;
  let matchCount = 0;
  let detailEl;

  // tree expansion state (reassigned on toggle so Svelte re-renders)
  let collapsedGuilds = new Set(); // guilds the user collapsed (default: expanded)
  let expandedClasses = new Set(); // "guild::class" the user expanded (default: collapsed)

  function since(dateStr) {
    const mins = Math.floor((Date.now() - new Date(dateStr).getTime()) / 60000);
    if (mins < 1) return "just now";
    if (mins === 1) return "1 minute ago";
    if (mins < 60) return `${mins} minutes ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs === 1) return "1 hour ago";
    return `${hrs} hours ago`;
  }

  const isHidden = (c) => c.class === "Anon" || c.class === "Role";

  // Quick filters shown above the search. Each is an independent toggle; when any
  // are active the listing shows characters matching ANY active filter (OR).
  //   ROLE = anonymous but guild IS shown; ANON = anonymous with no guild shown.
  const quickFilters = [
    { id: "lfg", label: "LFG", test: (c) => c.lfg },
    { id: "afk", label: "AFK", test: (c) => c.afk },
    { id: "role", label: "ROLE", test: (c) => isHidden(c) && !!c.guild },
    { id: "anon", label: "ANON", test: (c) => isHidden(c) && !c.guild },
  ];
  let activeFilters = new Set();
  function toggleFilter(id) {
    const s = new Set(activeFilters);
    s.has(id) ? s.delete(id) : s.add(id);
    activeFilters = s;
  }

  // Build a deterministically-ordered model so the view doesn't reshuffle on
  // each 10s reload. Fuse first, then guilds by descending size; classes alpha;
  // anon-with-guild ("roleplay") at the bottom of their guild; anon-without-guild
  // ("anonymous") at the very bottom.
  function buildModel(chars) {
    chars = chars || [];
    const guildMap = new Map();
    const anonymous = [];

    for (const c of chars) {
      if (isHidden(c) && !c.guild) {
        anonymous.push(c);
        continue;
      }
      const gname = c.guild || "(No Guild)";
      let g = guildMap.get(gname);
      if (!g) {
        g = { name: gname, classes: new Map(), roleplay: [], total: 0 };
        guildMap.set(gname, g);
      }
      g.total++;
      if (isHidden(c)) {
        g.roleplay.push(c);
      } else {
        if (!g.classes.has(c.class)) g.classes.set(c.class, []);
        g.classes.get(c.class).push(c);
      }
    }

    for (const g of guildMap.values()) {
      for (const arr of g.classes.values())
        arr.sort((a, b) => a.name.localeCompare(b.name));
      g.roleplay.sort((a, b) => a.name.localeCompare(b.name));
      g.classList = [...g.classes.keys()]
        .sort((a, b) => a.localeCompare(b))
        .map((cn) => ({ name: cn, members: g.classes.get(cn) }));
    }

    const guilds = [...guildMap.values()].sort((a, b) => {
      if (a.name === "Fuse") return -1;
      if (b.name === "Fuse") return 1;
      if (b.total !== a.total) return b.total - a.total;
      return a.name.localeCompare(b.name);
    });

    anonymous.sort((a, b) => a.name.localeCompare(b.name));
    return { guilds, anonymous, total: chars.length };
  }

  function lineFor(c) {
    const guild = c.guild ? ` <${c.guild}>` : "";
    const race = c.race ? ` (${c.race})` : "";
    const tags = `${c.afk ? " AFK" : ""}${c.lfg ? " LFG" : ""}`;
    if (c.class === "Anon")
      return `[ANONYMOUS] ${c.name}${race}${guild}${tags}`;
    if (c.class === "Role") return `[ROLEPLAY] ${c.name}${race}${guild}${tags}`;
    return `[${c.level} ${c.class}] ${c.name}${race}${guild}${tags}`;
  }

  // ── search / highlight ──────────────────────────────────────────────────────
  function esc(t) {
    return t.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }
  // q is passed explicitly (not closed over) so Svelte tracks `query` as a
  // dependency of each {@html hl(text, query)} and re-renders on every keystroke.
  function hl(text, q) {
    const safe = esc(text);
    if (!q) return safe;
    q = q.toLowerCase();
    const lower = text.toLowerCase();
    let out = "",
      last = 0;
    for (let p = 0; ; ) {
      const i = lower.indexOf(q, p);
      if (i === -1) break;
      out +=
        esc(text.slice(last, i)) +
        "<mark>" +
        esc(text.slice(i, i + q.length)) +
        "</mark>";
      last = i + q.length;
      p = last;
    }
    return out + esc(text.slice(last));
  }

  function applyMarks(scroll) {
    if (!detailEl) return;
    const marks = detailEl.querySelectorAll("mark");
    matchCount = marks.length;
    if (matchIdx >= matchCount) matchIdx = 0;
    marks.forEach((m, i) => m.classList.toggle("current", i === matchIdx));
    if (scroll && matchCount)
      marks[matchIdx]?.scrollIntoView({ block: "center", behavior: "smooth" });
  }
  async function refreshMarks(scroll) {
    await tick();
    applyMarks(scroll);
  }

  function onSearch(e) {
    query = e.target.value;
    matchIdx = 0;
    refreshMarks(true);
  }
  function prevMatch() {
    if (matchCount) {
      matchIdx = (matchIdx - 1 + matchCount) % matchCount;
      applyMarks(true);
    }
  }
  function nextMatch() {
    if (matchCount) {
      matchIdx = (matchIdx + 1) % matchCount;
      applyMarks(true);
    }
  }

  // ── tree toggles ─────────────────────────────────────────────────────────────
  function toggleGuild(name) {
    const s = new Set(collapsedGuilds);
    s.has(name) ? s.delete(name) : s.add(name);
    collapsedGuilds = s;
  }
  function toggleClass(key) {
    const s = new Set(expandedClasses);
    s.has(key) ? s.delete(key) : s.add(key);
    expandedClasses = s;
  }

  async function load() {
    try {
      zones = (await GetZones()) || [];
      error = "";
      zonesLoaded = true;
    } catch (e) {
      error = String(e);
    }
  }

  onMount(async () => {
    identities = (await GetToonIdentities().catch(() => ({}))) || {};
    await load();
    interval = setInterval(load, 10000);
  });
  onDestroy(() => clearInterval(interval));

  // Once the first load returns, drop any pin whose zone is no longer in the
  // live data (it aged out server-side). Guarded by zonesLoaded so an empty
  // pre-load list doesn't wipe pins on startup. Only reassign when something
  // actually changed to avoid a reactive loop.
  $: if (zonesLoaded) {
    const live = new Set(zones.map((z) => z.name));
    let changed = false;
    const next = new Set();
    for (const n of pinnedZones) {
      if (live.has(n)) next.add(n);
      else changed = true;
    }
    if (changed) {
      pinnedZones = next;
      savePins();
    }
  }

  // Pinned zones first, then most-recent /who (stable: name breaks ties),
  // so it stops reshuffling when no new data is arriving.
  $: sortedZones = [...zones].sort((a, b) => {
    const pa = pinnedZones.has(a.name);
    const pb = pinnedZones.has(b.name);
    if (pa !== pb) return pa ? -1 : 1;
    const d = new Date(b.last_seen).getTime() - new Date(a.last_seen).getTime();
    return d !== 0 ? d : a.name.localeCompare(b.name);
  });
  $: zone = sortedZones.find((z) => z.name === selectedZone) || sortedZones[0];

  // Apply the active quick filters (OR) to this zone's characters, then build the
  // view model from the result. No active filters => full roster.
  $: filteredChars = (() => {
    const chars = (zone && zone.characters) || [];
    if (!activeFilters.size) return chars;
    const preds = quickFilters
      .filter((f) => activeFilters.has(f.id))
      .map((f) => f.test);
    return chars.filter((c) => preds.some((p) => p(c)));
  })();
  $: model = buildModel(filteredChars);

  // Per-filter match counts for the current zone (shown on each chip).
  $: filterCounts = (() => {
    const chars = (zone && zone.characters) || [];
    const m = {};
    for (const f of quickFilters) m[f.id] = chars.filter(f.test).length;
    return m;
  })();

  // Reset class expansion when the displayed zone changes.
  let lastSelected = "";
  $: if (zone && zone.name !== lastSelected) {
    lastSelected = zone.name;
    expandedClasses = new Set();
    collapsedGuilds = new Set();
  }

  // While any quick filter is active, auto-expand every Guild → Class → Name
  // level so matches are immediately visible (re-applied on each rebuild so newly
  // arrived matches stay open). When filters are cleared, collapse back to the
  // default (guilds expanded, classes collapsed). `prevFilterActive` limits the
  // collapse to the active→inactive transition so normal browsing isn't reset.
  let prevFilterActive = false;
  $: {
    const active = activeFilters.size > 0;
    if (active) {
      const cls = new Set();
      for (const g of model.guilds) {
        for (const cl of g.classList) cls.add(g.name + "::" + cl.name);
        if (g.roleplay.length) cls.add(g.name + "::__rp");
      }
      if (model.anonymous.length) cls.add("::__anon");
      expandedClasses = cls;
      collapsedGuilds = new Set();
    } else if (prevFilterActive) {
      expandedClasses = new Set();
      collapsedGuilds = new Set();
    }
    prevFilterActive = active;
  }

  // Re-apply highlight (no scroll) whenever content or expansion changes — e.g.
  // the 10s poll rebuilds the DOM and would otherwise drop the current mark.
  // Explicit search/nav actions handle scrolling themselves.
  $: {
    model;
    collapsedGuilds;
    expandedClasses;
    if (detailEl) refreshMarks(false);
  }
</script>

<div class="zones">
  <div class="list">
    {#if !zones.length}
      <div class="empty">{error || "No zone data"}</div>
    {/if}
    {#each sortedZones as z (z.name)}
      <div
        class="zone-row"
        class:sel={zone && z.name === zone.name}
        role="button"
        tabindex="0"
        on:click={() => (selectedZone = z.name)}
        on:keydown={(e) => e.key === "Enter" && (selectedZone = z.name)}
      >
        <span class="zone-name">{z.name}</span>
        <span class="zone-ct">({z.characters?.length || 0})</span>
        <button
          class="pin"
          class:pinned={pinnedZones.has(z.name)}
          title={pinnedZones.has(z.name) ? "Unpin zone" : "Pin zone"}
          on:click|stopPropagation={() => togglePin(z.name)}>📌</button
        >
      </div>
    {/each}
  </div>

  <div class="detail" bind:this={detailEl}>
    {#if zone}
      <div class="search-row">
        <input
          class="search"
          type="text"
          placeholder="Search this zone…"
          value={query}
          on:input={onSearch}
        />
        {#if query && matchCount}
          <span class="match-info">{matchIdx + 1}/{matchCount}</span>
          <button class="nav" on:click={prevMatch} title="Previous">↑</button>
          <button class="nav" on:click={nextMatch} title="Next">↓</button>
        {:else if query}
          <span class="match-info">0/0</span>
        {/if}
      </div>
      <div class="quick-filters">
        {#each quickFilters as f (f.id)}
          <button
            class="btn"
            class:active={activeFilters.has(f.id)}
            disabled={!filterCounts[f.id] && !activeFilters.has(f.id)}
            on:click={() => toggleFilter(f.id)}
          >
            {f.label}{#if filterCounts[f.id]}<span class="qf-ct"
                >{filterCounts[f.id]}</span
              >{/if}
          </button>
        {/each}
        {#if activeFilters.size}
          <button class="btn" on:click={() => (activeFilters = new Set())}
            >Clear</button
          >
        {/if}
      </div>
      <div class="divider"></div>
      <div class="zone-header">
        {zone.name} ({model.total}) — Seen {since(zone.last_seen)}
      </div>

      <!-- Overview tree -->
      <div class="tree">
        {#each model.guilds as g (g.name)}
          <div class="row guild-row" on:click={() => toggleGuild(g.name)}>
            <span class="caret">{collapsedGuilds.has(g.name) ? "▸" : "▾"}</span>
            <span class="g-name">{@html hl(`<${g.name}>`, query)}</span>
            <span class="count">({g.total})</span>
          </div>
          {#if !collapsedGuilds.has(g.name)}
            {#each g.classList as cl (cl.name)}
              <div
                class="row class-row"
                on:click={() => toggleClass(g.name + "::" + cl.name)}
              >
                <span class="caret"
                  >{expandedClasses.has(g.name + "::" + cl.name)
                    ? "▾"
                    : "▸"}</span
                >
                <span class="c-name">{@html hl(cl.name, query)}</span>
                <span class="count">- {cl.members.length}</span>
              </div>
              {#if expandedClasses.has(g.name + "::" + cl.name)}
                {#each cl.members as m (m.name)}
                  <div class="row name-row">
                    <span
                      >{@html hl(m.name, query)}
                      {#if classAbbr(cl.name)}
                        - {m.level} {classAbbr(cl.name)}{/if}</span
                    >
                    {#if m.afk}<span class="badge afk">AFK</span>{/if}
                    {#if m.lfg}<span class="badge lfg">LFG</span>{/if}
                    {#if g.name === "Fuse" && identityFor(m.name)}<span
                        class="discord">{identityFor(m.name)}</span
                      >{/if}
                  </div>
                {/each}
              {/if}
            {/each}
            {#if g.roleplay.length}
              <div
                class="row class-row"
                on:click={() => toggleClass(g.name + "::__rp")}
              >
                <span class="caret"
                  >{expandedClasses.has(g.name + "::__rp") ? "▾" : "▸"}</span
                >
                <span class="c-name muted">Roleplay</span>
                <span class="count">- {g.roleplay.length}</span>
              </div>
              {#if expandedClasses.has(g.name + "::__rp")}
                {#each g.roleplay as m (m.name)}
                  <div class="row name-row">
                    <span>{@html hl(m.name, query)}</span>
                    {#if m.afk}<span class="badge afk">AFK</span>{/if}
                    {#if m.lfg}<span class="badge lfg">LFG</span>{/if}
                    {#if g.name === "Fuse" && identityFor(m.name)}<span
                        class="discord">{identityFor(m.name)}</span
                      >{/if}
                  </div>
                {/each}
              {/if}
            {/if}
          {/if}
        {/each}

        {#if model.anonymous.length}
          <div class="row guild-row" on:click={() => toggleClass("::__anon")}>
            <span class="caret"
              >{expandedClasses.has("::__anon") ? "▾" : "▸"}</span
            >
            <span class="g-name muted">Anonymous</span>
            <span class="count">({model.anonymous.length})</span>
          </div>
          {#if expandedClasses.has("::__anon")}
            {#each model.anonymous as m (m.name)}
              <div class="row name-row">
                <span>{@html hl(m.name, query)}</span>
                {#if m.afk}<span class="badge afk">AFK</span>{/if}
                {#if m.lfg}<span class="badge lfg">LFG</span>{/if}
              </div>
            {/each}
          {/if}
        {/if}
      </div>

      <div class="divider"></div>

      <!-- Complete zone listing -->
      <div class="listing">
        {#each model.guilds as g (g.name)}
          {#each g.classList as cl (cl.name)}
            {#each cl.members as m (m.name)}
              <div class="line">{@html hl(lineFor(m), query)}</div>
            {/each}
          {/each}
          {#each g.roleplay as m (m.name)}
            <div class="line">{@html hl(lineFor(m), query)}</div>
          {/each}
        {/each}
        {#each model.anonymous as m (m.name)}
          <div class="line">{@html hl(lineFor(m), query)}</div>
        {/each}
      </div>
    {:else}
      <div class="empty">Select a zone</div>
    {/if}
  </div>
</div>

<style>
  .zones {
    display: flex;
    height: 100%;
    overflow: hidden;
  }

  .list {
    width: 230px;
    min-width: 230px;
    overflow-y: auto;
    border-right: 1px solid var(--border);
    background: var(--bg-panel);
  }
  .zone-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 7px 12px;
    cursor: pointer;
    font-size: 12px;
    transition: background 0.1s;
    gap: 6px;
  }
  .zone-row:hover {
    background: rgba(255, 255, 255, 0.04);
  }
  .zone-row.sel {
    background: rgba(200, 169, 81, 0.12);
  }
  .pin {
    display: none;
    background: none;
    border: none;
    padding: 0;
    margin: 0;
    cursor: pointer;
    font-size: 11px;
    line-height: 1;
    flex-shrink: 0;
    opacity: 0;
    filter: grayscale(1);
    transition:
      opacity 0.1s,
      filter 0.1s;
  }
  .zone-row:hover .pin {
    display: block;
    opacity: 0.55;
  }

  .pin.pinned {
    display: block;
    opacity: 1;
    filter: grayscale(1);
  }
  .zone-name {
    color: var(--text-secondary);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .zone-row.sel .zone-name {
    color: var(--accent);
  }
  .zone-ct {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }

  .detail {
    flex: 1;
    overflow: auto;
    padding: 10px 14px;
  }

  .quick-filters {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 8px;
    flex-wrap: wrap;
  }
  /* Buttons match the Map tab: base = Reset, .active = Follow (active). */
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 8px;
  }
  .btn.active {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .qf-ct {
    margin-left: 4px;
    color: var(--text-muted);
    font-size: 10px;
  }
  .btn.active .qf-ct {
    color: var(--accent);
  }

  .badge {
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 0.05em;
    padding: 1px 5px;
    border-radius: 8px;
    line-height: 1.4;
    flex-shrink: 0;
  }
  .badge.afk {
    background: rgba(148, 163, 184, 0.18);
    color: #94a3b8;
  }
  .badge.lfg {
    background: rgba(74, 222, 128, 0.16);
    color: #4ade80;
  }

  .search-row {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 10px;
  }
  .search {
    flex: 1;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 5px 9px;
    outline: none;
  }
  .search:focus {
    border-color: var(--accent-dim);
  }
  .match-info {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .nav {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 2px 8px;
  }
  .nav:hover {
    border-color: var(--accent-dim);
    color: var(--accent);
  }

  .zone-header {
    color: var(--text-primary);
    font-size: 13px;
    font-weight: 600;
    margin-bottom: 8px;
  }

  /* tree */
  .tree {
    font-size: 12.5px;
    line-height: 1.5;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 2px 4px;
    border-radius: 3px;
    cursor: default;
  }
  .row:hover {
    background: rgba(255, 255, 255, 0.05);
  }
  .guild-row,
  .class-row {
    cursor: pointer;
  }

  .class-row {
    margin-left: 10px;
  }

  .caret {
    width: 11px;
    color: var(--text-muted);
    font-size: 10px;
    flex-shrink: 0;
  }
  .g-name {
    color: var(--accent);
    font-weight: 600;
  }
  .c-name {
    color: var(--text-secondary);
  }
  .name-row {
    padding-left: 34px;
    color: var(--text-secondary);
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .discord {
    margin-left: auto;
    color: var(--text-muted);
    font-size: 11px;
    font-style: italic;
    white-space: nowrap;
  }
  .count {
    color: var(--text-muted);
    font-size: 11px;
  }
  .muted {
    color: var(--text-muted);
    font-style: italic;
  }

  .divider {
    height: 1px;
    background: var(--border);
    margin: 12px 0;
  }

  /* full listing */
  .listing {
    font-family: var(--font-mono);
    font-size: 12.5px;
    line-height: 1.55;
    color: var(--text-secondary);
    user-select: text;
  }
  .line {
    padding: 1px 4px;
    border-radius: 3px;
    white-space: pre-wrap;
  }
  .line:hover {
    background: rgba(255, 255, 255, 0.05);
  }

  .empty {
    padding: 40px 20px;
    color: var(--text-muted);
    font-size: 12px;
    text-align: center;
  }
</style>
