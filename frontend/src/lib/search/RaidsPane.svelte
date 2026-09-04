<script context="module">
  // Survives sub-tab switches.
  let savedRows = [];
  let savedMob = "";
  let savedZone = "";
  let savedRange = "all";
</script>

<script>
  // Search → Raids: the permanent completed-raid history (raidinfo_raids +
  // attendance snapshots never expire — only the live cards on the Raids tab
  // do, after 2 hours). Search by mob and/or zone; expand a row for loot and
  // the stored /who roster; jump to the fight's damage parse when one exists.
  import {
    SearchRaidHistory,
    GetRaidAttendance,
  } from "../../../bindings/FuseBridge/app.js";
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
  let zone = savedZone;
  let range = savedRange;
  let loading = false;
  let searched = savedRows.length > 0;
  let err = "";
  let more = false;
  let openRaid = 0; // expanded raid_id
  let openParse = 0;
  let att = null;
  let attLoading = false;

  $: savedRows = rows;
  $: savedMob = mob;
  $: savedZone = zone;
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
    try {
      const offset = append ? rows.length : 0;
      const got =
        (await SearchRaidHistory(
          mob.trim(),
          zone.trim(),
          fromMs(),
          0,
          offset,
        )) || [];
      rows = append ? [...rows, ...got] : got;
      more = got.length === 25;
      searched = true;
      if (!append) {
        openRaid = 0;
        att = null;
      }
    } catch (e) {
      err = String(e?.message || e);
    }
    loading = false;
  }

  async function toggle(r) {
    if (openRaid === r.raid_id) {
      openRaid = 0;
      att = null;
      return;
    }
    openRaid = r.raid_id;
    att = null;
    if (r.has_attendance) {
      attLoading = true;
      try {
        att = await GetRaidAttendance(r.raid_id, r.zone);
      } catch {
        att = null;
      }
      attLoading = false;
    }
  }

  function onKey(e) {
    if (e.key === "Enter") search(false);
  }

  function fmtDate(ms) {
    if (!ms) return "";
    return new Date(ms).toLocaleString([], {
      dateStyle: "medium",
      timeStyle: "short",
    });
  }
  function durMin(r) {
    if (!r.started_at_ms || !r.kill_time_ms || r.kill_time_ms <= r.started_at_ms)
      return "";
    return `${Math.round((r.kill_time_ms - r.started_at_ms) / 60000)}m`;
  }
  // Loot rows come as "[Item](wiki-url) -> Winner (dkp)" — split the markdown
  // link out so the item reads as a link and the rest as text.
  function lootParts(s) {
    const m = /^\[([^\]]+)\]\(([^)]+)\)(.*)$/.exec(s || "");
    if (!m) return { name: s, url: "", rest: "" };
    return { name: m[1], url: m[2], rest: m[3] };
  }
</script>

<div class="raids-pane">
  {#if !$linked}
    <div class="empty">
      <div class="big">Link your Discord account</div>
      <div class="hint">Raid history requires a verified Fuse membership.</div>
      <button class="link-btn" on:click={() => activeTab.set("general")}
        >Link your account on the General tab →</button
      >
    </div>
  {:else}
    <div class="controls">
      <input
        class="inp"
        placeholder="Mob (e.g. Statue)"
        bind:value={mob}
        on:keydown={onKey}
      />
      <input
        class="inp"
        placeholder="Zone (e.g. Kael)"
        bind:value={zone}
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
          Search every completed raid — kill times, rosters, and loot, all the
          way back.
        </div>
      {:else if !rows.length}
        <div class="msg">No raids found.</div>
      {:else}
        <table>
          <thead>
            <tr>
              <th>Date</th>
              <th>Mob</th>
              <th>Zone</th>
              <th class="num">Duration</th>
              <th class="num">Raiders</th>
              <th class="num">Loot</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as r (r.raid_id)}
              <tr
                class="rowbtn"
                class:open={openRaid === r.raid_id}
                on:click={() => toggle(r)}
              >
                <td class="date">{fmtDate(r.kill_time_ms)}</td>
                <td class="c-name">{r.mob}</td>
                <td>{r.zone || "—"}</td>
                <td class="num">{durMin(r) || "—"}</td>
                <td class="num">{r.players || "—"}</td>
                <td class="num">{r.loot.length || "—"}</td>
              </tr>
              {#if openRaid === r.raid_id}
                <tr class="detail">
                  <td colspan="6">
                    <div class="d-actions">
                      {#if r.parse_id}
                        <button
                          class="act gold"
                          on:click|stopPropagation={() =>
                            (openParse = r.parse_id)}>View damage parse →</button
                        >
                      {/if}
                    </div>
                    {#if r.loot.length}
                      <div class="d-sec">Loot</div>
                      <ul class="lootlist">
                        {#each r.loot as l}
                          {@const lp = lootParts(l)}
                          <li>
                            {#if lp.url}
                              <a href={lp.url} target="_blank" rel="noreferrer"
                                >{lp.name}</a
                              >{lp.rest}
                            {:else}
                              {lp.name}
                            {/if}
                          </li>
                        {/each}
                      </ul>
                    {/if}
                    {#if attLoading}
                      <div class="d-sec">Loading attendance…</div>
                    {:else if att && att.chunks && att.chunks.length}
                      <div class="d-sec">
                        Attendance — {att.players} raider{att.players === 1
                          ? ""
                          : "s"}
                      </div>
                      {#each att.chunks as c}
                        {#if c.kind === "who"}
                          <pre class="who">{c.text}</pre>
                        {/if}
                      {/each}
                    {:else if r.has_attendance === false}
                      <div class="d-sec dim">
                        No attendance snapshot was captured for this raid.
                      </div>
                    {/if}
                  </td>
                </tr>
              {/if}
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
  <ParseDetail parseId={openParse} onClose={() => (openParse = 0)} />
{/if}

<style>
  .raids-pane {
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
  .rowbtn:hover td,
  .rowbtn.open td {
    background: var(--bg-panel);
  }
  .c-name {
    color: var(--text-primary);
    font-weight: 600;
  }
  .date {
    white-space: nowrap;
  }
  .detail td {
    background: var(--bg-panel);
    padding: 10px 14px;
  }
  .d-actions {
    margin-bottom: 6px;
  }
  .act {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 2px 8px;
    font-size: 11px;
    cursor: pointer;
  }
  .act.gold {
    color: var(--accent);
    border-color: var(--accent-dim, var(--border));
  }
  .d-sec {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    margin: 8px 0 4px;
  }
  .d-sec.dim {
    text-transform: none;
    letter-spacing: normal;
  }
  .lootlist {
    margin: 0;
    padding-left: 18px;
    font-size: 12px;
  }
  .lootlist a {
    color: #60a5fa;
    text-decoration: none;
  }
  .lootlist a:hover {
    text-decoration: underline;
  }
  .who {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 8px;
    margin: 0 0 6px;
    white-space: pre-wrap;
    max-height: 300px;
    overflow: auto;
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
