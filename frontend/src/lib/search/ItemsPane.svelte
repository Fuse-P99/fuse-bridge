<script context="module">
  // Survives sub-tab switches.
  let savedQuery = "";
  let savedCls = "";
  let savedSlot = "";
  let savedRows = [];
</script>

<script>
  // Search → Items: the server's item database as a sortable stat table.
  // Text search, or browse by Class/Slot with no text at all (the server
  // returns up to 200 usable items for a filter browse). Hovering an item
  // name shows the full wiki-style card — same card as the Magelo sheet and
  // quest editor.
  import { onDestroy } from "svelte";
  import {
    SearchItems,
    LookupItems,
    WhoHasItem,
  } from "../../../bindings/FuseBridge/app.js";
  import ItemTipCard from "../ItemTipCard.svelte";
  import { scale } from "../scale.js";

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
  // The DB's slot vocabulary (wiki "Slot:" lines, matched with LIKE).
  const SLOTS = [
    "AMMO",
    "ARMS",
    "BACK",
    "CHEST",
    "EAR",
    "FACE",
    "FEET",
    "FINGER",
    "HANDS",
    "HEAD",
    "LEGS",
    "NECK",
    "PRIMARY",
    "RANGE",
    "SECONDARY",
    "SHOULDERS",
    "WAIST",
    "WRIST",
  ];

  // Character stats only — item details (weight, size, slots, effects) live on
  // the hover card.
  const COLS = [
    { key: "ac", label: "AC" },
    { key: "hp", label: "HP" },
    { key: "mana", label: "Mana" },
    { key: "str", label: "STR" },
    { key: "sta", label: "STA" },
    { key: "agi", label: "AGI" },
    { key: "dex", label: "DEX" },
    { key: "wis", label: "WIS" },
    { key: "int", label: "INT" },
    { key: "cha", label: "CHA" },
    { key: "sv_fire", label: "SvF" },
    { key: "sv_cold", label: "SvC" },
    { key: "sv_disease", label: "SvD" },
    { key: "sv_poison", label: "SvP" },
    { key: "sv_magic", label: "SvM" },
  ];

  let query = savedQuery;
  let cls = savedCls;
  let slot = savedSlot;
  let rows = savedRows;
  let loading = false;
  let err = "";
  let searched = savedRows.length > 0;
  let capped = false;
  let debounceTimer;
  let seq = 0;
  let sortKey = "";
  let sortDir = -1;
  let tip = null; // { name, item, x, y }

  $: savedQuery = query;
  $: savedCls = cls;
  $: savedSlot = slot;
  $: savedRows = rows;

  function onInput() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(run, 350);
  }
  function onKey(e) {
    if (e.key === "Enter") {
      clearTimeout(debounceTimer);
      run();
    }
  }
  function onFilter() {
    clearTimeout(debounceTimer);
    run();
  }

  async function run() {
    const q = query.trim();
    const hasText = q.length >= 2;
    const hasFilter = cls !== "" || slot !== "";
    if (!hasText && !hasFilter) {
      rows = [];
      searched = false;
      err = "";
      return;
    }
    const mySeq = ++seq;
    loading = true;
    err = "";
    try {
      const names =
        (await SearchItems(hasText ? q : "", slot, cls, "")) || [];
      if (mySeq !== seq) return;
      capped = names.length >= (hasText ? 15 : 200);
      if (!names.length) {
        rows = [];
        searched = true;
        loading = false;
        return;
      }
      const res = await LookupItems(names);
      if (mySeq !== seq) return;
      const items = res.items || {};
      rows = names.map((n) => items[n.toLowerCase()]).filter(Boolean);
      searched = true;
    } catch (e) {
      if (mySeq === seq) err = String(e?.message || e);
    }
    if (mySeq === seq) loading = false;
  }

  function sortBy(key) {
    if (sortKey === key) sortDir = -sortDir;
    else {
      sortKey = key;
      // Names read A-Z first, stats read biggest-first.
      sortDir = key === "name" ? 1 : -1;
    }
  }

  $: shown = (() => {
    if (!sortKey) return rows;
    const out = [...rows];
    out.sort((a, b) => {
      if (sortKey === "name")
        return (a.name || "").localeCompare(b.name || "") * sortDir;
      const av = Number(a[sortKey] || 0),
        bv = Number(b[sortKey] || 0);
      if (av !== bv) return (av - bv) * sortDir;
      return (a.name || "").localeCompare(b.name || "");
    });
    return out;
  })();

  const cell = (v) => (v ? v : "");

  // "Held by" — the user's own characters holding the hovered item (local
  // inventory dumps only; never guildmates).
  let tipHolders = [];
  let tipHoldersFor = "";
  $: tipItemName = tip ? tip.name : "";
  $: if (tipItemName !== tipHoldersFor) loadTipHolders(tipItemName);
  async function loadTipHolders(name) {
    tipHoldersFor = name;
    tipHolders = [];
    if (!name) return;
    try {
      const hits = (await WhoHasItem(name)) || [];
      if (tipHoldersFor === name) tipHolders = hits;
    } catch {
      /* the footer is a bonus — a failed lookup shows nothing */
    }
  }

  // Item card on hover — positioned inside the zoomed shell, so cursor
  // coordinates divide by the UI scale (the CharactersTab qTip pattern).
  function tipPos(e) {
    const z = $scale || 1;
    const pad = 14;
    return {
      x: Math.min(e.clientX / z + pad, window.innerWidth / z - 280),
      y: Math.min(e.clientY / z + pad, window.innerHeight / z - 340),
    };
  }
  function showTip(e, it) {
    tip = { name: it.name, item: it, ...tipPos(e) };
  }
  function moveTip(e) {
    if (tip) tip = { ...tip, ...tipPos(e) };
  }
  function hideTip() {
    tip = null;
  }

  onDestroy(() => clearTimeout(debounceTimer));
