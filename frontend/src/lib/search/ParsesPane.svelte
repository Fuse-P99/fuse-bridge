<script context="module">
  // Survives sub-tab switches.
  let savedRows = [];
  let savedMob = "";
  let savedToon = "";
  let savedSearchedToon = "";
  let savedRange = "all";
</script>

<script>
  // Search → Parses: the permanent damage-parse archive. Every raid kill with
  // a parse (native FuseBridge boards or pasted GamParse lines) is in the DB
  // forever; this is the first surface that can reach past the live boards'
  // few-hour window. Search by mob (substring), by player (exact toon name),
  // or both; click a row for the full breakdown.
  import { SearchParseHistory } from "../../../bindings/FuseBridge/app.js";
  import { linked } from "../linkState.js";
  import { activeTab } from "../nav.js";
  import ParseDetail from "./ParseDetail.svelte";

  const RANGES = [
    { id: "all", label: "All time", days: 0 },
    { id: "week", label: "Last week", days: 7 },
    { id: "month", label: "Last month", days: 31 },
    { id: "quarter", label: "3 months", days: 92 },
    { id: "year", label: "Last year", days: 366 },
  ];

  let rows = savedRows;
  let mob = savedMob;
  let toon = savedToon;
  // The toon the CURRENT results were searched for — pinned at search time so
  // editing the input box can't relabel columns over the old data.
  let searchedToon = savedSearchedToon;
  let range = savedRange;
  let loading = false;
  let searched = savedRows.length > 0;
  let err = "";
  let more = false;
  let openParse = 0;

  $: savedRows = rows;
  $: savedMob = mob;
  $: savedToon = toon;
  $: savedSearchedToon = searchedToon;
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
    // whatever is in the boxes now.
    const useToon = append ? searchedToon : toon.trim();
    try {
      const offset = append ? rows.length : 0;
      const got =
        (await SearchParseHistory(mob.trim(), useToon, fromMs(), 0, offset)) ||
        [];
      rows = append ? [...rows, ...got] : got;
      searchedToon = useToon;
      more = got.length === 25;
      searched = true;
    } catch (e) {
      err = String(e?.message || e);
    }
    loading = false;
  }

  function onKey(e) {
    if (e.key === "Enter") search(false);
  }

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
</script>

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
      <input
        class="inp"
        placeholder="Mob (e.g. Vulak)"
        bind:value={mob}
        on:keydown={onKey}
      />
      <input
        class="inp"
        placeholder="Player (exact name)"
        bind:value={toon}
        on:keydown={onKey}
      />
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
          way back.
        </div>
      {:else if !rows.length}
        <div class="msg">No parses found.</div>
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
              <tr class="rowbtn" on:click={() => (openParse = p.parse_id)}>
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
    highlight={searchedToon}
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
