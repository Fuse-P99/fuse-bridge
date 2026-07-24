<script>
  // Magelo sub-tab (officers only): a wiki-Magelo-style character sheet built
  // from the local inventory outputfile. Worn slots cluster around the class
  // animation; bags and bank render below; the left panel estimates stats
  // (race base + class bonuses + user-assigned creation points + gear).
  // Item data comes from the server's eqitems table via LookupItems — unknown
  // items are queued there for a wiki scrape and fill in on a later visit.
  import { onDestroy } from "svelte";
  import {
    LookupItems,
    SaveMagelo,
    PreviewItem,
    CommitItem,
    ListMagelos,
    LoadMagelo,
    SaveMageloSlots,
    RenameMagelo,
    SearchItems,
    TopItems,
    ListFailedScrapes,
    StartMageloSeed,
    ListItemIcons,
    GetItemByName,
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "./scale.js";

  export let charName = "";
  export let inventory = []; // InventoryItem[] {location, name, count}
  export let info = null; // {level, class, race}
  export let isAdmin = false; // reveals the failed-scrapes / seed admin tools

  let itemsBy = {}; // lower(name) -> MageloItem
  let iconBase = "";
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
  // Custom magelos render from the editor's slot map; the default renders
  // from the live outputfile inventory.
  $: worn = curMagelo ? wornFromEdit(editSlots) : assignSlots(inventory);

  // Bags (General1-8) and bank (Bank1-8): the container item plus its
  // "-SlotN" contents, in file order. inventory is passed explicitly (not
  // read via closure) so Svelte re-runs these when the async prop arrives —
  // a bare containers("General") would never register the dependency and the
  // bag/bank sections would stay stale after a character switch.
  function containers(inv, prefix) {
    const out = [];
    for (let i = 1; i <= 8; i++) {
      const bagLoc = prefix + i;
      const bag = inv.find((x) => x.location === bagLoc);
      const contents = inv.filter((x) =>
        x.location.startsWith(bagLoc + "-Slot"),
      );
      if (bag || contents.length) out.push({ label: bagLoc, bag, contents });
    }
    return out;
  }
  $: bags = containers(inventory, "General");
  $: bank = containers(inventory, "Bank");

  // The 8 primary inventory slots (General1-8): each holds the bag itself or
  // a lone non-container item; null = empty slot. IIFE keeps inventory a
  // visible dependency so the grid refreshes when the async prop arrives.
  $: generalSlots = curMagelo
    ? Array.from({ length: 8 }, (_, i) =>
        editSlots["General" + (i + 1)]
          ? pseudoInv("General" + (i + 1), editSlots["General" + (i + 1)])
          : null,
      )
    : ((inv) => {
        const out = [];
        for (let i = 1; i <= 8; i++)
          out.push(inv.find((x) => x.location === "General" + i) || null);
        return out;
      })(inventory);

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
      // Merge, don't replace: editor magelos may have looked up items that
      // aren't in the outputfile inventory.
      itemsBy = { ...itemsBy, ...(res.items || {}) };
      iconBase = res.icon_url || "";
    } catch (e) {
      if (seq !== lookupSeq) return;
      lookupErr = String(e);
    }
    loading = false;
    // Persist this character's "current" magelo server-side (toon + per-slot
    // item references). Fire-and-forget — the view works regardless.
    if (names.length) SaveMagelo(charName).catch(() => {});
  }

  // ── magelo editor: additional saved gear sets (worn + General only) ────────
  // "Current" (curMagelo === "") is read-only, generated from the outputfile.
  let mageloList = [];
  let curMagelo = "";
  let editSlots = {}; // slotKey -> {name, count}
  let mgErr = "";
  let dirty = false;
  let saveTimer;

  let listedFor = null;
  $: if (charName && charName !== listedFor) {
    listedFor = charName;
    mageloList = [];
    curMagelo = "";
    editSlots = {};
    ListMagelos(charName)
      .then((l) => (mageloList = l))
      .catch(() => {});
  }

  function pseudoInv(loc, e) {
    return { location: loc, name: e.name, count: e.count || 1 };
  }
  function wornFromEdit(es) {
    const w = {};
    for (const s of WORN_ORDER) if (es[s]) w[s] = pseudoInv(s, es[s]);
    return w;
  }
  function slotsArray(es) {
    return Object.entries(es).map(([slot, e]) => ({
      slot,
      name: e.name,
      count: e.count || 1,
    }));
  }

  function saveNow() {
    clearTimeout(saveTimer);
    if (!curMagelo || !dirty) return;
    dirty = false;
    SaveMageloSlots(charName, curMagelo, slotsArray(editSlots)).catch(
      (e) => (mgErr = String(e)),
    );
  }
  function markDirty() {
    dirty = true;
    mgErr = "";
    clearTimeout(saveTimer);
    saveTimer = setTimeout(saveNow, 1200); // autosave shortly after the last edit
  }
  // Tabbing away (sub-tab switch, character switch) unmounts the view — flush.
  onDestroy(saveNow);

  // Fetch item records for names the current map doesn't have yet (editor
  // picks and loaded magelos may reference gear the character doesn't own).
  function ensureLookup(names) {
    const need = [...new Set(names.filter((n) => n && !itemsBy[n.toLowerCase()]))];
    if (!need.length) return;
    LookupItems(need)
      .then((res) => {
        itemsBy = { ...itemsBy, ...(res.items || {}) };
        if (res.icon_url) iconBase = res.icon_url;
      })
      .catch(() => {});
  }

  function selectMagelo(name) {
    saveNow(); // flush pending edits of the magelo we're leaving
    closeSlotEd();
    mgErr = "";
    curMagelo = name;
    editSlots = {};
    if (!name) return;
    LoadMagelo(charName, name)
      .then((slots) => {
        const es = {};
        for (const s of slots) es[s.slot] = { name: s.name, count: s.count || 1 };
        editSlots = es;
        ensureLookup(slots.map((s) => s.name));
      })
      .catch((e) => (mgErr = String(e)));
  }

  // Snapshot of the live view's worn + General slots, for "start from current".
  function snapshotCurrent() {
    const es = {};
    const w = assignSlots(inventory);
    for (const s of WORN_ORDER) if (w[s]) es[s] = { name: w[s].name, count: 1 };
    for (let i = 1; i <= 8; i++) {
      const g = inventory.find((x) => x.location === "General" + i);
      if (g) es["General" + i] = { name: g.name, count: g.count || 1 };
    }
    return es;
  }

  // New / Save As / Rename share one name prompt.
  let nameDlg = ""; // "" | "new" | "saveas" | "rename"
  let nameVal = "";
  let newFrom = "current";
  function validName(n) {
    n = n.trim();
    if (!n || n.length > 32) return "";
    if (n.toLowerCase() === "current") return "";
    if (nameDlg !== "rename" || n.toLowerCase() !== curMagelo.toLowerCase()) {
      if (mageloList.some((m) => m.toLowerCase() === n.toLowerCase())) return "";
    }
    return n;
  }
  function nameDlgGo() {
    const n = validName(nameVal);
    if (!n) {
      mgErr = 'Pick a unique name (not "current"), 32 characters max.';
      return;
    }
    mgErr = "";
    if (nameDlg === "rename") {
      const from = curMagelo;
      RenameMagelo(charName, from, n)
        .then(() => {
          mageloList = mageloList.map((m) => (m === from ? n : m)).sort();
          curMagelo = n;
        })
        .catch((e) => (mgErr = String(e)));
    } else {
      saveNow(); // "saveas" keeps the source magelo as last autosaved
      editSlots =
        nameDlg === "new"
          ? newFrom === "blank"
            ? {}
            : snapshotCurrent()
          : { ...editSlots };
      curMagelo = n;
      mageloList = [...mageloList, n].sort();
      dirty = true;
      saveNow();
      ensureLookup(Object.values(editSlots).map((e) => e.name));
    }
    nameDlg = "";
    nameVal = "";
  }

  // Slot editor: click a slot → search-as-you-type, filtered server-side to
  // the character's class/race and the slot's wiki SLOT keyword.
  const SLOT_FILTER = {
    Ear1: "EAR", Ear2: "EAR", Head: "HEAD", Face: "FACE", Neck: "NECK",
    Shoulders: "SHOULDERS", Arms: "ARMS", Back: "BACK", Wrist1: "WRIST",
    Wrist2: "WRIST", Range: "RANGE", Hands: "HANDS", Primary: "PRIMARY",
    Secondary: "SECONDARY", Finger1: "FINGER", Finger2: "FINGER",
    Chest: "CHEST", Legs: "LEGS", Waist: "WAIST", Feet: "FEET", Ammo: "AMMO",
  };
  let edSlot = ""; // slot key being edited ("" = closed)
  let edQ = "";
  let edSugs = [];
  let edTimer;
  let edTop = []; // top-100 stat table for the slot
  let edTopSort = ""; // "mana" (casters/priests) or "hp"
  function openSlotEd(key) {
    if (!curMagelo) return;
    edSlot = key;
    edQ = "";
    edSugs = [];
    edTop = [];
    edTopSort = "";
    TopItems(
      SLOT_FILTER[key] || "",
      (info && info.class) || "",
      (info && info.race) || "",
    )
      .then((r) => {
        edTop = r.items;
        edTopSort = r.sort;
      })
      .catch(() => {});
  }
  function closeSlotEd() {
    edSlot = "";
    clearTimeout(edTimer);
  }
  function edInput() {
    clearTimeout(edTimer);
    const q = edQ.trim();
    if (q.length < 2) {
      edSugs = [];
      return;
    }
    edTimer = setTimeout(() => {
      SearchItems(
        q,
        SLOT_FILTER[edSlot] || "",
        (info && info.class) || "",
        (info && info.race) || "",
      )
        .then((names) => (edSugs = names))
        .catch(() => (edSugs = []));
    }, 250);
  }
  function pickItem(name) {
    editSlots = { ...editSlots, [edSlot]: { name, count: 1 } };
    ensureLookup([name]);
    markDirty();
    closeSlotEd();
  }
  function clearSlot() {
    const es = { ...editSlots };
    delete es[edSlot];
    editSlots = es;
    markDirty();
    closeSlotEd();
  }
  function focusIt(node) {
    node.focus();
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
    ["link", "Link"],
  ];
  const ADD_FLAGS = [
    ["magic", "MAGIC"],
    ["lore", "LORE"],
    ["nodrop", "NO DROP"],
    ["norent", "NO RENT"],
  ];
  // Dedup: when the typed name matches an existing eqitems row (case and
  // apostrophe insensitive), load its fields and flip the action to update.
  // The server commit upserts by name either way — this makes it visible.
  let addExisting = false;
  let addChecked = ""; // last name checked, so repeated blurs don't clobber edits
  async function checkExisting(populate) {
    const nm = ((addItem && addItem.name) || "").trim();
    if (!nm) {
      addExisting = false;
      addChecked = "";
      return;
    }
    const key = nm.toLowerCase().replaceAll("`", "'");
    if (key === addChecked) return;
    addChecked = key;
    try {
      const res = await GetItemByName(nm);
      addExisting = !!res.found;
      if (res.found && populate) {
        addItem = { ...res.item };
        addMsg = `"${res.item.name}" is already in the DB — fields loaded; saving will update that row.`;
      }
    } catch {
      /* leave the flag as-is */
    }
  }

  async function addPreview() {
    addBusy = true;
    addErr = "";
    addMsg = "";
    addChecked = "";
    try {
      addItem = await PreviewItem(addLink);
      // Flag (but don't overwrite) when the scraped item already exists.
      checkExisting(false);
    } catch (e) {
      addErr = String(e) + " — you can still fill the fields in manually.";
    }
    addBusy = false;
  }
  // Manual entry: same editable form, blank record — for when the wiki page
  // is down (scrape failed) or the item has no page at all. Name/link are
  // prefilled from the pasted link when there is one.
  function addManual() {
    const it = {};
    for (const [k] of ADD_TEXTS) it[k] = "";
    for (const [k] of ADD_NUMS) it[k] = 0;
    for (const [k] of ADD_FLAGS) it[k] = false;
    const link = addLink.trim();
    if (link) {
      it.link = link;
      let name = link.includes("/") ? link.slice(link.lastIndexOf("/") + 1) : link;
      try {
        name = decodeURIComponent(name);
      } catch {
        /* keep as-is */
      }
      it.name = name.replace(/_/g, " ").trim();
    }
    addItem = it;
    addErr = "";
    addMsg = "";
    addChecked = "";
    addExisting = false;
    if (it.name) checkExisting(true);
  }
  async function addCommit() {
    if (!addItem) return;
    addBusy = true;
    addErr = "";
    try {
      await CommitItem(addItem);
      addMsg = `"${addItem.name}" saved to the item DB.`;
      addExisting = true; // it exists now — further saves update
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
    addExisting = false;
    addChecked = "";
  }

  // ── admin: failed-scrape ledger + one-time Magelo_Blue seed ────────────────
  let showFails = false;
  let failRows = [];
  let failErr = "";
  let admMsg = "";
  function openFails() {
    showFails = true;
    failErr = "";
    ListFailedScrapes()
      .then((r) => (failRows = r))
      .catch((e) => (failErr = String(e)));
  }
  // Jump a failed name straight into the Add Item dialog (link prefilled —
  // Scrape often works one-off, and Manual is right there when it doesn't).
  function failToAdd(name) {
    showFails = false;
    showAdd = true;
    addLink = wikiLink(name);
    addItem = null;
    addErr = "";
    addMsg = "";
  }
  // Icon browser for the Add Item dialog: pick from the server's icon cache
  // instead of typing a filename.
  let showIconPick = false;
  let iconList = [];
  let iconPickBase = "";
  let iconFilter = "";
  function openIconPick() {
    showIconPick = true;
    iconFilter = "";
    if (!iconList.length) {
      ListItemIcons()
        .then((r) => {
          iconList = r.icons;
          iconPickBase = r.icon_url;
        })
        .catch((e) => (addErr = String(e)));
    }
  }
  $: iconShown = iconFilter.trim()
    ? iconList.filter((n) => n.includes(iconFilter.trim()))
    : iconList;
  function pickIcon(n) {
    addItem = { ...addItem, icon: n };
    showIconPick = false;
  }

  function startSeed() {
    admMsg = "";
    StartMageloSeed()
      .then(
        (started) =>
          (admMsg = started
            ? "Seed started — progress is in the server log."
            : "A seed run is already in progress."),
      )
      .catch((e) => (admMsg = String(e)));
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
    if (curMagelo) {
      // Editor magelos carry no bag contents — worn plus the General items.
      for (const g of generalSlots) {
        const it = itemFor(g, map);
        if (it) w += (it.wt || 0) * ((g && g.count) || 1);
      }
      return Math.round(w * 10) / 10;
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
  <!-- magelo picker: the read-only outputfile default plus saved alternates -->
  <div class="mg-tabs">
    <button
      class="mg-tab"
      class:active={!curMagelo}
      on:click={() => selectMagelo("")}>Current</button
    >
    {#each mageloList as m}
      <button
        class="mg-tab"
        class:active={curMagelo === m}
        on:click={() => selectMagelo(m)}>{m}</button
      >
    {/each}
    <button
      class="mg-tab mg-tab-new"
      on:click={() => {
        nameDlg = "new";
        nameVal = "";
        newFrom = "current";
        mgErr = "";
      }}>+ New</button
    >
    {#if curMagelo}
      <span class="mg-tab-note">click a slot to edit — autosaves</span>
      <span class="mg-tab-sp"></span>
      <button
        class="mg-btn"
        on:click={() => {
          nameDlg = "rename";
          nameVal = curMagelo;
          mgErr = "";
        }}>Rename</button
      >
      <button
        class="mg-btn"
        on:click={() => {
          nameDlg = "saveas";
          nameVal = "";
          mgErr = "";
        }}>Save As…</button
      >
    {/if}
    {#if mgErr && !nameDlg}<span class="mg-err">{mgErr}</span>{/if}
  </div>

  <div class="mg-row">
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
  </div>

  <!-- right: worn grid with the 8 General slots beside it -->
  <div class="mg-right">
    {#if loading}
      <div class="mg-loading">Loading items…</div>
    {/if}
    <div class="mg-toprow">
    <div class="mg-grid">
      {#each WORN_ORDER as slot}
        {@const inv = worn[slot]}
        {@const it = itemFor(inv, itemsBy)}
        <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
        <div
          class="mg-slot"
          class:filled={!!inv}
          class:editable={!!curMagelo}
          style="grid-area: {slot.toLowerCase()}"
          on:click={() => curMagelo && openSlotEd(slot)}
          on:mouseenter={(e) => inv && showTip(e, inv)}
          on:mousemove={(e) => inv && showTip(e, inv)}
          on:mouseleave={hideTip}
          role="img"
        >
          {#if inv && it && it.icon}
            {#if curMagelo}
              <img class="mg-icon" src={iconBase + it.icon} alt={inv.name} />
            {:else}
              <a href={it.link || wikiLink(inv.name)} target="_blank" rel="noreferrer">
                <img class="mg-icon" src={iconBase + it.icon} alt={inv.name} />
              </a>
            {/if}
          {:else if inv}
            {#if curMagelo}
              <span class="mg-noicon">{inv.name.slice(0, 12)}</span>
            {:else}
              <a
                class="mg-noicon"
                href={wikiLink(inv.name)}
                target="_blank"
                rel="noreferrer">{inv.name.slice(0, 12)}</a
              >
            {/if}
          {:else}
            <span class="mg-slot-label">{slot.replace(/[12]$/, "")}</span>
          {/if}
        </div>
      {/each}
      <div class="mg-class" style="grid-area: gif">
        {#if classGif}<img src={classGif} alt={info.class} />{/if}
      </div>
    </div>

    <!-- General1-8: the bag (or lone item) sitting in each primary slot. -->
    <div class="mg-general-col">
      <div class="mg-sec-title mg-gen-title">General</div>
      <div class="mg-general">
        {#each generalSlots as g, gi}
          {@const it = itemFor(g, itemsBy)}
          <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
          <div
            class="mg-slot"
            class:filled={!!g}
            class:editable={!!curMagelo}
            on:click={() => curMagelo && openSlotEd("General" + (gi + 1))}
            on:mouseenter={(e) => g && showTip(e, g)}
            on:mousemove={(e) => g && showTip(e, g)}
            on:mouseleave={hideTip}
            role="img"
          >
            {#if g && it && it.icon}
              {#if curMagelo}
                <img class="mg-icon" src={iconBase + it.icon} alt={g.name} />
              {:else}
                <a href={it.link || wikiLink(g.name)} target="_blank" rel="noreferrer">
                  <img class="mg-icon" src={iconBase + it.icon} alt={g.name} />
                </a>
              {/if}
            {:else if g}
              {#if curMagelo}
                <span class="mg-noicon">{g.name.slice(0, 12)}</span>
              {:else}
                <a
                  class="mg-noicon"
                  href={wikiLink(g.name)}
                  target="_blank"
                  rel="noreferrer">{g.name.slice(0, 12)}</a
                >
              {/if}
            {:else}
              <span class="mg-slot-label">{gi + 1}</span>
            {/if}
            {#if g && g.count > 1}<span class="mg-count">{g.count}</span>{/if}
          </div>
        {/each}
      </div>
    </div>
    </div>
  </div>
  </div>

  <!-- Bags/Bank belong to the live outputfile view only; editor magelos are
       worn armor + the 8 General slots. -->
  {#each curMagelo ? [] : [{ title: "Bags", list: bags }, { title: "Bank", list: bank }] as sec}
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
    {#if admMsg}<span class="mg-adm-msg">{admMsg}</span>{/if}
    {#if isAdmin}
      <button class="mg-btn" on:click={startSeed}>Seed Item DB</button>
      <button class="mg-btn" on:click={openFails}>Failed Scrapes…</button>
    {/if}
    <button class="mg-btn" on:click={() => (showAdd = true)}
      >Add Item…</button
    >
  </div>
</div>

{#if showIconPick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={() => (showIconPick = false)}>
    <div class="mg-dlg">
      <div class="mg-dlg-title">Choose an Icon ({iconShown.length})</div>
      <input
        class="mg-in"
        placeholder="Filter by number…"
        bind:value={iconFilter}
        use:focusIt
      />
      <div class="mg-icon-grid">
        {#each iconShown as n (n)}
          <button class="mg-icon-cell" title={n} on:click={() => pickIcon(n)}>
            <img src={iconPickBase + n} alt={n} loading="lazy" />
          </button>
        {:else}
          <div class="mg-dlg-note">
            {iconList.length
              ? "No icons match that filter."
              : "No icons cached yet — they arrive as items are scraped."}
          </div>
        {/each}
      </div>
      <div class="mg-dlg-btns">
        <button class="mg-btn" on:click={() => (showIconPick = false)}
          >Close</button
        >
      </div>
    </div>
  </div>
{/if}

{#if showFails}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={() => (showFails = false)}>
    <div class="mg-dlg">
      <div class="mg-dlg-title">Failed Scrapes (3+ attempts)</div>
      <div class="mg-dlg-note">
        The auto-scraper gave up on these — add them manually. A successful
        manual add (or a later successful scrape) clears the row.
      </div>
      {#if failErr}<div class="mg-err">{failErr}</div>{/if}
      {#if failRows.length}
        <table class="mg-fails">
          <thead>
            <tr><th>Item</th><th>Fails</th><th>Last error</th><th></th></tr>
          </thead>
          <tbody>
            {#each failRows as f}
              <tr>
                <td class="mg-fail-name">{f.name}</td>
                <td class="mg-fail-n">{f.fails}</td>
                <td class="mg-fail-err" title={f.error}>{f.error}</td>
                <td>
                  <button class="mg-btn" on:click={() => failToAdd(f.name)}
                    >Add…</button
                  >
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else if !failErr}
        <div class="mg-dlg-note">Nothing here — every scrape is landing.</div>
      {/if}
      <div class="mg-dlg-btns">
        <button class="mg-btn" on:click={() => (showFails = false)}>Close</button>
      </div>
    </div>
  </div>
{/if}

{#if edSlot}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={closeSlotEd}>
    <div class="mg-dlg">
      <div class="mg-dlg-title">{edSlot} — {curMagelo}</div>
      {#if editSlots[edSlot]}
        <div class="mg-dlg-note">Now: {editSlots[edSlot].name}</div>
      {/if}
      <input
        class="mg-in"
        placeholder="Type to search items…"
        bind:value={edQ}
        on:input={edInput}
        use:focusIt
      />
      <div class="mg-sugs">
        {#each edSugs as s}
          <button class="mg-sug" on:click={() => pickItem(s)}>{s}</button>
        {:else}
          {#if edQ.trim().length >= 2}
            <div class="mg-dlg-note">No matches in the item DB.</div>
          {/if}
        {/each}
      </div>
      {#if edTop.length}
        <div class="mg-dlg-note">
          Top {edTop.length} for this slot, ranked by {edTopSort === "mana"
            ? "mana"
            : "HP"} — click to equip.
        </div>
        <div class="mg-top-wrap">
          <table class="mg-fails mg-top">
            <thead>
              <tr>
                <th>Item</th><th>HP</th><th>Mana</th><th>STA</th>
                <th>INT</th><th>WIS</th><th>SvM</th>
              </tr>
            </thead>
            <tbody>
              {#each edTop as t}
                <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
                <tr class="mg-top-row" on:click={() => pickItem(t.name)}>
                  <td class="mg-fail-name">{t.name}</td>
                  <td>{t.hp || ""}</td>
                  <td>{t.mana || ""}</td>
                  <td>{t.sta || ""}</td>
                  <td>{t.int || ""}</td>
                  <td>{t.wis || ""}</td>
                  <td>{t.sv_magic || ""}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
      <div class="mg-dlg-btns">
        {#if editSlots[edSlot]}
          <button class="mg-btn" on:click={clearSlot}>Clear Slot</button>
        {/if}
        <button class="mg-btn" on:click={closeSlotEd}>Close</button>
      </div>
    </div>
  </div>
{/if}

{#if nameDlg}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={() => (nameDlg = "")}>
    <div class="mg-dlg mg-dlg-sm">
      <div class="mg-dlg-title">
        {nameDlg === "new"
          ? "New Magelo"
          : nameDlg === "saveas"
            ? "Save As"
            : "Rename Magelo"}
      </div>
      <input
        class="mg-in"
        placeholder="Name…"
        bind:value={nameVal}
        use:focusIt
        on:keydown={(e) => e.key === "Enter" && nameDlgGo()}
      />
      {#if nameDlg === "new"}
        <label class="mg-f-chk">
          <input type="radio" bind:group={newFrom} value="current" />
          <span>Start from current gear</span>
        </label>
        <label class="mg-f-chk">
          <input type="radio" bind:group={newFrom} value="blank" />
          <span>Start blank</span>
        </label>
      {/if}
      {#if mgErr}<div class="mg-err">{mgErr}</div>{/if}
      <div class="mg-dlg-btns">
        <button class="mg-btn" on:click={() => (nameDlg = "")}>Cancel</button>
        <button class="mg-btn mg-btn-go" on:click={nameDlgGo}>OK</button>
      </div>
    </div>
  </div>
{/if}

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
        <button class="mg-btn" disabled={addBusy} on:click={addManual}
          >Manual</button
        >
      </div>

      {#if addItem}
        <div class="mg-dlg-note">
          Review the fields — correct anything that's wrong, then save. Name
          is required; use the exact in-game spelling so inventory lookups
          match.
        </div>
        <div class="mg-form">
          {#each ADD_TEXTS as [k, label]}
            <label class="mg-f mg-f-wide">
              <span>{label}</span>
              <input
                class="mg-in"
                bind:value={addItem[k]}
                on:blur={() => k === "name" && checkExisting(true)}
              />
            </label>
          {/each}
          <div class="mg-f mg-f-wide">
            <span>Icon</span>
            <div class="mg-icon-pickrow">
              {#if addItem.icon}
                <img
                  class="mg-icon-prev"
                  src={(iconPickBase || iconBase) + addItem.icon}
                  alt={addItem.icon}
                />
              {/if}
              <input
                class="mg-in mg-icon-in"
                bind:value={addItem.icon}
                placeholder="Item_NNN.png"
              />
              <button class="mg-btn" on:click={openIconPick}>Browse…</button>
            </div>
          </div>
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
          on:click={addCommit}
          >{addBusy && addItem
            ? "Saving…"
            : addExisting
              ? "Update in DB"
              : "Add to DB"}</button
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
  /* Column layout: the top row (stats | worn | general) with Bags and Bank
     always full-width below it. */
  .mg {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 12px;
    overflow: auto;
    flex: 1;
  }
  .mg-row {
    display: flex;
    gap: 16px;
    align-items: flex-start;
  }
  /* ── magelo picker bar ── */
  .mg-tabs {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
  .mg-tab {
    background: var(--bg-input);
    color: var(--text-secondary);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 3px 12px;
    font-size: 12px;
    cursor: pointer;
  }
  .mg-tab.active {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .mg-tab-note {
    font-size: 11px;
    color: var(--text-muted);
  }
  .mg-tab-sp {
    flex: 1;
  }
  /* Editable slots (custom magelo selected) invite the click. */
  .mg-slot.editable {
    cursor: pointer;
  }
  .mg-slot.editable:hover {
    border-color: var(--accent-dim);
  }
  .mg-dlg-sm {
    width: 380px;
  }
  .mg-sugs {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 260px;
    overflow-y: auto;
  }
  .mg-sug {
    text-align: left;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 4px 8px;
    cursor: pointer;
  }
  .mg-sug:hover {
    border-color: var(--accent-dim);
  }

  /* Worn grid and the General slots side by side. */
  .mg-toprow {
    display: flex;
    gap: 16px;
    align-items: flex-start;
  }
  /* Double class: .mg-sec-title is defined later in the file and would
     otherwise win the margin at equal specificity. */
  .mg-sec-title.mg-gen-title {
    margin: 0 0 6px;
  }
  /* The 8 primary slots, EQ-style: two columns, 1-2 / 3-4 / … downward. */
  .mg-general {
    display: grid;
    grid-template-columns: repeat(2, 46px);
    grid-auto-rows: 46px;
    gap: 4px;
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
    align-items: center;
    gap: 8px;
    padding: 10px 0 2px;
  }
  .mg-adm-msg {
    font-size: 11px;
    color: var(--text-muted);
  }

  /* ── failed scrapes table ── */
  .mg-fails {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .mg-fails th {
    text-align: left;
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    padding: 2px 8px 4px 0;
  }
  .mg-fails td {
    padding: 3px 8px 3px 0;
    border-top: 1px solid var(--border);
    vertical-align: middle;
  }
  .mg-fail-name {
    color: var(--text-primary);
    font-weight: 600;
    word-break: break-word;
  }
  .mg-fail-n {
    color: #e3a008;
    font-variant-numeric: tabular-nums;
  }
  .mg-fail-err {
    color: var(--text-muted);
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* ── Add Item icon browser ── */
  .mg-icon-pickrow {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .mg-icon-in {
    flex: 1;
    min-width: 0;
  }
  .mg-icon-prev {
    width: 28px;
    height: 28px;
    flex: none;
  }
  .mg-icon-grid {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    max-height: 380px;
    overflow-y: auto;
  }
  .mg-icon-cell {
    width: 46px;
    height: 46px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    padding: 0;
  }
  .mg-icon-cell:hover {
    border-color: var(--accent-dim);
  }
  .mg-icon-cell img {
    max-width: 40px;
    max-height: 40px;
  }

  /* ── slot editor top-100 table ── */
  .mg-top-wrap {
    max-height: 300px;
    overflow-y: auto;
  }
  .mg-top td {
    color: var(--text-secondary);
    font-variant-numeric: tabular-nums;
  }
  .mg-top-row {
    cursor: pointer;
  }
  .mg-top-row:hover td {
    background: rgba(255, 255, 255, 0.04);
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