</script>

<!-- Item stats are public wiki data — this pane works unlinked; membership
     only adds the DKP lines on the hover card (stripped server-side). -->
<div class="items-pane">
  <div class="controls">
      <input
        class="inp"
        placeholder="Search items… (2+ characters)"
        bind:value={query}
        on:input={onInput}
        on:keydown={onKey}
      />
      <label class="flabel"
        >Class
        <select class="inp sel" bind:value={cls} on:change={onFilter}>
          <option value="">All</option>
          {#each CLASSES as c}
            <option value={c}>{c}</option>
          {/each}
        </select>
      </label>
      <label class="flabel"
        >Slot
        <select class="inp sel" bind:value={slot} on:change={onFilter}>
          <option value="">All</option>
          {#each SLOTS as s}
            <option value={s}>{s}</option>
          {/each}
        </select>
      </label>
      {#if loading}<span class="dim">Searching…</span>{/if}
      {#if err}<span class="errline">{err}</span>{/if}
    </div>

    <div class="table-wrap">
      {#if !searched}
        <div class="msg">
          Search by name, or pick a Class or Slot to browse — stats sort on
          click, full item card on hover.
        </div>
      {:else if !shown.length}
        <div class="msg">No items found.</div>
      {:else}
        <table>
          <thead>
            <tr>
              <th class="c-name sortable" on:click={() => sortBy("name")}
                >Name{#if sortKey === "name"}<span class="arrow"
                    >{sortDir === 1 ? "▲" : "▼"}</span
                  >{/if}</th
              >
              {#each COLS as c}
                <th class="num sortable" on:click={() => sortBy(c.key)}
                  >{c.label}{#if sortKey === c.key}<span class="arrow"
                      >{sortDir === 1 ? "▲" : "▼"}</span
                    >{/if}</th
                >
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each shown as it (it.name)}
              <tr>
                <td class="c-name">
                  {#if it.link}
                    <a
                      href={it.link}
                      target="_blank"
                      rel="noreferrer"
                      on:mouseenter={(e) => showTip(e, it)}
                      on:mousemove={moveTip}
                      on:mouseleave={hideTip}>{it.name}</a
                    >
                  {:else}
                    <!-- svelte-ignore a11y-no-static-element-interactions -->
                    <span
                      on:mouseenter={(e) => showTip(e, it)}
                      on:mousemove={moveTip}
                      on:mouseleave={hideTip}>{it.name}</span
                    >
                  {/if}
                </td>
                {#each COLS as c}
                  <td class="num">{cell(it[c.key])}</td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
        {#if capped}
          <div class="capnote">
            Showing the first {query.trim().length >= 2 ? 15 : 200} matches —
            narrow the search to see the rest.
          </div>
        {/if}
      {/if}
    </div>
</div>

{#if tip}
  <ItemTipCard
    name={tip.name}
    item={tip.item}
    holders={tipHolders}
    x={tip.x}
    y={tip.y}
  />
{/if}

<style>
  .items-pane {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }
  .controls {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 10px;
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
    width: 240px;
  }
  .inp:focus {
    outline: none;
    border-color: var(--border-hover);
  }
  .flabel {
    display: flex;
    align-items: center;
    gap: 5px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .sel {
    width: auto;
  }
  .dim {
    color: var(--text-muted);
    font-size: 11px;
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
  th.sortable {
    cursor: pointer;
    user-select: none;
  }
  th.num {
    text-align: right;
  }
  .arrow {
    margin-left: 2px;
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
    min-width: 200px;
  }
  .c-name a,
  .c-name span {
    color: var(--accent);
    font-weight: 600;
    text-decoration: none;
  }
  .c-name a:hover {
    text-decoration: underline;
  }
  .capnote {
    padding: 8px 12px;
    color: var(--text-muted);
    font-size: 11px;
    border-top: 1px solid var(--border);
  }
</style>
