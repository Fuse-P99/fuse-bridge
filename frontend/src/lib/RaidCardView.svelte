<script>
  import RaidAssignments from "./RaidAssignments.svelte";
  import RaidDebuffs from "./RaidDebuffs.svelte";
  import RaidClerics from "./RaidClerics.svelte";
  import OtherTimers from "./OtherTimers.svelte";
  import RaidDPS from "./RaidDPS.svelte";
  import AttendanceDialog from "./AttendanceDialog.svelte";
  import {
    GetRaidAttendance,
    GetItemByName,
    WhoHasItem,
    OpenPopout,
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "./scale.js";
  import ItemTipCard from "./ItemTipCard.svelte";
  import Icon from "./Icon.svelte";
  import { allianceGuild } from "./alliances.js";

  export let card;
  export let liveHP = null; // live client HP for the active raid; null = use card value

  // Event raids (Sky / HoT / Ring War) have no boss: no health bar, and an
  // extra row below the three sections for event timer bars.
  $: isEvent = card.kind === "event";
  // An event's whole channel set, in raid order. Server-sorted; the guard is
  // for older servers that don't send the field at all.
  $: chanList = card.discord_channels || [];
  // Other Timers row visibility, pushed up from the component (hidden rows
  // must not leave an empty gap in the card's flex column).
  let otherHas = false;
  // Raid DPS occupies the right half of the same row; either side alone is
  // enough to keep the row visible.
  let dpsHas = false;

  // ── loot item card ──────────────────────────────────────────────────────
  // The same card the Magelo sheet and the quest walkthrough show, so a drop
  // can be judged — including what it has historically sold for — without
  // leaving the raid. Items are fetched once per name and cached; a name the
  // item DB doesn't have says so rather than showing a blank card.
  let itemCache = {};
  let tip = null; // { name, item, x, y }

  // "Also held by" — the user's own characters holding the hovered loot
  // (local inventory dumps only; never guildmates).
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

  // Competition: every other guild in the raid zone, from the same /who
  // roster the Fuse columns come from. Never part of the Raiders count.
  // Sanctum's member guilds cluster into one entry, as on the Zones tab.
  // Opens guild → class → names. The server files class-hidden (/roleplay)
  // characters under "ROLE"; inside an alliance cluster a member's own guild
  // shows beside the name, since the row's isn't theirs.
  let openGuild = {};
  function toggleGuild(name) {
    openGuild = { ...openGuild, [name]: !openGuild[name] };
  }
  let openOther = {}; // "guild|class" → names shown
  function toggleOther(key) {
    openOther = { ...openOther, [key]: !openOther[key] };
  }
  const byLevelThenName = (a, b) =>
    (b.level || 0) - (a.level || 0) || a.name.localeCompare(b.name);
  function clusterOthers(list) {
    const byName = new Map();
    for (const g of list || []) {
      const alliance = allianceGuild(g.name);
      const key = alliance ? "Sanctum" : g.name;
      let e = byName.get(key);
      if (!e) {
        e = {
          name: key,
          alliance,
          total: 0,
          classes: new Map(),
          guilds: new Map(),
        };
        byName.set(key, e);
      }
      e.total += g.total || 0;
      if (alliance)
        e.guilds.set(g.name, (e.guilds.get(g.name) || 0) + (g.total || 0));
      for (const c of g.classes || []) {
        const members = e.classes.get(c.class) || [];
        for (const m of c.members || []) {
          members.push({ name: m.name, level: m.level || 0, guild: g.name });
        }
        e.classes.set(c.class, members);
      }
    }
    return [...byName.values()]
      .map((e) => {
        // Biggest class first; the ROLE bucket last whatever its size.
        const classes = [...e.classes.entries()]
          .map(([cls, members]) => ({
            class: cls,
            count: members.length,
            members: members.sort(byLevelThenName),
          }))
          .sort(
            (a, b) =>
              (a.class === "ROLE") - (b.class === "ROLE") ||
              b.count - a.count ||
              a.class.localeCompare(b.class),
          );
        return {
          name: e.name,
          alliance: e.alliance,
          total: e.total,
          classes,
          title: e.alliance
            ? "Alliance cluster: " +
              [...e.guilds.entries()]
                .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
                .map(([n, k]) => `${n} x${k}`)
                .join(", ")
            : "",
        };
      })
      .sort((a, b) => b.total - a.total || a.name.localeCompare(b.name));
  }
  $: otherGuilds = clusterOthers(card.raiders && card.raiders.others);

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
  <!-- Target Health (top) — meaningless for an event raid, which has no boss. -->
  {#if !isEvent}
    <div class="rc-target">
      <div class="rc-label">Target Health</div>
      <div class="rc-bar">
        <div class="rc-fill" style="width:{hp}%"></div>
        <span class="rc-bar-txt"
          >{card.status === "complete" ? "Dead" : hp + "%"}</span
        >
      </div>
    </div>
  {/if}

  <!-- The three raid sections are shared components — the Special Overlays
       (Raid Assignments / Raid Debuffs / Raid Clerics) render the same ones. -->
  <div class="rc-grid">
    <div class="rc-cell">
      <RaidAssignments {card} />
      {#if card.status !== "complete"}
        <button
          class="rc-pop"
          title="Pop out Raid Assignments as an overlay"
          on:click={() => OpenPopout("raidassign", "")}
          ><Icon name="popout" /></button
        >
      {/if}
    </div>
    <div class="rc-cell">
      <RaidDebuffs {card} />
      {#if card.status !== "complete"}
        <button
          class="rc-pop"
          title="Pop out Raid Debuffs as an overlay"
          on:click={() => OpenPopout("raiddebuffs", "")}
          ><Icon name="popout" /></button
        >
      {/if}
    </div>
    <div class="rc-cell">
      <RaidClerics {card} />
      {#if card.status !== "complete"}
        <button
          class="rc-pop"
          title="Pop out Raid Clerics as an overlay"
          on:click={() => OpenPopout("raidclerics", "")}
          ><Icon name="popout" /></button
        >
      {/if}
    </div>
  </div>

  <!-- Damage board on the left, raid/event timer bars (Ring War waves,
       Narandi, mob AE cooldowns) on the right. Vanishes entirely when neither
       has anything to say. -->
  <div class="rc-extra" class:hidden={!otherHas && !dpsHas}>
    <div class="rc-cell">
      <RaidDPS {card} bind:hasAny={dpsHas} />
      {#if card.status !== "complete"}
        <button
          class="rc-pop"
          title="Pop out Raid DPS as an overlay"
          on:click={() => OpenPopout("raiddps", "")}
          ><Icon name="popout" /></button
        >
      {/if}
    </div>
    <div class="rc-cell">
      <OtherTimers {card} bind:hasAny={otherHas} />
      {#if card.status !== "complete"}
        <button
          class="rc-pop"
          title="Pop out Raid Specific Timers as an overlay"
          on:click={() => OpenPopout("othertimers", "")}
          ><Icon name="popout" /></button
        >
      {/if}
    </div>
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
    {#if otherGuilds.length}
      <!-- Competition: the other guilds in the zone, four to a row like the
           role columns above. Collapsed to a count; opens to class counts.
           Not part of the Raiders total. -->
      <div class="rc-rcol-title rc-others-title">Competition</div>
      <div class="rc-raiders-cols">
        {#each otherGuilds as g (g.name)}
          <div class="rc-rcol">
            <div class="rc-class">
              <div
                class="rc-class-head has"
                role="button"
                tabindex="0"
                title={g.title || null}
                on:click={() => toggleGuild(g.name)}
                on:keydown={(e) => e.key === "Enter" && toggleGuild(g.name)}
              >
                <span class="rc-chev2">{openGuild[g.name] ? "▾" : "▸"}</span>
                <span class="rc-gname">{g.name}</span>
                {#if g.alliance}<span class="rc-alliance">alliance</span>{/if}
                <span class="rc-cnt">({g.total})</span>
              </div>
              {#if openGuild[g.name]}
                {#each g.classes as c (c.class)}
                  {@const ck = g.name + "|" + c.class}
                  <div class="rc-class rc-sub">
                    <div
                      class="rc-class-head has"
                      role="button"
                      tabindex="0"
                      on:click={() => toggleOther(ck)}
                      on:keydown={(e) => e.key === "Enter" && toggleOther(ck)}
                    >
                      <span class="rc-chev2">{openOther[ck] ? "▾" : "▸"}</span>
                      <span class="rc-abbr">{c.class}</span>
                      <span class="rc-cnt">({c.count})</span>
                    </div>
                    {#if openOther[ck]}
                      {#each c.members as m (m.name)}
                        <div class="rc-member">
                          {m.name}
                          {#if m.level}
                            ({m.level})
                          {/if}{#if g.alliance}
                            <span class="rc-disc">{m.guild}</span>{/if}
                        </div>
                      {/each}
                    {/if}
                  </div>
                {/each}
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
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
      <!-- Event raids run across a whole set of channels rather than one, and
           the set grows mid-raid (HoT adds an hour at a time), so they list
           every channel instead of linking "the" one. -->
      <div class="rc-label">
        {chanList.length ? "Discord Channels" : "Discord Channel"}
      </div>
      {#if chanList.length}
        <div class="rc-chans">
          {#each chanList as ch (ch.url)}
            <a
              class="rc-chanrow"
              class:logged={ch.logged}
              href={ch.url}
              target="_blank"
              rel="noreferrer"
              title={ch.name}
            >
              <span class="rc-chanrole">{ch.label}</span>
              {#if ch.logged}<span class="rc-chandone" title="Attendance posted"
                  >✓</span
                >{/if}
            </a>
          {/each}
        </div>
      {:else if card.discord_url}
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

<!-- Loot item card — the shared ItemTipCard every surface shows. -->
{#if tip}
  <ItemTipCard
    name={tip.name}
    item={tip.item}
    holders={tipHolders}
    x={tip.x}
    y={tip.y}
  />
{/if}

{#if attOpen}
  <AttendanceDialog
    heading="Attendance Logs — {card.target || card.label || card.zone}"
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
  .rc-extra {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
  /* display:none (not {#if}) so the OtherTimers poll loop stays mounted and
     can flip the row back on the moment a timer starts. */
  .rc-extra.hidden {
    display: none;
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
    .rc-bottom,
    .rc-extra {
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

  /* Competition block under the role columns. */
  .rc-others-title {
    margin-top: 6px;
  }
  /* A guild's class rows sit one step in; their names one step further via
     .rc-member's own margin. */
  .rc-sub {
    margin-left: 10px;
  }
  .rc-gname {
    font-weight: 500;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Marks the Sanctum cluster; the row title lists the member guilds. */
  .rc-alliance {
    border: 1px solid var(--accent-dim);
    color: var(--accent);
    border-radius: 8px;
    font-size: 9px;
    padding: 0 5px;
    margin-left: 5px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    flex-shrink: 0;
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
  /* Event channel set. Scrolls rather than stretching the card — a long HoT
     night runs to a dozen hour channels. */
  .rc-chans {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 132px;
    overflow-y: auto;
    min-width: 0;
  }
  .rc-chanrow {
    display: flex;
    align-items: center;
    gap: 5px;
    color: var(--accent);
    font-size: 12.5px;
    text-decoration: none;
    white-space: nowrap;
  }
  .rc-chanrow:hover {
    text-decoration: underline;
  }
  /* Already has attendance in it — muted, so the eye lands on the ones still
     needing logs. */
  .rc-chanrow.logged {
    color: var(--text-secondary);
  }
  .rc-chanrole {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .rc-chandone {
    color: var(--success);
    font-size: 11px;
    flex-shrink: 0;
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

  /* Pop-out button in each section's upper right — quiet until hovered. */
  .rc-cell {
    position: relative;
    min-width: 0;
  }
  .rc-pop {
    position: absolute;
    top: -3px;
    right: 0;
    background: none;
    border: none;
    padding: 2px;
    cursor: pointer;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1;
    opacity: 0.7;
  }
  .rc-cell:hover .rc-pop {
    opacity: 1;
  }
  .rc-pop:hover {
    color: var(--accent);
  }
</style>
