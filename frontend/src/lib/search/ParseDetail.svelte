<script>
  // Full per-toon breakdown of one archived parse, fetched by parse id — the
  // historical twin of ParseDialog (which reads the live board by mob name).
  // Same two rates, same table: SDPS over the whole fight (the ranking), DPS
  // over each character's engaged seconds.
  import { onMount } from "svelte";
  import { GetParseHistDetail } from "../../../bindings/FuseBridge/app.js";
  import Icon from "../Icon.svelte";

  export let parseId = 0;
  // Name of the character the user searched for — their row reads gold.
  export let highlight = "";
  export let onClose;

  $: hlLower = (highlight || "").trim().toLowerCase();
  // Parses fold a contributing pet into a "<Owner> + Pet" row — highlight
  // that form too.
  function isHl(name) {
    if (!hlLower) return false;
    const n = (name || "").toLowerCase();
    return n === hlLower || n === hlLower + " + pet";
  }

  let loading = true;
  let data = null;
  let err = "";
  let sortKey = "sdps";
  let sortDir = -1;

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
      data = await GetParseHistDetail(parseId);
    } catch (e) {
      err = String(e?.message || e);
    }
    loading = false;
  });

  // Rank is by SDPS, fixed before any view sort (a property of the fight).
  $: ranked = ((data && data.rows) || [])
    .slice()
    .sort((a, b) => b.sdps - a.sdps)
    .map((p, i) => ({ ...p, rank: i + 1 }));

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

  function sortBy(key) {
    if (key === "rank") {
      sortKey = "sdps";
      sortDir = -1;
      return;
    }
    if (sortKey === key) sortDir = -sortDir;
    else {
      sortKey = key;
      sortDir = key === "name" ? 1 : -1;
    }
  }

  const num = (n) => Math.round(n || 0).toLocaleString();
  const pct = (n) => (n || 0).toFixed(1) + "%";
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
</script>

<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="overlay" on:click|self={onClose}>
  <div class="modal">
    <div class="modal-title">Parse</div>

    {#if loading}
      <div class="none">Loading…</div>
    {:else if err}
      <div class="none">{err}</div>
    {:else if !data || !data.total_damage}
      <div class="none">No damage recorded for this parse.</div>
    {:else}
      <div class="head">
        <span class="mobname">{data.mob || "Unknown"}</span>
        <span class="meta">
          {fmtDate(data.battle_at_ms)} · {num(data.total_damage)} damage · {mmss(
            data.duration_s,
          )} · {num(data.raid_dps)} raid DPS
        </span>
      </div>

      <div class="scroll">
        <table>
          <thead>
            <tr>
              {#each COLS as c}
                <th
                  class="sortable"
                  class:num={c.num}
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
            {#each rows as p (p.name)}
              <tr class:dead={p.died} class:hl={isHl(p.name)}>
                <td class="num">{p.rank}</td>
                <td class="c-name"
                  >{p.name}{#if p.died}<span class="tomb" title="Died"
                      ><Icon name="headstone" /></span
                    >{/if}{#if p.tank}<span class="tanktag" title="Tank"
                      ><Icon name="shield" /></span
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
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th {
    text-align: left;
    padding: 5px 8px;
    color: var(--text-muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    background: var(--bg-secondary);
    cursor: pointer;
    user-select: none;
  }
  th.num {
    text-align: right;
  }
  .arrow {
    margin-left: 3px;
    color: var(--accent);
  }
  td {
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
    color: var(--text-secondary);
  }
  td.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  tr:last-child td {
    border-bottom: none;
  }
  .c-name {
    color: var(--text-primary);
    font-weight: 600;
  }
  tr.dead .c-name {
    color: var(--text-muted);
  }
  tr.hl td {
    background: rgba(200, 169, 81, 0.1);
  }
  tr.hl .c-name {
    color: var(--accent);
  }
  .tomb,
  .tanktag {
    margin-left: 4px;
    font-size: 11px;
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
    background: var(--bg-panel);
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 4px;
    padding: 4px 14px;
    font-size: 12px;
    cursor: pointer;
  }
</style>
