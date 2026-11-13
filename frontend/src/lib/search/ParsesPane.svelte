<script context="module">
  // Survives sub-tab switches.
  let savedRows = [];
  let savedMob = "";
  let savedToon = "";
  let savedSearchedToon = "";
  let savedClasses = [];
  let savedSearchedClasses = [];
  let savedSearchedMode = "list";
  let savedRange = "all";
</script>

<script>
  // Search → Parses: the permanent damage-parse archive. Every raid kill with
  // a parse (native FuseBridge boards or pasted GamParse lines) is in the DB
  // forever; this is the first surface that can reach past the live boards'
  // few-hour window. Click any row for the fight's full breakdown.
  //
  // Two shapes of answer, decided by what was asked:
  //   - A LEADERBOARD — the best individual performances, one row each,
  //     ranked by DPS over seconds engaged the way the /parses command ranks —
  //     whenever a mob is named without a player, or any classes are picked
  //     (on the mob typed, or on every mob). The class picker is a multi-
  //     select, so "Monk, Rogue, Ranger" compares just those.
  //   - A LIST of parses, newest first — with nothing typed (browse the
  //     history), or with a player named (their fights, with their numbers).
  // The player box is idle while classes are picked; a single player's history
  // is the list search with their name.
  import { onDestroy } from "svelte";
  import {
    SearchParseHistory,
    SearchParseTop,
    SearchParseNames,
  } from "../../../bindings/FuseBridge/app.js";
  import { linked } from "../linkState.js";
  import { activeTab } from "../nav.js";
  import Icon from "../Icon.svelte";
  import ParseDetail from "./ParseDetail.svelte";

  const RANGES = [
    { id: "all", label: "All time", days: 0 },
    { id: "week", label: "Last week", days: 7 },
    { id: "month", label: "Last month", days: 31 },
    { id: "quarter", label: "3 months", days: 92 },
    { id: "year", label: "Last year", days: 366 },
  ];
  // The roster's class names, as the toons table spells them.
  const CLASSES = [
    "Bard",
    "Cleric",
    "Druid",
    "Enchanter",
    "Magician",
    "Monk",
    "Necromancer",
    "Paladin",
    "Ranger",
    "Rogue",
    "Shadow Knight",
    "Shaman",
    "Warrior",
    "Wizard",
  ];

  let rows = savedRows;
  let mob = savedMob;
  let toon = savedToon;
  let classes = savedClasses;
  // What the CURRENT results were searched for — pinned at search time so
  // editing the boxes can't relabel columns over the old data. The mode
  // decides which table the rows are drawn in; the classes decide its columns.
  let searchedToon = savedSearchedToon;
  let searchedClasses = savedSearchedClasses;
  let searchedMode = savedSearchedMode; // "list" | "top"
  let range = savedRange;
  let loading = false;
  let searched = savedRows.length > 0;
  let err = "";
  let more = false;
  let openParse = 0;
  // The name the detail view highlights: the searched player, or on a
  // leaderboard the character whose row was clicked.
  let openHighlight = "";

  $: savedRows = rows;
  $: savedMob = mob;
  $: savedToon = toon;
  $: savedSearchedToon = searchedToon;
  $: savedClasses = classes;
  $: savedSearchedClasses = searchedClasses;
  $: savedSearchedMode = searchedMode;
  $: savedRange = range;

  function fromMs() {
    const r = RANGES.find((x) => x.id === range);
    if (!r || !r.days) return 0;
    return Date.now() - r.days * 86400000;
  }

  async function search(append = false) {
    if (loading) return;
    loading = true;
    err = "";
    // Appends continue the search the rows came from; a fresh search adopts
    // whatever is in the boxes now. A mob without a player, or any classes
    // at all, asks for the leaderboard; otherwise it is the list of parses.
    const useClasses = append ? searchedClasses : classes.slice();
    const typed = toon.trim();
    const mode = append
      ? searchedMode
      : useClasses.length > 0 || (mob.trim() !== "" && typed === "")
        ? "top"
        : "list";
    const useToon = append ? searchedToon : mode === "top" ? "" : typed;
    try {
      const offset = append ? rows.length : 0;
      const got =
        (mode === "top"
          ? await SearchParseTop(
              mob.trim(),
              useClasses.join(","),
              fromMs(),
              0,
              offset,
            )
          : await SearchParseHistory(mob.trim(), useToon, fromMs(), 0, offset)) ||
        [];
      rows = append ? [...rows, ...got] : got;
      searchedToon = useToon;
      searchedClasses = useClasses;
      searchedMode = mode;
      more = got.length === 25;
      searched = true;
    } catch (e) {
      err = String(e?.message || e);
    }
    loading = false;
  }

  // ── class picker ────────────────────────────────────────────────────────
  // A checklist rather than a <select multiple>: the native control is a
  // scrolling box nobody can read the state of at a glance, and it can't say
  // "Any class". The button reads the selection; the menu edits it.
  let clsOpen = false;
  let clsWrap;
  function toggleClass(c) {
    classes = classes.includes(c)
      ? classes.filter((x) => x !== c)
      : [...classes, c];
  }
  function clearClasses() {
    classes = [];
    clsOpen = false;
  }
  // A click anywhere outside the picker closes it.
  function onWinDown(e) {
    if (clsOpen && clsWrap && !clsWrap.contains(e.target)) clsOpen = false;
  }
  $: classLabel =
    classes.length === 0
      ? "Any class"
      : classes.length <= 3
        ? CLASSES.filter((c) => classes.includes(c)).join(", ")
        : `${classes.length} classes`;

  // ── autocomplete ────────────────────────────────────────────────────────
  // The mob and player boxes suggest from what the archive can actually
  // answer for — mobs with a parse, characters with a row — after two
  // characters, debounced, with the Members box's dropdown. Arrow keys walk
  // the list, Enter takes the highlighted name or, with none highlighted,
  // runs the search as it always did.
  let mobSugs = [];
  let mobOpen = false;
  let mobSel = -1;
  let toonSugs = [];
  let toonOpen = false;
  let toonSel = -1;
  const sugTimers = {};
  const sugSeq = {};

  function setSugs(kind, list) {
    if (kind === "mob") {
      mobSugs = list;
      mobOpen = list.length > 0;
      mobSel = -1;
    } else {
      toonSugs = list;
      toonOpen = list.length > 0;
      toonSel = -1;
    }
  }
  function closeSugs(kind) {
    if (kind === "mob") mobOpen = false;
    else toonOpen = false;
  }
  function suggest(kind, q) {
    clearTimeout(sugTimers[kind]);
    q = q.trim();
    if (q.length < 2) {
      setSugs(kind, []);
      return;
    }
    sugTimers[kind] = setTimeout(async () => {
      const mine = (sugSeq[kind] = (sugSeq[kind] || 0) + 1);
      let got = [];
      try {
        got = (await SearchParseNames(kind, q)) || [];
      } catch {
        got = [];
      }
      // A slower answer to an older keystroke must not replace a newer one.
      if (mine === sugSeq[kind]) setSugs(kind, got);
    }, 250);
  }
  function pickSug(kind, name) {
    if (kind === "mob") mob = name;
    else toon = name;
    closeSugs(kind);
  }
  function sugKey(e, kind) {
    const open = kind === "mob" ? mobOpen : toonOpen;
    const list = kind === "mob" ? mobSugs : toonSugs;
    let sel = kind === "mob" ? mobSel : toonSel;
    if (open && list.length) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        sel = (sel + 1) % list.length;
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        sel = (sel - 1 + list.length) % list.length;
      } else if (e.key === "Escape") {
        closeSugs(kind);
        return;
      } else if (e.key === "Enter" && sel >= 0) {
        e.preventDefault();
        pickSug(kind, list[sel]);
        return;
      }
      if (kind === "mob") mobSel = sel;
      else toonSel = sel;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") return;
    }
    if (e.key === "Enter") {
      closeSugs(kind);
      search(false);
    }
  }
  onDestroy(() => {
    clearTimeout(sugTimers.mob);
    clearTimeout(sugTimers.toon);
  });

  function openRow(parseId, highlight) {
    openHighlight = highlight || "";
    openParse = parseId;
  }
  // An owner's "+ Pet" row highlights the owner in the detail view, which
  // already matches both spellings of the name.
  const baseName = (n) => (n || "").replace(/ \+ Pet$/, "");

  const num = (n) => Math.round(n || 0).toLocaleString();
  function mmss(s) {
    s = Math.max(0, Math.round(s || 0));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }
  function fmtDate(ms) {
    if (!ms) return "";
    return new Date(ms).toLocaleString([], {
      dateStyle: "medium",
      timeStyle: "short",
    });
  }

  $: toonSearch = searchedToon !== "";
  $: topSearch = searchedMode === "top";
  // One class named: the column carries its name and needs no class column.
  $: oneClass = searchedClasses.length === 1;
