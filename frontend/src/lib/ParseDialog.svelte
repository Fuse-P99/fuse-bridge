<script>
  // The full damage parse for one fight, in the tabular form people actually
  // read after a kill. The DPS meter answers "who is carrying this right now";
  // this answers "what happened".
  //
  // Two rates, because they are different questions and the difference is the
  // whole point of the table:
  //   SDPS — damage over the WHOLE fight. The raid-wide comparison, and what
  //          the ranking is built on.
  //   DPS  — damage over the seconds that character was engaged. A rogue who
  //          zoned in at 60% has a poor SDPS and may still have the best DPS on
  //          the board; only one of those two numbers is about them.
  import { onMount } from "svelte";
  import { GetRaidParse } from "../../bindings/FuseBridge/app.js";

  export let mob = "";
  export let onClose;

  let loading = true;
  let data = null;
  let sortKey = "sdps";
  let sortDir = -1; // damage-shaped columns read best biggest-first

  const COLS = [
    { key: "rank", label: "Rank", num: true },
    { key: "name", label: "Name", num: false },
    { key: "pct", label: "% Total", num: true },
    { key: "total", label: "Damage", num: true },
    { key: "edps", label: "DPS", num: true },
    { key: "sdps", label: "SDPS", num: true },
    { key: "engaged_s", label: "Sec", num: true },
  ];

  onMount(async () => {
    try {
      data = await GetRaidParse(mob);
    } catch {
      data = null;
    }
    loading = false;
  });

  // Rank is assigned by SDPS before any sorting, so re-sorting the table by
  // another column doesn't renumber everyone — the rank is a property of the
  // fight, not of the current view.
  $: ranked = ((data && data.top) || []).map((p, i) => ({
    ...p,
    rank: i + 1,
  }));

  $: rows = (() => {
    const out = [...ranked];
    const k = sortKey;
    out.sort((a, b) => {
      if (k === "name") return a.name.localeCompare(b.name) * sortDir;
      const av = Number(a[k] || 0),
        bv = Number(b[k] || 0);
      if (av !== bv) return (av - bv) * sortDir;
      return a.name.localeCompare(b.name);
    });
    return out;
  })();

  // The mob is the fight, not a competitor in it: it never sorts, never ranks,
  // and its "share" is the whole thing by definition.
  $: mobRow = data
    ? {
        name: data.mob || mob || "Unknown",
        total: data.total || 0,
        dps: data.raid_dps || 0,
        secs: data.engaged_s || 0,
      }
    : null;

  function sortBy(key) {
    if (key === "rank") {
      sortKey = "sdps";
      sortDir = -1;
      return;
    }
    if (sortKey === key) sortDir = -sortDir;
    else {
      sortKey = key;
      // Names read A-Z first, numbers read biggest-first.
      sortDir = key === "name" ? 1 : -1;
    }
  }

  const num = (n) => Math.round(n || 0).toLocaleString();
  const pct = (n) => (n || 0).toFixed(1) + "%";
  function mmss(s) {
    s = Math.max(0, Math.round(s || 0));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }
</script>

<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="overlay" on:click|self={onClose}>
  <div class="modal">
    <div class="modal-title">Parse</div>

    {#if loading}
      <div class="none">Loading…</div>
    {:else if !data || !data.total}
      <div class="none">No damage recorded for this fight.</div>
    {:else}
      <div class="head">
        <span class="mobname">{mobRow.name}</span>
        <span class="meta">
          {num(mobRow.total)} damage · {mmss(mobRow.secs)} · {num(
            mobRow.dps,
          )} raid DPS
        </span>
      </div>

      <div class="scroll">
        <table class="char-table">
          <thead>
            <tr>
              {#each COLS as c}
                <th
                  class="sortable"
                  class:num={c.num}
                  class:sorted={sortKey === c.key ||
                    (c.key === "rank" && sortKey === "sdps")}
                  on:click={() => sortBy(c.key)}
                >
                  {c.label}{#if sortKey === c.key}<span class="arrow"
                      >{sortDir === 1 ? "▲" : "▼"}</span
                    >{/if}
                </th>
              {/each}
            </tr>
          </thead>
          <tbody>
            <!-- The mob is pinned above every sort. -->
            <tr class="mobrow">
              <td class="num">0</td>
              <td class="c-name">{mobRow.name}</td>
              <td class="num">100.0%</td>
              <td class="num">{num(mobRow.total)}</td>
              <td class="num">{num(mobRow.dps)}</td>
              <td class="num">{num(mobRow.dps)}</td>
              <td class="num">{mobRow.secs}</td>
            </tr>
            {#each rows as p (p.name)}
              <tr class:dead={p.dead}>
                <td class="num">{p.rank}</td>
                <td class="c-name"
                  >{p.name}{#if p.dead}<span class="tomb" title="Died">🪦</span
                    >{/if}</td
                >
                <td class="num">{pct(p.pct)}</td>
                <td class="num">{num(p.total)}</td>
                <td class="num">{num(p.edps)}</td>
                <td class="num">{num(p.sdps)}</td>
                <td class="num">{p.engaged_s}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="legend">
        <strong>DPS</strong> is over the seconds each character was engaged;
        <strong>SDPS</strong> is over the whole fight. Rank is by SDPS.
      </div>
    {/if}

    <div class="modal-actions">
      <button class="btn" on:click={onClose}>Close</button>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 120;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .modal {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 14px;
    width: 700px;
    max-width: 94vw;
    max-height: 85vh;
    display: flex;
    flex-direction: column;
    gap: 8px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    flex-wrap: wrap;
  }
  .mobname {
    font-size: 15px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .meta {
    font-size: 11px;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }
  .scroll {
    overflow: auto;
    min-height: 0;
  }
  .none {
    color: var(--text-muted);
    font-size: 12px;
    padding: 18px 2px;
  }
  .legend {
    font-size: 10px;
    color: var(--text-muted);
    line-height: 1.5;
  }
  .legend strong {
    color: var(--text-secondary);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
  }
  .btn {
    background: var(--bg-tertiary, rgba(255, 255, 255, 0.06));
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 5px 14px;
    font-size: 12px;
    cursor: pointer;
  }
  .btn:hover {
    background: rgba(255, 255, 255, 0.1);
  }

  /* Same table anatomy as the Characters tab. */
  .char-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .char-table thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--bg-secondary);
    color: var(--text-muted);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    text-align: left;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }
  .char-table thead th.num {
    text-align: right;
  }
  .char-table th.sortable {
    cursor: pointer;
    user-select: none;
  }
  .char-table th.sortable:hover {
    color: var(--text-primary);
  }
  .char-table th.sorted {
    color: var(--accent);
  }
  .char-table th .arrow {
    font-size: 8px;
    margin-left: 3px;
  }
  .char-table tbody tr {
    border-bottom: 1px solid rgba(37, 40, 54, 0.6);
  }
  .char-table tbody tr:hover {
    background: rgba(255, 255, 255, 0.04);
  }
  .char-table td {
    padding: 4px 8px;
    color: var(--text-secondary);
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .char-table td.c-name {
    color: var(--text-primary);
    font-weight: 600;
    font-variant-numeric: normal;
  }
  .char-table td.num {
    text-align: right;
  }
  .char-table tbody tr.mobrow {
    background: rgba(200, 169, 81, 0.1);
  }
  .char-table tbody tr.mobrow td {
    color: var(--accent);
    font-weight: 600;
    border-bottom: 1px solid var(--border);
  }
  .char-table tbody tr.dead td.c-name {
    color: #7a7a7a;
    text-decoration: line-through;
  }
  .tomb {
    margin-left: 4px;
  }
</style>
