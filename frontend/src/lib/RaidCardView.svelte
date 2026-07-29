<script>
  import RaidAssignments from "./RaidAssignments.svelte";
  import RaidDebuffs from "./RaidDebuffs.svelte";
  import RaidClerics from "./RaidClerics.svelte";
  import AttendanceDialog from "./AttendanceDialog.svelte";
  import {
    GetRaidAttendance,
    GetItemByName,
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "./scale.js";
  import { tipStats, TIP_RULE } from "./itemTip.js";

  export let card;
  export let liveHP = null; // live client HP for the active raid; null = use card value

  // ── loot item card ──────────────────────────────────────────────────────
  // The same card the Magelo sheet and the quest walkthrough show, so a drop
  // can be judged — including what it has historically sold for — without
  // leaving the raid. Items are fetched once per name and cached; a name the
  // item DB doesn't have says so rather than showing a blank card.
  let itemCache = {};
  let tip = null; // { name, item, x, y }

  async function showItemTip(e, name) {
    // Positioned inside the zoomed shell, so cursor coordinates divide by the
    // UI scale or the card drifts at Medium/Large.
    const z = $scale || 1;
    const pad = 14;
    tip = {
      name,
      item: itemCache[name] || null,
      x: Math.min(e.clientX / z + pad, window.innerWidth / z - 280),
      y: Math.min(e.clientY / z + pad, window.innerHeight / z - 320),
    };
    if (itemCache[name] === undefined) {
      try {
        const res = await GetItemByName(name);
        itemCache[name] = res && res.found ? res.item : null;
      } catch {
        itemCache[name] = null;
      }
      // Only adopt the result if the cursor is still on the same item.
      if (tip && tip.name === name) tip = { ...tip, item: itemCache[name] };
    }
  }
  function moveItemTip(e) {
    if (!tip) return;
    const z = $scale || 1;
    const pad = 14;
    tip = {
      ...tip,
      x: Math.min(e.clientX / z + pad, window.innerWidth / z - 280),
      y: Math.min(e.clientY / z + pad, window.innerHeight / z - 320),
    };
  }
  function hideItemTip() {
    tip = null;
  }

  // Attendance logs. A completed raid reads its stored snapshot (keyed by
  // raid_id); an active one has no ToD yet, so it takes a live capture of its
  // zone — which is why raid_id is only passed once the raid is complete.
  let attOpen = false;
  $: attRaidID = card.status === "complete" ? card.raid_id || 0 : 0;
  $: attZone = card.zone || "";
  $: attAvailable = attRaidID > 0 || attZone !== "";

  let openClass = {};
  function toggleClass(c) {
    openClass = { ...openClass, [c]: !openClass[c] };
  }

  $: hp =
    card.status === "complete"
      ? 0
      : liveHP != null && liveHP >= 0
        ? liveHP
        : (card.target_hp ?? 100);

  // Raiders grouped into 4 role columns.
  const RAIDER_COLS = [
    { title: "Priests", classes: ["CLR", "SHM", "DRU"] },
    { title: "Casters", classes: ["MAG", "WIZ", "ENC", "NEC"] },
    { title: "Tanks", classes: ["WAR", "SHD", "PAL"] },
    { title: "DPS", classes: ["ROG", "MNK", "RNG", "BRD"] },
  ];
  $: raiderMap = Object.fromEntries(
    ((card.raiders && card.raiders.groups) || []).map((g) => [g.class, g]),
  );
  // Reactive columns: counts/lists must be derived here (not via a helper
  // function called from the template) or Svelte won't re-render them when the
  // card data refreshes — counts would freeze at their mount-time values.
  // Also guards members:null (no one of that class) from the server.
  $: raiderCols = RAIDER_COLS.map((col) => ({
    title: col.title,
    groups: col.classes.map((ab) => {
      const g = raiderMap[ab];
      return g && g.members ? g : { class: ab, members: [] };
    }),
  }));
</script>

<div class="raidcard">
  <!-- Target Health (top) -->
  <div class="rc-target">
    <div class="rc-label">Target Health</div>
    <div class="rc-bar">
      <div class="rc-fill" style="width:{hp}%"></div>
      <span class="rc-bar-txt"
        >{card.status === "complete" ? "Dead" : hp + "%"}</span
      >
    </div>
  </div>

  <!-- The three raid sections are shared components — the Special Overlays
       (Raid Assignments / Raid Debuffs / Raid Clerics) render the same ones. -->
  <div class="rc-grid">
    <RaidAssignments {card} />
    <RaidDebuffs {card} />
    <RaidClerics {card} />
  </div>

  <!-- Raiders (4 role columns) -->
  <div class="rc-col">
    <div class="rc-label">
      Raiders <span class="rc-total"
        >{card.raiders ? card.raiders.total : 0}</span
      >
    </div>
    <div class="rc-raiders-cols">
      {#each raiderCols as col}
        <div class="rc-rcol">
          <div class="rc-rcol-title">{col.title}</div>
          {#each col.groups as g (g.class)}
            <div class="rc-class">
              <div
                class="rc-class-head"
                class:has={g.members.length}
                on:click={() => g.members.length && toggleClass(g.class)}
              >
                {#if g.members.length}<span class="rc-chev2"
                    >{openClass[g.class] ? "▾" : "▸"}</span
                  >{/if}
                <span class="rc-abbr">{g.class}</span>
                <span class="rc-cnt">({g.members.length})</span>
              </div>
              {#if openClass[g.class]}
                {#each g.members as m}
                  <div class="rc-member">
                    {m.name}
                    {#if m.level}
                      ({m.level})
                    {/if}{#if m.discord}
                      <span class="rc-disc">{m.discord}</span>{/if}
                  </div>
                {/each}
              {/if}
            </div>
          {/each}
        </div>
      {/each}
    </div>
  </div>

  <!-- Loot + Discord channel -->
  <div class="rc-bottom">
    <div class="rc-col">
      <div class="rc-label">Loot</div>
      {#if card.loot && card.loot.length}
        {#each card.loot as l}
          <!-- svelte-ignore a11y-no-static-element-interactions -->
          <div
            class="rc-loot"
            on:mouseenter={(e) => showItemTip(e, l.name)}
            on:mousemove={moveItemTip}
            on:mouseleave={hideItemTip}
          >
            {#if l.wiki_url}<a
                href={l.wiki_url}
                target="_blank"
                rel="noreferrer">{l.name}</a
              >{:else}{l.name}{/if}
            {#if l.price}<span class="rc-price">{l.price}</span>{/if}
          </div>
        {/each}
      {:else}
        <div class="rc-none">No loot recorded</div>
      {/if}
    </div>
    <div class="rc-col">
      <div class="rc-label">Discord Channel</div>
      {#if card.discord_url}
        <a
          class="rc-chanlink"
          href={card.discord_url}
          target="_blank"
          rel="noreferrer">Open raid channel →</a
        >
      {:else}
        <div class="rc-none">Not linked yet</div>
      {/if}
      {#if attAvailable}
        <button class="rc-attbtn" on:click={() => (attOpen = true)}>
          Attendance Logs
        </button>
      {/if}
    </div>
  </div>
</div>

<!-- Loot item card, the same one the Magelo sheet shows for inventory. -->
{#if tip}
  <div class="rc-tip" style="left:{tip.x}px;top:{tip.y}px">
    <div class="rc-tip-name">{tip.name}</div>
    {#if tip.item}
      {#each tipStats(tip.item) as l}{#if l === TIP_RULE}<div
            class="rc-tip-rule"
          ></div>{:else}<div class="rc-tip-line">{l}</div>{/if}{/each}
    {:else}
      <div class="rc-tip-line rc-tip-dim">Not in the item DB yet.</div>
    {/if}
  </div>
{/if}

{#if attOpen}
  <AttendanceDialog
    heading="Attendance Logs — {card.target}"
    raidID={attRaidID}
    zone={attZone}
    load={() => GetRaidAttendance(attRaidID, attZone)}
    onClose={() => (attOpen = false)}
  />
{/if}

<style>
  .raidcard {
    padding: 8px 4px 6px 22px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .rc-grid {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 16px;
  }
  .rc-bottom {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
  .rc-raiders-cols {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 4px 14px;
  }
  .rc-rcol {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .rc-rcol-title {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-muted);
    margin-bottom: 2px;
  }
  @media (max-width: 720px) {
    .rc-grid,
    .rc-bottom {
      grid-template-columns: 1fr;
    }
    .rc-raiders-cols {
      grid-template-columns: 1fr 1fr;
    }
  }

  .rc-col {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  /* All section headers gold */
  .rc-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e3a008;
    margin-bottom: 3px;
  }

  .rc-total {
    color: var(--text-primary);
    font-weight: 400;
  }
  .rc-class {
    display: flex;
    flex-direction: column;
  }
  .rc-class-head {
    display: flex;
    align-items: center;
    font-size: 12px;
    color: var(--text-muted);
  }
  .rc-class-head.has {
    cursor: pointer;
    color: var(--text-primary);
  }
  .rc-abbr {
    font-weight: 500;
    min-width: 20px;
  }
  .rc-cnt {
    color: var(--text-accent);
    margin-left: 5px;
  }
  .rc-chev2 {
    font-size: 16px;
  }
  .rc-member {
    font-size: 12px;
    color: var(--text-secondary);
    margin-left: 5px;
    display: flex;
  }
  .rc-disc {
    color: var(--text-muted);
    margin-left: auto;
  }

  .rc-target {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .rc-bar {
    position: relative;
    height: 20px;
    border-radius: 4px;
    overflow: hidden;
    background: #3a1414;
    border: 1px solid #5c2020;
  }
  .rc-fill {
    position: absolute;
    inset: 0 auto 0 0;
    background: linear-gradient(90deg, #b91c1c, #ef4444);
    transition: width 0.8s ease;
  }
  .rc-bar-txt {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 700;
    color: #fff;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.6);
  }

  .rc-loot {
    font-size: 13px;
    color: var(--text-primary);
    min-width: 50%;
    display: flex;
  }
  /* ── loot item card ── */
  .rc-tip {
    position: fixed;
    z-index: 500;
    width: 260px;
    background: rgba(10, 12, 18, 0.97);
    border: 1px solid var(--accent-dim);
    border-radius: 5px;
    padding: 8px 10px;
    pointer-events: none;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.6);
  }
  .rc-tip-name {
    font-size: 12.5px;
    font-weight: 700;
    color: var(--accent);
    margin-bottom: 4px;
  }
  .rc-tip-line {
    font-size: 11px;
    color: var(--text-primary);
    line-height: 1.5;
  }
  /* Splits the item's stats from what it costs — two different kinds of fact
     that otherwise run together as one wall of lines. */
  .rc-tip-rule {
    height: 1px;
    margin: 5px 0 4px;
    background: rgba(255, 255, 255, 0.14);
  }
  .rc-tip-dim {
    color: var(--text-muted);
    font-style: italic;
  }
  .rc-loot a {
    color: var(--accent);
    text-decoration: none;
  }
  .rc-loot a:hover {
    text-decoration: underline;
  }
  .rc-price {
    color: #e3a008;
    font-size: 12px;
    margin-left: auto;
  }
  .rc-chanlink {
    color: var(--accent);
    font-size: 13px;
    text-decoration: none;
  }
  .rc-chanlink:hover {
    text-decoration: underline;
  }
  .rc-none {
    font-size: 13px;
    color: var(--text-muted);
    font-style: italic;
  }
  /* Sits under the channel link — the fallback for when there's no channel to
     open, and a shortcut for re-posting when there is. */
  .rc-attbtn {
    align-self: flex-start;
    margin-top: 6px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-family: inherit;
    font-size: 11px;
    padding: 3px 9px;
  }
  .rc-attbtn:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
</style>