</script>

<svelte:window on:mousedown={onWinDown} />

<div class="parses-pane">
  {#if !$linked}
    <div class="empty">
      <div class="big">Link your Discord account</div>
      <div class="hint">Parse history requires a verified Fuse membership.</div>
      <button class="link-btn" on:click={() => activeTab.set("general")}
        >Link your account on the General tab →</button
      >
    </div>
  {:else}
    <div class="controls">
      <div class="acwrap">
        <input
          class="inp"
          placeholder="Mob (e.g. Vulak)"
          bind:value={mob}
          on:input={() => suggest("mob", mob)}
          on:keydown={(e) => sugKey(e, "mob")}
          on:blur={() => setTimeout(() => closeSugs("mob"), 150)}
        />
        {#if mobOpen}
          <div class="sugs">
            {#each mobSugs as s, i (s)}
              <button
                class="sug"
                class:active={i === mobSel}
                on:mousedown={() => pickSug("mob", s)}>{s}</button
              >
            {/each}
          </div>
        {/if}
      </div>
      <div class="acwrap">
        <input
          class="inp"
          placeholder={classes.length
            ? "Player (clear the classes to use)"
            : "Player (exact name)"}
          title={classes.length
            ? "A class search ranks every player of those classes; clear them to search one player's history."
            : ""}
          disabled={classes.length > 0}
          bind:value={toon}
          on:input={() => suggest("toon", toon)}
          on:keydown={(e) => sugKey(e, "toon")}
          on:blur={() => setTimeout(() => closeSugs("toon"), 150)}
        />
        {#if toonOpen}
          <div class="sugs">
            {#each toonSugs as s, i (s)}
              <button
                class="sug"
                class:active={i === toonSel}
                on:mousedown={() => pickSug("toon", s)}>{s}</button
              >
            {/each}
          </div>
        {/if}
      </div>
      <div class="acwrap" bind:this={clsWrap}>
        <button
          type="button"
          class="inp sel clsbtn"
          class:on={classes.length > 0}
          title="Rank the best performances of the classes picked, on the mob typed or on every mob"
          on:click={() => (clsOpen = !clsOpen)}>{classLabel} ▾</button
        >
        {#if clsOpen}
          <div class="sugs clsmenu">
            {#each CLASSES as c}
              <label class="clsrow">
                <input
                  type="checkbox"
                  checked={classes.includes(c)}
                  on:change={() => toggleClass(c)}
                />
                {c}
              </label>
            {/each}
            <button type="button" class="sug clsclear" on:click={clearClasses}
              >Any class</button
            >
          </div>
        {/if}
      </div>
      <select class="inp sel" bind:value={range}>
        {#each RANGES as r}
          <option value={r.id}>{r.label}</option>
        {/each}
      </select>
      <button class="go" disabled={loading} on:click={() => search(false)}
        >{loading ? "Searching…" : "Search"}</button
      >
      {#if err}<span class="errline">{err}</span>{/if}
    </div>

    <div class="table-wrap">
      {#if !searched}
        <div class="msg">
          Search the guild's full parse history — every recorded kill, all the
          way back. A mob alone ranks the top players on it; a player alone
          lists their fights; pick classes to compare just those.
        </div>
      {:else if !rows.length}
        <div class="msg">No parses found.</div>
      {:else if topSearch}
        <!-- The leaderboard: one row per character performance, ranked by
             DPS over seconds engaged (rank is the row's place in the ranked
             list, so pages keep counting). Only rows engaged for more than
             half their fight are ranked, as everywhere else. -->
        <table>
          <thead>
            <tr>
              <th class="num">Rank</th>
              <th>{oneClass ? searchedClasses[0] : "Player"}</th>
              {#if !oneClass}
                <th>Class</th>
              {/if}
              <th>Mob</th>
              <th>Date</th>
              <th class="num gold">DPS</th>
              <th class="num">Damage</th>
              <th class="num">% Total</th>
              <th class="num">Sec</th>
              <th class="num">Fight</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as r, i (r.parse_id + "|" + r.name)}
              <tr
                class="rowbtn"
                class:dead={r.died}
                on:click={() => openRow(r.parse_id, baseName(r.name))}
              >
                <td class="num">{i + 1}</td>
                <td class="c-name"
                  >{r.name}{#if r.died}<span class="tag" title="Died"
                      ><Icon name="headstone" /></span
                    >{/if}{#if r.tank}<span class="tag" title="Tank"
                      ><Icon name="shield" /></span
                    >{/if}</td
                >
                {#if !oneClass}
                  <td>{r.class}</td>
                {/if}
                <td>{r.mob}</td>
                <td class="date">{fmtDate(r.battle_at_ms)}</td>
                <td class="num gold">{num(r.dps)}</td>
                <td class="num">{num(r.damage)}</td>
                <td class="num">{(r.pct || 0).toFixed(1)}%</td>
                <td class="num">{r.engaged_s}</td>
                <td class="num">{mmss(r.duration_s)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <table>
          <thead>
            <tr>
              <th>Date</th>
              <th>Mob</th>
              {#if toonSearch}
                <th class="num gold">{searchedToon} DPS</th>
                <th class="num gold">{searchedToon} Dmg</th>
              {/if}
              <th class="num">Duration</th>
              <th class="num">Total</th>
              <th class="num">Raid DPS</th>
              {#if !toonSearch}
                <th class="num">Toons</th>
              {/if}
            </tr>
          </thead>
          <tbody>
            {#each rows as p (p.parse_id)}
              <tr
                class="rowbtn"
                on:click={() => openRow(p.parse_id, searchedToon)}
              >
                <td class="date">{fmtDate(p.battle_at_ms)}</td>
                <td class="c-name">{p.mob}</td>
                {#if toonSearch}
                  <td class="num gold">{num(p.toon_dps)}</td>
                  <td class="num gold">{num(p.toon_damage)}</td>
                {/if}
                <td class="num">{mmss(p.duration_s)}</td>
                <td class="num">{num(p.total_damage)}</td>
                <td class="num">{num(p.raid_dps)}</td>
                {#if !toonSearch}
                  <td class="num">{p.toon_count}</td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
        {#if more}
          <button class="more" disabled={loading} on:click={() => search(true)}
            >{loading ? "Loading…" : "Load more"}</button
          >
        {/if}
      {/if}
    </div>
  {/if}
</div>

{#if openParse}
  <ParseDetail
    parseId={openParse}
    highlight={openHighlight}
    onClose={() => (openParse = 0)}
  />
{/if}

<style>
  .parses-pane {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }
  .empty {
    padding: 40px 20px;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
  }
  .empty .big {
    font-size: 15px;
    font-weight: 600;
    color: var(--text-secondary);
    margin-bottom: 6px;
  }
  .empty .hint {
    margin-bottom: 12px;
  }
  .link-btn {
    background: none;
    border: 1px solid rgba(96, 165, 250, 0.55);
    color: #60a5fa;
    border-radius: 4px;
    padding: 4px 12px;
    font-size: 12px;
    cursor: pointer;
  }
  .controls {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 12px 8px;
    flex-wrap: wrap;
  }
  .inp {
    background: var(--bg-input);
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 4px;
    padding: 5px 8px;
    font-size: 12px;
    width: 180px;
  }
  .inp:focus {
    outline: none;
    border-color: var(--border-hover);
  }
  .sel {
    width: auto;
  }
  .go {
    background: var(--bg-panel);
    border: 1px solid var(--accent-dim, var(--border));
    color: var(--accent);
    border-radius: 4px;
    padding: 5px 14px;
    font-size: 12px;
    cursor: pointer;
  }
  .go:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .errline {
    color: var(--error, #e05c5c);
    font-size: 11px;
  }
  .table-wrap {
    flex: 1;
    min-height: 0;
    overflow: auto;
    margin: 0 12px 12px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg-secondary);
  }
  .msg {
    padding: 16px;
    color: var(--text-muted);
    font-size: 12px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th {
    text-align: left;
    padding: 6px 8px;
    color: var(--text-muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    background: var(--bg-secondary);
    z-index: 1;
  }
  th.num {
    text-align: right;
  }
  td {
    padding: 5px 8px;
    border-bottom: 1px solid var(--border);
    color: var(--text-secondary);
  }
  td.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .rowbtn {
    cursor: pointer;
  }
  .rowbtn:hover td {
    background: var(--bg-panel);
  }
  .c-name {
    color: var(--text-primary);
    font-weight: 600;
  }
  .date {
    white-space: nowrap;
  }
  th.gold,
  td.gold {
    color: var(--accent);
  }
  /* Class leaderboard: the death and tank marks the detail view uses, and a
     dead row's name dimmed the same way. */
  .tag {
    margin-left: 4px;
    font-size: 11px;
  }
  tr.dead .c-name {
    color: var(--text-muted);
  }
  .inp:disabled {
    opacity: 0.45;
    cursor: default;
  }
  /* Autocomplete dropdowns, the Members box's look: anchored under the input,
     over the table. */
  .acwrap {
    position: relative;
  }
  .sugs {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 50;
    background: var(--bg-panel);
    border: 1px solid var(--border-hover);
    border-radius: 4px;
    margin-top: 2px;
    max-height: 220px;
    overflow: auto;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.5);
  }
  .sug {
    display: block;
    width: 100%;
    background: none;
    border: none;
    color: var(--text-primary);
    text-align: left;
    padding: 5px 8px;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sug:hover,
  .sug.active {
    background: var(--bg-secondary);
    color: var(--accent);
  }
  /* Class picker: a button that reads like the selects beside it, and a
     checklist in the same dropdown as the suggestions. */
  .clsbtn {
    cursor: pointer;
    text-align: left;
    min-width: 130px;
    white-space: nowrap;
  }
  .clsbtn.on {
    color: var(--accent);
    border-color: var(--accent-dim, var(--border));
  }
  .clsmenu {
    min-width: 180px;
    padding: 4px 0;
  }
  .clsrow {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 10px;
    font-size: 12px;
    color: var(--text-primary);
    cursor: pointer;
    white-space: nowrap;
  }
  .clsrow:hover {
    background: var(--bg-secondary);
  }
  .clsrow input {
    accent-color: var(--accent);
    margin: 0;
  }
  .clsclear {
    border-top: 1px solid var(--border);
    color: var(--accent);
    margin-top: 4px;
  }
  .more {
    display: block;
    width: 100%;
    background: none;
    border: none;
    border-top: 1px solid var(--border);
    color: var(--accent);
    padding: 8px;
    font-size: 12px;
    cursor: pointer;
  }
</style>
