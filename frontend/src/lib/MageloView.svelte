<script>
  // Magelo sub-tab (officers only): a wiki-Magelo-style character sheet built
  // from the local inventory outputfile. Worn slots cluster around the class
  // animation; bags and bank render below; the left panel estimates stats
  // (race base + class bonuses + user-assigned creation points + gear).
  // Item data comes from the server's eqitems table via LookupItems — unknown
  // items are queued there for a wiki scrape and fill in on a later visit.
  import {
    LookupItems,
    SaveMagelo,
    PreviewItem,
    CommitItem,
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "./scale.js";

  export let charName = "";
  export let inventory = []; // InventoryItem[] {location, name, count}
  export let info = null; // {level, class, race}

  let itemsBy = {}; // lower(name) -> MageloItem
  let iconBase = "";
  let missing = [];
  let lookupErr = "";
  let loading = true;

  // ── worn slot layout (grid-template-areas around the class gif) ─────────────
  // No Charm slot: charm items don't exist in this era's expansions.
  const WORN_ORDER = [
    "Ear1",
    "Head",
    "Face",
    "Ear2",
    "Neck",
    "Shoulders",
    "Arms",
    "Back",
    "Wrist1",
    "Wrist2",
    "Finger1",
    "Finger2",
    "Chest",
    "Legs",
    "Waist",
    "Feet",
    "Primary",
    "Secondary",
    "Range",
    "Hands",
    "Ammo",
  ];

  // Inventory locations repeat for paired slots (Ear, Wrist, Fingers) — number
  // them in file order.
  function assignSlots(items) {
    const worn = {};
    const seen = {};
    for (const it of items) {
      const loc = it.location;
      let slot = null;
      if (loc === "Ear" || loc === "Wrist") {
        seen[loc] = (seen[loc] || 0) + 1;
        slot = loc + Math.min(seen[loc], 2);
      } else if (loc === "Fingers" || loc === "Finger") {
        seen.Finger = (seen.Finger || 0) + 1;
        slot = "Finger" + Math.min(seen.Finger, 2);
      } else if (WORN_ORDER.includes(loc)) {
        slot = loc;
      }
      if (slot) worn[slot] = it;
    }
    return worn;
  }
  $: worn = assignSlots(inventory);

  // Bags (General1-8) and bank (Bank1-8): the container item plus its
  // "-SlotN" contents, in file order.
  function containers(prefix) {
    const out = [];
    for (let i = 1; i <= 8; i++) {
      const bagLoc = prefix + i;
      const bag = inventory.find((x) => x.location === bagLoc);
      const contents = inventory.filter((x) =>
        x.location.startsWith(bagLoc + "-Slot"),
      );
      if (bag || contents.length) out.push({ label: bagLoc, bag, contents });
    }
    return out;
  }
  $: bags = containers("General");
  $: bank = containers("Bank");

  // map is passed explicitly at template call sites so Svelte re-renders the
  // cells when the lookup result arrives (a bare itemFor(inv) call would not
  // register itemsBy as a dependency).
  function itemFor(inv, map = itemsBy) {
    return inv ? (map || {})[inv.name.toLowerCase()] : null;
  }

  // EQ bags fill two columns in slot order (1 2 / 3 4 / …). The cell count
  // comes from the bag item's capacity stat, falling back to the highest
  // occupied slot when the bag isn't in the DB yet.
  function bagSlots(b, map) {
    const by = {};
    let maxSeen = 0;
    for (const c of b.contents) {
      const m = /-Slot(\d+)$/.exec(c.location);
      const n = m ? +m[1] : 0;
      if (n) {
        by[n] = c;
        maxSeen = Math.max(maxSeen, n);
      }
    }
    const bagIt = itemFor(b.bag, map);
    const cap = Math.max((bagIt && bagIt.capacity) || 0, maxSeen);
    const out = [];
    for (let n = 1; n <= cap; n++) out.push(by[n] || null);
    return out;
  }
  function wikiLink(name) {
    return "https://wiki.project1999.com/" + name.replace(/ /g, "_");
  }

  // ── item lookup ──────────────────────────────────────────────────────────────
  // Reactive, not mount-time: the inventory prop arrives asynchronously (and
  // can still be the previous character's at mount when the user clicks
  // between characters with the Magelo sub-tab open), so the lookup re-runs
  // whenever the set of item names actually changes.
  let lookedUp = null; // name-set fingerprint already looked up
  $: lookupNames = [...new Set(inventory.map((x) => x.name))];
  $: {
    const fp = lookupNames.join("|");
    if (fp !== lookedUp) {
      lookedUp = fp;
      runLookup(lookupNames);
    }
  }
  let lookupSeq = 0; // discard out-of-order responses (stale-then-fresh races)
  async function runLookup(names) {
    const seq = ++lookupSeq;
    loading = true;
    lookupErr = "";
    try {
      const res = await LookupItems(names);
      if (seq !== lookupSeq) return;
      itemsBy = res.items || {};
      iconBase = res.icon_url || "";
      missing = res.missing || [];
    } catch (e) {
      if (seq !== lookupSeq) return;
      lookupErr = String(e);
    }
    loading = false;
    // Persist this character's "current" magelo server-side (toon + per-slot
    // item references). Fire-and-forget — the view works regardless.
    if (names.length) SaveMagelo(charName).catch(() => {});
  }

  // ── Add Item (officer): link → server scrape preview → correct → commit ────
  let showAdd = false;
  let addLink = "";
  let addItem = null; // editable parsed record
  let addBusy = false;
  let addErr = "";
  let addMsg = "";
  const ADD_NUMS = [
    ["ac", "AC"],
    ["str", "STR"],
    ["sta", "STA"],
    ["agi", "AGI"],
    ["dex", "DEX"],
    ["wis", "WIS"],
    ["int", "INT"],
    ["cha", "CHA"],
    ["hp", "HP"],
    ["mana", "MANA"],
    ["sv_fire", "SV FIRE"],
    ["sv_cold", "SV COLD"],
    ["sv_disease", "SV DIS"],
    ["sv_poison", "SV POI"],
    ["sv_magic", "SV MAG"],
    ["dmg", "DMG"],
    ["delay", "DELAY"],
    ["range", "RANGE"],
    ["wt", "WT"],
    ["capacity", "CAP"],
    ["wr", "WR%"],
    ["charges", "CHARGES"],
  ];
  const ADD_TEXTS = [
    ["name", "Name"],
    ["slot", "Slot"],
    ["skill", "Skill"],
    ["effect", "Effect"],
    ["size", "Size"],
    ["size_capacity", "Size Cap"],
    ["classes", "Classes"],
    ["races", "Races"],
    ["era", "Era"],
    ["icon", "Icon file"],
    ["link", "Link"],
  ];
  const ADD_FLAGS = [
    ["magic", "MAGIC"],
    ["lore", "LORE"],
    ["nodrop", "NO DROP"],
    ["norent", "NO RENT"],
  ];
  async function addPreview() {
    addBusy = true;
    addErr = "";
    addMsg = "";
    try {
      addItem = await PreviewItem(addLink);
    } catch (e) {
      addErr = String(e);
    }
    addBusy = false;
  }
  async function addCommit() {
    if (!addItem) return;
    addBusy = true;
    addErr = "";
    try {
      await CommitItem(addItem);
      addMsg = `"${addItem.name}" saved to the item DB.`;
      // Light up any matching slots in the current view immediately.
      itemsBy = { ...itemsBy, [addItem.name.toLowerCase()]: addItem };
    } catch (e) {
      addErr = String(e);
    }
    addBusy = false;
  }
  function addClose() {
    showAdd = false;
    addLink = "";
    addItem = null;
    addErr = "";
    addMsg = "";
  }

  // ── hover tooltip ────────────────────────────────────────────────────────────
  let tip = null; // {inv, item, x, y}
  function showTip(e, inv) {
    // The tooltip is fixed-positioned INSIDE the zoomed shell (the A/A/A UI
    // scale), so cursor and viewport coordinates must be divided by the zoom
    // factor or the tip drifts down-right at Medium/Large.
    const z = $scale || 1;
    const pad = 14;
    tip = {
      inv,
      item: itemFor(inv),
      x: Math.min(e.clientX / z + pad, window.innerWidth / z - 280),
      y: Math.min(e.clientY / z + pad, window.innerHeight / z - 320),
    };
  }
  function hideTip() {
    tip = null;
  }
  function tipStats(it) {
    // Wiki-style stat lines assembled from the DB record.
    const lines = [];
    const flags = [
      it.magic && "MAGIC ITEM",
      it.lore && "LORE ITEM",
      it.nodrop && "NO DROP",
      it.norent && "NO RENT",
    ].filter(Boolean);
    if (flags.length) lines.push(flags.join("  "));
    if (it.slot) lines.push("Slot: " + it.slot);
    if (it.skill) lines.push("Skill: " + it.skill + "  Atk Delay: " + it.delay);
    if (it.dmg) lines.push("DMG: " + it.dmg + (it.ac ? "  AC: " + it.ac : ""));
    else if (it.ac) lines.push("AC: " + it.ac);
    const s = [];
    for (const [k, v] of [
      ["STR", it.str],
      ["STA", it.sta],
      ["AGI", it.agi],
      ["DEX", it.dex],
      ["WIS", it.wis],
      ["INT", it.int],
      ["CHA", it.cha],
      ["HP", it.hp],
      ["MANA", it.mana],
    ])
      if (v) s.push(`${k}: ${v > 0 ? "+" : ""}${v}`);
    if (s.length) lines.push(s.join("  "));
    const sv = [];
    for (const [k, v] of [
      ["SV FIRE", it.sv_fire],
      ["SV COLD", it.sv_cold],
      ["SV DISEASE", it.sv_disease],
      ["SV POISON", it.sv_poison],
      ["SV MAGIC", it.sv_magic],
    ])
      if (v) sv.push(`${k}: ${v > 0 ? "+" : ""}${v}`);
    if (sv.length) lines.push(sv.join("  "));
    if (it.effect) lines.push("Effect: " + it.effect);
    if (it.capacity)
      lines.push(
        `Capacity: ${it.capacity}  Size Capacity: ${it.size_capacity}` +
          (it.wr ? `  WR: ${it.wr}%` : ""),
      );
    lines.push(`WT: ${(it.wt || 0).toFixed(1)}  Size: ${it.size || "?"}`);
    if (it.classes) lines.push("Class: " + it.classes);
    if (it.races) lines.push("Race: " + it.races);
    return lines;
  }

  // ── stats panel (estimates) ─────────────────────────────────────────────────
  // Race base stats from wiki.project1999.com/Character_Races.
  const RACE_BASE = {
    human: [75, 75, 75, 75, 75, 75, 75],
    erudite: [60, 70, 70, 70, 83, 107, 70],
    barbarian: [103, 95, 82, 70, 70, 60, 55],
    halfling: [70, 75, 95, 90, 80, 67, 50],
    dwarf: [90, 90, 70, 90, 83, 60, 45],
    gnome: [60, 70, 85, 85, 67, 98, 60],
    "half elf": [70, 70, 90, 85, 60, 75, 75],
    "wood elf": [65, 65, 95, 80, 80, 75, 75],
    "high elf": [55, 65, 85, 70, 95, 92, 80],
    "dark elf": [60, 65, 90, 75, 83, 99, 60],
    ogre: [130, 122, 70, 70, 67, 60, 37],
    troll: [108, 109, 83, 75, 60, 52, 40],
    iksar: [70, 70, 90, 85, 80, 75, 55],
  };
  // Classic class creation bonuses [STR,STA,AGI,DEX,WIS,INT,CHA] + point budget.
  const CLASS_BONUS = {
    warrior: { s: [10, 10, 5, 0, 0, 0, 0], pts: 25 },
    cleric: { s: [5, 5, 0, 0, 10, 0, 0], pts: 30 },
    paladin: { s: [10, 5, 0, 0, 5, 0, 10], pts: 20 },
    ranger: { s: [5, 10, 10, 0, 5, 0, 0], pts: 20 },
    "shadow knight": { s: [10, 5, 0, 0, 0, 10, 5], pts: 20 },
    druid: { s: [0, 10, 0, 0, 10, 0, 0], pts: 30 },
    monk: { s: [5, 5, 10, 10, 0, 0, 0], pts: 20 },
    bard: { s: [5, 0, 0, 10, 0, 0, 10], pts: 25 },
    rogue: { s: [0, 0, 10, 10, 0, 0, 0], pts: 30 },
    shaman: { s: [0, 5, 0, 0, 10, 0, 5], pts: 30 },
    necromancer: { s: [0, 0, 0, 10, 0, 10, 0], pts: 30 },
    wizard: { s: [0, 10, 0, 0, 0, 10, 0], pts: 30 },
    magician: { s: [0, 10, 0, 0, 0, 10, 0], pts: 30 },
    enchanter: { s: [0, 0, 0, 0, 0, 10, 10], pts: 30 },
  };
  // Racial resist bonuses [FIRE, COLD, DISEASE, POISON, MAGIC] (classic).
  const RACE_RESIST = {
    barbarian: [0, 10, 0, 0, 0],
    erudite: [0, 0, -5, 0, 5],
    halfling: [0, 0, 5, 5, 0],
    dwarf: [0, 0, 0, 5, 5],
  };
  const STAT_NAMES = ["STR", "STA", "AGI", "DEX", "WIS", "INT", "CHA"];
  const CASTER_WIS = ["cleric", "druid", "shaman", "paladin", "ranger"];
  const CASTER_INT = [
    "necromancer",
    "wizard",
    "magician",
    "enchanter",
    "shadow knight",
    "bard",
  ];

  $: cls = ((info && info.class) || "").toLowerCase();
  $: race = ((info && info.race) || "").toLowerCase();
  $: level = (info && info.level) || 0;
  $: base = RACE_BASE[race] || null;
  $: bonus = CLASS_BONUS[cls] || null;

  // Assigned creation points, persisted per character on this install.
  const ptsKey = () => `fuse.magelo.points.${charName.toLowerCase()}`;
  let assigned = [0, 0, 0, 0, 0, 0, 0];
  let showAssign = false;
  $: charName, loadAssigned();
  function loadAssigned() {
    try {
      const a = JSON.parse(localStorage.getItem(ptsKey()) || "[]");
      assigned = STAT_NAMES.map((_, i) => Math.max(0, a[i] | 0));
    } catch {
      assigned = [0, 0, 0, 0, 0, 0, 0];
    }
  }
  function saveAssigned() {
    localStorage.setItem(ptsKey(), JSON.stringify(assigned));
  }
  $: budget = bonus ? bonus.pts : 0;
  $: spent = assigned.reduce((a, b) => a + b, 0);
  function bump(i, d) {
    if (d > 0 && spent >= budget) return;
    assigned[i] = Math.max(0, assigned[i] + d);
    assigned = [...assigned];
    saveAssigned();
  }

  // Gear sums from worn items only.
  function gearSum(field) {
    let n = 0;
    for (const slot of WORN_ORDER) {
      const it = itemFor(worn[slot]);
      if (it) n += it[field] || 0;
    }
    return n;
  }
  $: gearStats = itemsBy && worn && {
    str: gearSum("str"),
    sta: gearSum("sta"),
    agi: gearSum("agi"),
    dex: gearSum("dex"),
    wis: gearSum("wis"),
    int: gearSum("int"),
    cha: gearSum("cha"),
    hp: gearSum("hp"),
    mana: gearSum("mana"),
    ac: gearSum("ac"),
    fire: gearSum("sv_fire"),
    cold: gearSum("sv_cold"),
    disease: gearSum("sv_disease"),
    poison: gearSum("sv_poison"),
    magic: gearSum("sv_magic"),
  };
  const GKEY = ["str", "sta", "agi", "dex", "wis", "int", "cha"];
  // Era hard cap: every attribute and resist tops out at 255. Overcap is
  // wasted — the capped value is what feeds HP/mana and every other derived
  // number, so the cap is applied at the totals level.
  const cap255 = (v) => Math.min(255, v);
  $: totals = STAT_NAMES.map((_, i) => {
    const b = base ? base[i] : 0;
    const c = bonus ? bonus.s[i] : 0;
    return cap255(b + c + assigned[i] + (gearStats ? gearStats[GKEY[i]] : 0));
  });

  // HP estimate: classic base-HP curve (EQEmu-style class level multiplier),
  // plus gear +HP. An estimate — buffs and per-level nuances aren't modeled.
  function hpMultiplier(c, l) {
    if (c === "warrior")
      return l < 20 ? 22 : l < 30 ? 23 : l < 40 ? 25 : l < 53 ? 27 : l < 57 ? 28 : 30;
    if (c === "paladin" || c === "shadow knight")
      return l < 35 ? 21 : l < 45 ? 22 : l < 51 ? 23 : l < 56 ? 24 : l < 60 ? 25 : 26;
    if (c === "ranger") return l < 58 ? 20 : 21;
    if (c === "monk" || c === "rogue" || c === "bard")
      return l < 51 ? 18 : l < 58 ? 19 : 20;
    if (c === "cleric" || c === "druid" || c === "shaman") return 15;
    return 12; // int casters
  }
  $: staTotal = totals[1] || 0;
  $: hpEst =
    level && cls
      ? Math.floor(
          5 +
            hpMultiplier(cls, level) * level +
            (staTotal * level * hpMultiplier(cls, level)) / 300,
        ) + (gearStats ? gearStats.hp : 0)
      : 0;

  // Mana estimate: the wiki's formula ((80|40 × level)/425 × stat), halved
  // above 200, plus gear +mana. Hybrids/pure casters only.
  $: manaStatIdx = CASTER_WIS.includes(cls) ? 4 : CASTER_INT.includes(cls) ? 5 : -1;
  $: manaEst = (() => {
    if (manaStatIdx < 0 || !level) return 0;
    const st = totals[manaStatIdx] || 0;
    const lo = Math.min(st, 200);
    const hi = Math.max(0, st - 200);
    return (
      Math.floor(((80 * level) / 425) * lo + ((40 * level) / 425) * hi) +
      (gearStats ? gearStats.mana : 0)
    );
  })();

  // Resists: racial bonus + gear. AC shown is the worn-gear AC sum.
  $: rr = RACE_RESIST[race] || [0, 0, 0, 0, 0];
  $: resists = gearStats
    ? [
        ["FIRE", cap255(rr[0] + gearStats.fire)],
        ["COLD", cap255(rr[1] + gearStats.cold)],
        ["DISEASE", cap255(rr[2] + gearStats.disease)],
        ["POISON", cap255(rr[3] + gearStats.poison)],
        ["MAGIC", cap255(rr[4] + gearStats.magic)],
      ]
    : [];

  // Weight: worn + general bags (bank excluded), honoring bag weight reduction.
  // itemsBy is referenced explicitly so the total recomputes when lookups land.
  $: weight = ((map) => {
    let w = 0;
    for (const slot of WORN_ORDER) {
      const it = itemFor(worn[slot], map);
      if (it) w += it.wt || 0;
    }
    for (const b of bags) {
      const bagIt = itemFor(b.bag, map);
      const wr = bagIt ? (bagIt.wr || 0) / 100 : 0;
      if (bagIt) w += bagIt.wt || 0;
      let inner = 0;
      for (const c of b.contents) {
        const it = itemFor(c, map);
        if (it) inner += (it.wt || 0) * (c.count || 1);
      }
      w += inner * (1 - wr);
    }
    return Math.round(w * 10) / 10;
  })(itemsBy);

  $: classGif =
    info && info.class ? `/classes/${info.class.replace(/ /g, "_")}.gif` : "";
</script>

<div class="mg">
  <!-- left: stats panel -->
  <div class="mg-stats">
    <div class="mg-name">{charName}</div>
    {#if info && info.class}
      <div class="mg-sub">{level} {info.class} ({info.race || "?"})</div>
    {/if}
    <div class="mg-est" title="Race base + class bonus + assigned creation points + worn gear. HP/mana use wiki formulas; creation points are yours to assign below.">Estimates</div>

    <div class="mg-core">
      <div><span>HP</span>{hpEst || "—"}</div>
      <div><span>MANA</span>{manaStatIdx >= 0 ? manaEst : "—"}</div>
      <div><span>AC (gear)</span>{gearStats ? gearStats.ac : 0}</div>
      <div><span>WT</span>{weight}</div>
    </div>

    <div class="mg-attrs">
      {#each STAT_NAMES as n, i}
        <div class="mg-attr">
          <span class="mg-attr-n">{n}</span>
          <span class="mg-attr-v">{base ? totals[i] : "—"}</span>
          {#if gearStats && gearStats[GKEY[i]]}
            <span class="mg-attr-g"
              >{gearStats[GKEY[i]] > 0 ? "+" : ""}{gearStats[GKEY[i]]}</span
            >
          {/if}
        </div>
      {/each}
    </div>

    <div class="mg-resists">
      {#each resists as [n, v]}
        <div class="mg-res"><span>{n}</span>{v}</div>
      {/each}
    </div>

    <button class="mg-btn" on:click={() => (showAssign = !showAssign)}
      >Assign Points ({budget - spent} left)</button
    >
    {#if showAssign}
      <div class="mg-assign">
        <div class="mg-assign-note">
          Your {budget} creation points — set them as you did at character
          creation.
        </div>
        {#each STAT_NAMES as n, i}
          <div class="mg-assign-row">
            <span class="mg-attr-n">{n}</span>
            <button class="mg-mini" on:click={() => bump(i, -1)}>−</button>
            <span class="mg-assign-v">{assigned[i]}</span>
            <button class="mg-mini" on:click={() => bump(i, 1)}>+</button>
          </div>
        {/each}
      </div>
    {/if}

    {#if lookupErr}<div class="mg-err">{lookupErr}</div>{/if}
    {#if missing.length}
      <div class="mg-missing">
        {missing.length} item{missing.length === 1 ? "" : "s"} not in the DB yet
        — queued for scraping; check back shortly.
      </div>
    {/if}
  </div>

  <!-- right: worn grid + bags + bank -->
  <div class="mg-right">
    {#if loading}
      <div class="mg-loading">Loading items…</div>
    {/if}
    <div class="mg-grid">
      {#each WORN_ORDER as slot}
        {@const inv = worn[slot]}
        {@const it = itemFor(inv, itemsBy)}
        <div
          class="mg-slot"
          class:filled={!!inv}
          style="grid-area: {slot.toLowerCase()}"
          on:mouseenter={(e) => inv && showTip(e, inv)}
          on:mousemove={(e) => inv && showTip(e, inv)}
          on:mouseleave={hideTip}
          role="img"
        >
          {#if inv && it && it.icon}
            <a href={it.link || wikiLink(inv.name)} target="_blank" rel="noreferrer">
              <img class="mg-icon" src={iconBase + it.icon} alt={inv.name} />
            </a>
          {:else if inv}
            <a
              class="mg-noicon"
              href={wikiLink(inv.name)}
              target="_blank"
              rel="noreferrer">{inv.name.slice(0, 12)}</a
            >
          {:else}
            <span class="mg-slot-label">{slot.replace(/[12]$/, "")}</span>
          {/if}
        </div>
      {/each}
      <div class="mg-class" style="grid-area: gif">
        {#if classGif}<img src={classGif} alt={info.class} />{/if}
      </div>
    </div>

    {#each [{ title: "Bags", list: bags }, { title: "Bank", list: bank }] as sec}
      {#if sec.list.length}
        <div class="mg-sec-title">{sec.title}</div>
        <div class="mg-bags">
          {#each sec.list as b}
            {@const slots = bagSlots(b, itemsBy)}
            <div class="mg-bag">
              <div
                class="mg-bag-head"
                on:mouseenter={(e) => b.bag && showTip(e, b.bag)}
                on:mouseleave={hideTip}
                role="note"
              >
                {b.bag ? b.bag.name : b.label}
              </div>
              {#if slots.length}
                <div class="mg-bag-grid">
                  {#each slots as c}
                    {#if c}
                      {@const it = itemFor(c, itemsBy)}
                      <div
                        class="mg-slot mini filled"
                        on:mouseenter={(e) => showTip(e, c)}
                        on:mousemove={(e) => showTip(e, c)}
                        on:mouseleave={hideTip}
                        role="img"
                      >
                        {#if it && it.icon}
                          <a href={it.link || wikiLink(c.name)} target="_blank" rel="noreferrer">
                            <img class="mg-icon" src={iconBase + it.icon} alt={c.name} />
                          </a>
                        {:else}
                          <a
                            class="mg-noicon"
                            href={wikiLink(c.name)}
                            target="_blank"
                            rel="noreferrer">{c.name.slice(0, 10)}</a
                          >
                        {/if}
                        {#if c.count > 1}<span class="mg-count">{c.count}</span>{/if}
                      </div>
                    {:else}
                      <div class="mg-slot mini"></div>
                    {/if}
                  {/each}
                </div>
              {:else if b.bag}
                <!-- A non-container item sitting directly in this slot. -->
                {@const it = itemFor(b.bag, itemsBy)}
                <div
                  class="mg-slot mini filled"
                  on:mouseenter={(e) => showTip(e, b.bag)}
                  on:mousemove={(e) => showTip(e, b.bag)}
                  on:mouseleave={hideTip}
                  role="img"
                >
                  {#if it && it.icon}
                    <a href={it.link || wikiLink(b.bag.name)} target="_blank" rel="noreferrer">
                      <img class="mg-icon" src={iconBase + it.icon} alt={b.bag.name} />
                    </a>
                  {:else}
                    <a
                      class="mg-noicon"
                      href={wikiLink(b.bag.name)}
                      target="_blank"
                      rel="noreferrer">{b.bag.name.slice(0, 10)}</a
                    >
                  {/if}
                  {#if b.bag.count > 1}<span class="mg-count">{b.bag.count}</span>{/if}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    {/each}

    <div class="mg-addrow">
      <button class="mg-btn" on:click={() => (showAdd = true)}
        >Add Item…</button
      >
    </div>
  </div>
</div>

{#if showAdd}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={addClose}>
    <div class="mg-dlg">
      <div class="mg-dlg-title">Add Item to DB</div>
      <div class="mg-dlg-row">
        <input
          class="mg-in mg-in-link"
          placeholder="Paste the item's wiki link…"
          bind:value={addLink}
          on:keydown={(e) => e.key === "Enter" && addPreview()}
        />
        <button class="mg-btn" disabled={addBusy || !addLink} on:click={addPreview}
          >{addBusy && !addItem ? "Scraping…" : "Scrape"}</button
        >
      </div>

      {#if addItem}
        <div class="mg-dlg-note">
          Review the scraped fields — correct anything that's wrong, then save.
        </div>
        <div class="mg-form">
          {#each ADD_TEXTS as [k, label]}
            <label class="mg-f mg-f-wide">
              <span>{label}</span>
              <input class="mg-in" bind:value={addItem[k]} />
            </label>
          {/each}
          {#each ADD_NUMS as [k, label]}
            <label class="mg-f">
              <span>{label}</span>
              <input class="mg-in" type="number" bind:value={addItem[k]} />
            </label>
          {/each}
          {#each ADD_FLAGS as [k, label]}
            <label class="mg-f mg-f-chk">
              <input type="checkbox" bind:checked={addItem[k]} />
              <span>{label}</span>
            </label>
          {/each}
        </div>
      {/if}

      {#if addErr}<div class="mg-err">{addErr}</div>{/if}
      {#if addMsg}<div class="mg-ok">{addMsg}</div>{/if}
      <div class="mg-dlg-btns">
        <button class="mg-btn" on:click={addClose}>Close</button>
        <button
          class="mg-btn mg-btn-go"
          disabled={!addItem || addBusy}
          on:click={addCommit}>{addBusy && addItem ? "Saving…" : "Add to DB"}</button
        >
      </div>
    </div>
  </div>
{/if}

{#if tip}
  <div class="mg-tip" style="left:{tip.x}px;top:{tip.y}px">
    <div class="mg-tip-name">{tip.inv.name}</div>
    {#if tip.item}
      {#each tipStats(tip.item) as l}<div class="mg-tip-line">{l}</div>{/each}
    {:else}
      <div class="mg-tip-line mg-tip-dim">
        Not in the item DB yet — queued for scraping.
      </div>
    {/if}
  </div>
{/if}

<style>
  .mg {
    display: flex;
    gap: 16px;
    padding: 12px;
    overflow: auto;
    flex: 1;
    align-items: flex-start;
  }

  /* ── stats panel ── */
  .mg-stats {
    flex: 0 0 190px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px;
    position: sticky;
    top: 0;
  }
  .mg-name {
    font-size: 15px;
    font-weight: 700;
    color: var(--accent);
  }
  .mg-sub {
    font-size: 12px;
    color: var(--text-secondary);
    margin-top: -6px;
  }
  .mg-est {
    font-size: 9px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-muted);
    cursor: help;
  }
  .mg-core div {
    display: flex;
    justify-content: space-between;
    font-size: 12.5px;
    color: var(--text-primary);
    font-weight: 600;
    padding: 1px 0;
  }
  .mg-core span {
    color: var(--text-muted);
    font-weight: 600;
    font-size: 10.5px;
    letter-spacing: 0.05em;
  }
  .mg-attrs {
    border-top: 1px solid var(--border);
    padding-top: 6px;
  }
  .mg-attr {
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: 12px;
    padding: 1px 0;
  }
  .mg-attr-n {
    width: 32px;
    color: var(--text-muted);
    font-size: 10.5px;
    font-weight: 700;
  }
  .mg-attr-v {
    color: var(--text-primary);
    font-weight: 600;
  }
  .mg-attr-g {
    color: var(--success);
    font-size: 10.5px;
    margin-left: auto;
  }
  .mg-resists {
    border-top: 1px solid var(--border);
    padding-top: 6px;
  }
  .mg-res {
    display: flex;
    justify-content: space-between;
    font-size: 11.5px;
    color: var(--text-primary);
    padding: 1px 0;
  }
  .mg-res span {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
  }
  .mg-btn {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 4px 8px;
  }
  .mg-btn:hover {
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .mg-assign-note {
    font-size: 10.5px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }
  .mg-assign-row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 1px 0;
  }
  .mg-assign-v {
    width: 20px;
    text-align: center;
    font-size: 12px;
    color: var(--text-primary);
  }
  .mg-mini {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 11px;
    width: 18px;
    height: 18px;
    line-height: 1;
  }
  .mg-err {
    font-size: 11.5px;
    color: #ff6b6b;
  }
  .mg-missing {
    font-size: 10.5px;
    color: #e3a008;
  }
  .mg-loading {
    font-size: 11px;
    color: var(--text-muted);
    padding: 4px 0;
  }

  /* ── worn grid ── */
  .mg-right {
    flex: 1;
    min-width: 0;
  }
  .mg-grid {
    display: grid;
    grid-template-columns: repeat(4, 46px);
    grid-auto-rows: 46px;
    gap: 4px;
    grid-template-areas:
      "ear1 head face ear2"
      "chest gif gif neck"
      "arms gif gif back"
      "waist gif gif shoulders"
      "wrist1 gif gif wrist2"
      "legs hands . feet"
      ". finger1 finger2 ."
      "primary secondary range ammo";
  }
  .mg-slot {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    position: relative;
  }
  .mg-slot.filled {
    border-color: var(--accent-dim);
  }
  .mg-slot.mini {
    width: 46px;
    height: 46px;
  }
  .mg-slot-label {
    font-size: 8px;
    letter-spacing: 0.04em;
    color: var(--text-muted);
    text-transform: uppercase;
  }
  .mg-icon {
    width: 40px;
    height: 40px;
    image-rendering: pixelated;
  }
  .mg-noicon {
    font-size: 8px;
    color: var(--text-secondary);
    text-align: center;
    text-decoration: none;
    padding: 2px;
    word-break: break-word;
  }
  .mg-count {
    position: absolute;
    right: 1px;
    bottom: 0;
    font-size: 9px;
    color: #fff;
    text-shadow: 0 1px 2px #000;
  }
  .mg-class {
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .mg-class img {
    max-width: 100%;
    max-height: 100%;
  }

  /* ── bags / bank ── */
  .mg-sec-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e3a008;
    margin: 14px 0 6px;
  }
  .mg-bags {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
  }
  .mg-bag {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 6px;
  }
  .mg-bag-head {
    font-size: 10.5px;
    color: var(--text-secondary);
    margin-bottom: 5px;
    max-width: 160px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .mg-bag-grid {
    display: grid;
    /* EQ bag layout: two columns, slots numbered 1-2 / 3-4 / … downward. */
    grid-template-columns: repeat(2, 46px);
    gap: 3px;
  }

  /* ── Add Item ── */
  .mg-addrow {
    position: sticky;
    bottom: 0;
    display: flex;
    justify-content: flex-end;
    padding: 10px 0 2px;
  }
  .mg-ov {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 300;
  }
  .mg-dlg {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 14px;
    width: 560px;
    max-width: 94vw;
    max-height: 86vh;
    overflow-y: auto;
    overflow-x: hidden;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .mg-dlg-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .mg-dlg-row {
    display: flex;
    gap: 8px;
  }
  .mg-in {
    background: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 4px 7px;
    font-size: 12px;
    min-width: 0;
    color-scheme: dark;
  }
  .mg-in-link {
    flex: 1;
  }
  .mg-dlg-note {
    font-size: 11px;
    color: var(--text-muted);
  }
  .mg-form {
    display: grid;
    /* minmax(0, …) lets the columns actually shrink — a bare 1fr floors at
       the input's intrinsic width (~30% wider), which forced a horizontal
       scrollbar on the dialog. */
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 6px 8px;
  }
  .mg-form .mg-in {
    width: 100%;
  }
  .mg-f {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 10px;
  }
  .mg-f span {
    color: var(--text-muted);
    font-weight: 700;
    letter-spacing: 0.04em;
  }
  .mg-f-wide {
    grid-column: span 2;
  }
  .mg-f-chk {
    flex-direction: row;
    align-items: center;
    gap: 5px;
    font-size: 11px;
  }
  .mg-f-chk span {
    font-weight: 600;
  }
  .mg-dlg-btns {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .mg-btn-go {
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .mg-btn-go:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .mg-ok {
    font-size: 11.5px;
    color: var(--success);
  }

  /* ── tooltip ── */
  .mg-tip {
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
  .mg-tip-name {
    font-size: 12.5px;
    font-weight: 700;
    color: var(--accent);
    margin-bottom: 4px;
  }
  .mg-tip-line {
    font-size: 11px;
    color: var(--text-primary);
    line-height: 1.5;
  }
  .mg-tip-dim {
    color: var(--text-muted);
    font-style: italic;
  }
</style>
