<script>
  // Magelo sub-tab (all users; item data needs a linked client): a
  // wiki-Magelo-style character sheet built from the local inventory
  // outputfile. Worn slots cluster around the class
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
    DeleteMagelo,
    SearchItems,
    TopItems,
    ListFailedScrapes,
    ListItemIcons,
    GetItemByName,
    ListBuffs,
    SaveBuff,
    DeleteBuff,
    PreviewBuff,
    NoteLevitate,
    ShareMageloToLibrary,
    ListLibrary,
    GetLibraryMagelo,
    VoteLibrary,
    DeleteLibraryEntry,
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "./scale.js";
  import { classAbbr } from "./classAbbr.js";
  import {
    STAT_NAMES,
    GKEY,
    RESIST_GEAR_KEY,
    RACE_BASE,
    CLASS_BONUS,
    characterStats,
    hasteCap,
  } from "./eqstats.js";

  export let charName = "";
  export let inventory = []; // InventoryItem[] {location, name, count}
  export let info = null; // {level, class, race}
  export let isAdmin = false; // reveals the failed-scrapes / seed admin tools
  export let allChars = []; // [{name, class}] — the user's characters, for library copies
  // Bumped by CharactersTab's "Fuse Shared Magelos" button (which lives on
  // its filter row); each bump opens the library dialog here.
  export let libraryOpenReq = 0;
  $: if (libraryOpenReq) openLib();

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
    const need = [
      ...new Set(names.filter((n) => n && !itemsBy[n.toLowerCase()])),
    ];
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
        for (const s of slots)
          es[s.slot] = { name: s.name, count: s.count || 1 };
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

  // New / Rename share one name prompt.
  let nameDlg = ""; // "" | "new" | "rename"
  let nameVal = "";
  let newFrom = "current";
  function validName(n) {
    n = n.trim();
    if (!n || n.length > 32) return "";
    if (n.toLowerCase() === "current") return "";
    if (nameDlg !== "rename" || n.toLowerCase() !== curMagelo.toLowerCase()) {
      if (mageloList.some((m) => m.toLowerCase() === n.toLowerCase()))
        return "";
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
      saveNow(); // flush the magelo we're leaving before starting the new one
      editSlots = newFrom === "blank" ? {} : snapshotCurrent();
      curMagelo = n;
      mageloList = [...mageloList, n].sort();
      dirty = true;
      saveNow();
      ensureLookup(Object.values(editSlots).map((e) => e.name));
    }
    nameDlg = "";
    nameVal = "";
  }

  // Delete the selected magelo — armed two-click so a slip can't nuke a
  // build (the first click flips the button to a confirm for a few seconds).
  let delArm = false;
  let delArmTimer;
  function deleteMagelo() {
    if (!curMagelo) return;
    if (!delArm) {
      delArm = true;
      clearTimeout(delArmTimer);
      delArmTimer = setTimeout(() => (delArm = false), 3500);
      return;
    }
    clearTimeout(delArmTimer);
    delArm = false;
    const name = curMagelo;
    clearTimeout(saveTimer);
    dirty = false; // never autosave a magelo we're deleting
    DeleteMagelo(charName, name)
      .then(() => {
        mageloList = mageloList.filter((m) => m !== name);
        selectMagelo("");
      })
      .catch((e) => (mgErr = String(e)));
  }

  // Slot editor: click a slot → search-as-you-type, filtered server-side to
  // the character's class/race and the slot's wiki SLOT keyword.
  const SLOT_FILTER = {
    Ear1: "EAR",
    Ear2: "EAR",
    Head: "HEAD",
    Face: "FACE",
    Neck: "NECK",
    Shoulders: "SHOULDERS",
    Arms: "ARMS",
    Back: "BACK",
    Wrist1: "WRIST",
    Wrist2: "WRIST",
    Range: "RANGE",
    Hands: "HANDS",
    Primary: "PRIMARY",
    Secondary: "SECONDARY",
    Finger1: "FINGER",
    Finger2: "FINGER",
    Chest: "CHEST",
    Legs: "LEGS",
    Waist: "WAIST",
    Feet: "FEET",
    Ammo: "AMMO",
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
      let name = link.includes("/")
        ? link.slice(link.lastIndexOf("/") + 1)
        : link;
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
    // DKP purchase overview (server fills this for linked lookups only):
    // "DKP: 12 sales · med 150 · last 175 (Jun 2026) ↑"
    if (it.dkp_count) {
      const tr = it.dkp_trend > 0 ? " ↑" : it.dkp_trend < 0 ? " ↓" : "";
      lines.push(
        `DKP: ${it.dkp_count} sale${it.dkp_count === 1 ? "" : "s"} · med ${it.dkp_median} · last ${it.dkp_last}` +
          (it.dkp_last_at ? ` (${it.dkp_last_at})` : "") +
          tr,
      );
    }
    return lines;
  }

  // ── buffs ────────────────────────────────────────────────────────────────
  // Catalog from the server (admin-editable there); the character's active
  // set persists locally per character.
  let allBuffs = [];
  let buffErr = "";
  ListBuffs()
    .then((b) => (allBuffs = b))
    .catch(() => {});

  const buffKey = () => `fuse.magelo.buffs.${charName.toLowerCase()}`;
  let activeBuffNames = [];
  $: charName, loadBuffSel();
  function loadBuffSel() {
    try {
      const a = JSON.parse(localStorage.getItem(buffKey()) || "[]");
      activeBuffNames = Array.isArray(a)
        ? a.filter((x) => typeof x === "string")
        : [];
    } catch {
      activeBuffNames = [];
    }
  }
  function saveBuffSel() {
    try {
      localStorage.setItem(buffKey(), JSON.stringify(activeBuffNames));
    } catch {
      /* storage full/blocked — selection just won't persist */
    }
  }
  $: activeBuffs = activeBuffNames
    .map((n) => allBuffs.find((b) => b.name === n))
    .filter(Boolean);

  let showBuffPick = false;
  // Conflicts are symmetric: either side listing the other blocks the add.
  function conflictWith(b) {
    const names = (s) =>
      (s || "")
        .toLowerCase()
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean);
    for (const a of activeBuffs) {
      if (
        names(a.conflicts).includes(b.name.toLowerCase()) ||
        names(b.conflicts).includes(a.name.toLowerCase())
      )
        return a.name;
    }
    return "";
  }
  // Already-active buffs are filtered out of the picker; the guard here
  // backstops that (double-click, case/apostrophe drift) — never twice.
  $: activeBuffKeys = new Set(activeBuffNames.map(beNameKey));
  function addBuff(b) {
    if (activeBuffKeys.has(beNameKey(b.name))) {
      showBuffPick = false;
      return;
    }
    const c = conflictWith(b);
    if (c) {
      buffErr = `${b.name} doesn't stack with ${c}.`;
      return;
    }
    buffErr = "";
    activeBuffNames = [...activeBuffNames, b.name];
    saveBuffSel();
    showBuffPick = false;
    // The one buff whose use is reported: a levitation buff notifies the guild
    // admin (see NoteLevitate). Every other buff stays local to this machine.
    if (/levitat/i.test(b.name)) NoteLevitate(charName, b.name);
  }
  function removeBuff(n) {
    activeBuffNames = activeBuffNames.filter((x) => x !== n);
    saveBuffSel();
  }

  // Buff totals. Everything sums except haste: only the strongest spell
  // haste applies (worn-item haste stacks with it separately).
  $: buffStats = ((list) => {
    const t = {
      hp: 0,
      mana: 0,
      ac: 0,
      atk: 0,
      haste: 0,
      ds: 0,
      hpregen: 0,
      str: 0,
      sta: 0,
      agi: 0,
      dex: 0,
      wis: 0,
      int: 0,
      cha: 0,
      svf: 0,
      svc: 0,
      svd: 0,
      svp: 0,
      svm: 0,
    };
    for (const b of list) {
      for (const [k, v] of Object.entries(b.mods || {})) {
        if (k === "haste") t.haste = Math.max(t.haste, v);
        else if (k in t) t[k] += v;
      }
    }
    return t;
  })(activeBuffs);

  // Levitate flavor: with a levitation buff active the stats panel, worn grid, and General column bob gently.
  $: floating = activeBuffs.some((b) => /levitat/i.test(b.name));

  const MOD_LABELS = {
    hp: "HP",
    mana: "MANA",
    ac: "AC",
    atk: "ATK",
    haste: "Haste",
    ds: "DS",
    hpregen: "HPrgn",
    str: "STR",
    sta: "STA",
    agi: "AGI",
    dex: "DEX",
    wis: "WIS",
    int: "INT",
    cha: "CHA",
    svf: "SvF",
    svc: "SvC",
    svd: "SvD",
    svp: "SvP",
    svm: "SvM",
  };
  function modText(mods) {
    return Object.entries(mods || {})
      .filter(([, v]) => v)
      .map(
        ([k, v]) =>
          `${MOD_LABELS[k] || k.toUpperCase()} +${v}${k === "haste" ? "%" : ""}`,
      )
      .join(" · ");
  }

  // ── buff admin (Add Buffs…): link → scrape preview → correct → save ───────
  // Mirrors the Add Item flow: paste a wiki link and Scrape, or Manual for a
  // blank form whose Name field live-fills from the wiki on tab-out.
  let showBuffAdmin = false;
  let buffLink = ""; // stage-1 wiki URL input
  let beSel = ""; // catalog buff loaded in the editor ("" = new)
  let be = null; // editable copy {name, icon, conflicts, note, mods}
  let beExisting = false; // name matches a catalog row → Save updates it
  let beChecked = ""; // last name live-looked-up, so repeat blurs are free
  let buffAdmMsg = "";
  const beNameKey = (s) => (s || "").trim().toLowerCase().replaceAll("`", "'");
  function openBuffAdmin() {
    showBuffAdmin = true;
    buffLink = "";
    beSel = "";
    be = null;
    beExisting = false;
    beChecked = "";
    buffAdmMsg = "";
  }
  const BUFF_MOD_KEYS = [
    ["hp", "HP"],
    ["mana", "MANA"],
    ["ac", "AC"],
    ["atk", "ATK"],
    ["haste", "Haste %"],
    ["ds", "Dmg Shield"],
    ["hpregen", "HP Regen"],
    ["str", "STR"],
    ["sta", "STA"],
    ["agi", "AGI"],
    ["dex", "DEX"],
    ["wis", "WIS"],
    ["int", "INT"],
    ["cha", "CHA"],
    ["svf", "Sv Fire"],
    ["svc", "Sv Cold"],
    ["svd", "Sv Disease"],
    ["svp", "Sv Poison"],
    ["svm", "Sv Magic"],
  ];
  function buffEdLoad(name) {
    beSel = name;
    buffAdmMsg = "";
    const src = allBuffs.find((b) => b.name === name);
    const mods = {};
    for (const [k] of BUFF_MOD_KEYS)
      mods[k] = (src && src.mods && src.mods[k]) || 0;
    be = src
      ? {
          name: src.name,
          icon: src.icon,
          conflicts: src.conflicts,
          note: src.note,
          mods,
        }
      : { name: "", icon: "", conflicts: "", note: "", mods };
    beExisting = !!src;
    beChecked = src ? beNameKey(src.name) : "";
  }
  // Merge a scraped preview into the form. Scraped mods only replace the
  // grid when the parse actually found some.
  function buffFill(p, base) {
    const found = Object.keys(p.mods || {}).length > 0;
    const mods = {};
    for (const [k] of BUFF_MOD_KEYS)
      mods[k] = found
        ? (p.mods && p.mods[k]) || 0
        : (base && base.mods[k]) || 0;
    be = {
      name: p.name || (base && base.name) || "",
      icon: p.icon || (base && base.icon) || "",
      conflicts: p.conflicts || (base && base.conflicts) || "",
      note: p.note || (base && base.note) || "",
      mods,
    };
    buffAdmMsg = found
      ? "Scraped — review the fields and stacking, then save."
      : "Page found but no stat effects parsed — fill the fields manually.";
  }
  let beBusy = false;
  // Stage 1 "Scrape": fetch the pasted wiki link server-side; the spell name
  // comes from the URL's last segment (underscores → spaces).
  function buffUrlScrape() {
    const link = buffLink.trim();
    if (!link || beBusy) return;
    beBusy = true;
    buffAdmMsg = "";
    PreviewBuff("", link)
      .then((p) => {
        buffFill(p, null);
        beSel = "";
        beChecked = beNameKey(be.name);
        beExisting = allBuffs.some((b) => beNameKey(b.name) === beChecked);
        if (beExisting)
          buffAdmMsg = `"${be.name}" is already in the catalog — saving will update it.`;
        beBusy = false;
      })
      .catch((e) => {
        buffAdmMsg = String(e) + " — use Manual to fill the fields yourself.";
        beBusy = false;
      });
  }
  // Stage 1 "Manual": blank form. Name prefills from the pasted link when
  // there is one, and the Name field live-fills from the wiki on tab-out.
  function buffManual() {
    buffEdLoad("");
    const link = buffLink.trim();
    if (link) {
      let name = link.includes("/")
        ? link.slice(link.lastIndexOf("/") + 1)
        : link;
      try {
        name = decodeURIComponent(name);
      } catch {
        /* keep as-is */
      }
      be.name = name.replace(/_/g, " ").trim();
      if (be.name) buffNameBlur();
    }
  }
  // Live lookup on Name tab-out: an existing catalog buff loads its stored
  // (possibly hand-corrected) row; a new name is fetched from the wiki —
  // the server builds the page URL from it, spaces → underscores.
  function buffNameBlur() {
    const n = ((be && be.name) || "").trim();
    if (!n || beBusy) return;
    const key = beNameKey(n);
    if (key === beChecked) return;
    beChecked = key;
    const src = allBuffs.find((b) => beNameKey(b.name) === key);
    if (src) {
      buffEdLoad(src.name);
      buffAdmMsg = `"${src.name}" is already in the catalog — fields loaded; saving will update it.`;
      return;
    }
    beExisting = false;
    beBusy = true;
    buffAdmMsg = `Looking up "${n}" on the wiki…`;
    PreviewBuff(n, "")
      .then((p) => {
        buffFill({ ...p, name: n }, be);
        beBusy = false;
      })
      .catch((e) => {
        buffAdmMsg = String(e) + " — fill the fields manually.";
        beBusy = false;
      });
  }

  function buffEdSave() {
    const mods = {};
    for (const [k] of BUFF_MOD_KEYS) if (+be.mods[k]) mods[k] = +be.mods[k];
    SaveBuff({ ...be, mods })
      .then(() => ListBuffs())
      .then((b) => {
        allBuffs = b;
        beExisting = true;
        buffAdmMsg =
          `"${be.name}" saved.` +
          (be.icon ? "" : " Icon will auto-scrape from the wiki.");
      })
      .catch((e) => (buffAdmMsg = String(e)));
  }
  function buffEdDelete() {
    const n = ((be && be.name) || beSel || "").trim();
    if (!n) return;
    DeleteBuff(n)
      .then(() => ListBuffs())
      .then((b) => {
        allBuffs = b;
        buffEdLoad("");
        buffAdmMsg = `"${n}" deleted (seeded buffs return on server restart).`;
      })
      .catch((e) => (buffAdmMsg = String(e)));
  }

  // ── Fuse Shared Magelos: browse / vote / copy shared magelos ──────────────
  let showLib = false;
  let libRows = [];
  let libErr = "";
  let libMsg = "";
  let libSelClasses = []; // class filter chips (empty = all classes)
  let libSelTags = []; // tag filter chips (every selected tag must match)
  let libOpen = 0; // expanded entry id (0 = collapsed)
  let libSlots = {}; // id -> [{slot, name, count}]
  let libShareBusy = false;
  let libCopySel = {}; // id -> chosen target character
  let libCopyBusy = 0;

  function openLib() {
    showLib = true;
    libErr = "";
    libMsg = "";
    libRefresh();
  }
  function libRefresh() {
    ListLibrary()
      .then((r) => (libRows = r))
      .catch((e) => (libErr = String(e)));
  }
  // Display name: the sharer names the entry; fall back for safety.
  function libName(r) {
    return r.name || (r.magelo && r.magelo !== "current" ? r.magelo : r.toon);
  }
  function libRowTags(r) {
    return (r.tags || "")
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
  }
  // Filter chips: every class and tag the library actually has entries for.
  // Multi-select — classes OR together (an entry has one class), tags AND
  // together (each selected tag narrows the list).
  $: libClasses = [
    ...new Set(libRows.map((r) => r.class).filter(Boolean)),
  ].sort();
  $: libTagsPresent = LIB_TAGS.filter((t) =>
    libRows.some((r) => libRowTags(r).includes(t)),
  );
  function libToggleClass(c) {
    libSelClasses = libSelClasses.includes(c)
      ? libSelClasses.filter((x) => x !== c)
      : [...libSelClasses, c];
  }
  function libToggleTag(t) {
    libSelTags = libSelTags.includes(t)
      ? libSelTags.filter((x) => x !== t)
      : [...libSelTags, t];
  }
  // Score-sorted (server pre-sorts too, but votes change live in this list).
  $: libShown = libRows
    .filter(
      (r) =>
        (!libSelClasses.length || libSelClasses.includes(r.class)) &&
        libSelTags.every((t) => libRowTags(r).includes(t)),
    )
    .slice()
    .sort((a, b) => b.votes - a.votes || libName(a).localeCompare(libName(b)));

  function libToggle(r) {
    if (libOpen === r.id) {
      libOpen = 0;
      return;
    }
    libOpen = r.id;
    if (!libSlots[r.id]) {
      GetLibraryMagelo(r.id)
        .then((slots) => {
          libSlots = { ...libSlots, [r.id]: slots };
          ensureLookup(slots.map((s) => s.name));
        })
        .catch((e) => (libErr = String(e)));
    }
  }
  function libVote(r) {
    VoteLibrary(r.id, !r.my_vote)
      .then((v) => {
        libRows = libRows.map((x) =>
          x.id === r.id ? { ...x, votes: v.votes, my_vote: v.my_vote } : x,
        );
      })
      .catch((e) => (libErr = String(e)));
  }
  function libDelete(r) {
    DeleteLibraryEntry(r.id)
      .then(() => {
        libRows = libRows.filter((x) => x.id !== r.id);
        if (libOpen === r.id) libOpen = 0;
      })
      .catch((e) => (libErr = String(e)));
  }
  // Sharing is a two-step: name the entry (50 chars max) and pick tags.
  const LIB_TAGS = [
    "BIS",
    "Value",
    "Twink",
    "Solo",
    "Starter",
    "Bot",
    "Nag/Vox",
  ];
  let libShareOpen = false;
  let libShareName = "";
  let libShareTags = {}; // tag -> bool
  function libShareStart() {
    libShareOpen = true;
    libErr = "";
    libMsg = "";
    libShareName = (curMagelo || charName).slice(0, 50);
    libShareTags = {};
  }
  async function libShareGo() {
    const name = libShareName.trim().slice(0, 50);
    if (!name) {
      libErr = "Name the shared magelo first.";
      return;
    }
    libShareBusy = true;
    libErr = "";
    libMsg = "";
    try {
      saveNow(); // flush pending editor changes so the server snapshot is fresh
      if (!curMagelo) await SaveMagelo(charName); // re-snapshot the outputfile
      await ShareMageloToLibrary(
        charName,
        curMagelo || "current",
        name,
        LIB_TAGS.filter((t) => libShareTags[t]),
        (info && info.race) || "",
        (info && info.level) || 0,
      );
      libMsg = `Shared "${name}" to the library.`;
      libShareOpen = false;
      libRefresh();
    } catch (e) {
      libErr = String(e);
    }
    libShareBusy = false;
  }
  // Mini-sheet helpers: rebuild worn/general structures from a slot snapshot.
  function libWornMap(slots) {
    const w = {};
    for (const s of slots)
      if (WORN_ORDER.includes(s.slot))
        w[s.slot] = { location: s.slot, name: s.name, count: s.count || 1 };
    return w;
  }
  function libGeneral(slots) {
    const out = [];
    for (let i = 1; i <= 8; i++) {
      const s = slots.find((x) => x.slot === "General" + i);
      out.push(
        s ? { location: s.slot, name: s.name, count: s.count || 1 } : null,
      );
    }
    return out;
  }
  // Stats bar for an expanded library entry: the main panel's formulas, fed
  // from the snapshot's worn gear only — creation points and buffs don't
  // travel with a share, so the sharer's real numbers may sit a bit higher.
  function libStats(slots, map, clsName, raceName, lvl) {
    const w = libWornMap(slots);
    const sum = (f) => {
      let n = 0;
      for (const s of WORN_ORDER) {
        const it = itemFor(w[s], map);
        if (it) n += it[f] || 0;
      }
      return n;
    };
    const g = {
      str: sum("str"),
      sta: sum("sta"),
      agi: sum("agi"),
      dex: sum("dex"),
      wis: sum("wis"),
      int: sum("int"),
      cha: sum("cha"),
      hp: sum("hp"),
      mana: sum("mana"),
      ac: sum("ac"),
      atk: sum("atk"),
      fire: sum("sv_fire"),
      cold: sum("sv_cold"),
      disease: sum("sv_disease"),
      poison: sum("sv_poison"),
      magic: sum("sv_magic"),
    };
    // No creation points and no buffs — neither travels with a shared snapshot.
    const st = characterStats({
      cls: clsName,
      race: raceName,
      level: lvl,
      gear: g,
    });
    let haste = 0;
    let wt = 0;
    for (const s of WORN_ORDER) {
      const it = itemFor(w[s], map);
      if (!it) continue;
      wt += it.wt || 0;
      const m = it.effect && /haste\s*\+?(\d+)\s*%/i.exec(it.effect);
      if (m) haste = Math.max(haste, +m[1]);
    }
    haste = Math.min(haste, lvl ? hasteCap(lvl) : 100);
    for (const gi of libGeneral(slots)) {
      const it = itemFor(gi, map);
      if (it) wt += it.wt || 0;
    }
    return {
      hp: st.hp,
      mana: st.mana,
      ac: g.ac,
      atk: g.atk,
      haste,
      weight: Math.round(wt),
      str: st.hasBase ? st.totals[0] : 0,
      totals: st.totals,
      hasBase: st.hasBase,
      gear: g,
      resists: st.resists,
    };
  }
  // Copy targets: the viewer's characters of the entry's class.
  function libTargets(r) {
    const cls = (r.class || "").toLowerCase();
    return cls
      ? allChars.filter((c) => c.class && c.class.toLowerCase() === cls)
      : [];
  }
  async function libSaveCopy(r) {
    const target = libCopySel[r.id] || (libTargets(r)[0] || {}).name;
    if (!target || libCopyBusy) return;
    libCopyBusy = r.id;
    libErr = "";
    libMsg = "";
    try {
      let slots = libSlots[r.id];
      if (!slots) {
        slots = await GetLibraryMagelo(r.id);
        libSlots = { ...libSlots, [r.id]: slots };
      }
      // Unique name on the target character: "Name", "Name 2", "Name 3"…
      const existing = (await ListMagelos(target)).map((m) => m.toLowerCase());
      const base = libName(r).slice(0, 28);
      let name = base;
      for (
        let i = 2;
        existing.includes(name.toLowerCase()) ||
        name.toLowerCase() === "current";
        i++
      )
        name = `${base} ${i}`;
      await SaveMageloSlots(target, name, slots);
      if (target.toLowerCase() === charName.toLowerCase())
        mageloList = [...mageloList, name].sort();
      libMsg = `Saved a copy as "${name}" on ${target}.`;
    } catch (e) {
      libErr = String(e);
    }
    libCopyBusy = 0;
  }

  // ── stats panel (estimates) ─────────────────────────────────────────────────
  // Tables and formulas live in eqstats.js — shared with the library cards
  // below so the two views can't disagree.

  // Green middle column on every stat row: what worn gear contributes, shown
  // apart from the total so the item half of a number is readable at a glance
  // (and quotable — "+80 mana from gear" is what pins down a mana calibration).
  // Empty string for zero, so the column stays blank rather than reading "+0".
  const gearBadge = (n, suffix = "") =>
    n ? `${n > 0 ? "+" : ""}${n}${suffix}` : "";

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
  $: gearStats = itemsBy &&
    worn && {
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
      atk: gearSum("atk"),
      fire: gearSum("sv_fire"),
      cold: gearSum("sv_cold"),
      disease: gearSum("sv_disease"),
      poison: gearSum("sv_poison"),
      magic: gearSum("sv_magic"),
    };
  // Totals, HP, mana and resists all come from the shared model — creation
  // points and active buffs fold in here, which is what separates this from
  // the library cards' gear-only view.
  $: stats = characterStats({
    cls,
    race,
    level,
    assigned,
    gear: gearStats || {},
    buff: buffStats,
  });
  $: totals = stats.totals;
  // gearStats is null until the item lookups land; gs keeps the markup terse.
  $: gs = gearStats || {};
  // STR total for the WEIGHT/STRENGTH readout (0 = race unknown, hide ratio).
  $: strTotal = base ? totals[0] || 0 : 0;
  $: hpEst = stats.hp;
  // -1 = a class that never has mana; the panel renders "—" for it.
  $: manaEst = stats.mana;
  // AC shown is the worn-gear AC sum.
  $: resists = gearStats ? stats.resists : [];

  // Weight: worn + general bags (bank excluded), honoring bag weight reduction.
  // itemsBy is referenced explicitly so the total recomputes when lookups land.
  // Haste: highest single worn-item haste (parsed from the effect text the
  // scraper folds it into) plus the highest spell haste among active buffs —
  // the two stack, but multiples within each kind don't.
  $: wornHaste = ((map) => {
    let h = 0;
    for (const slot of WORN_ORDER) {
      const it = itemFor(worn[slot], map);
      const m = it && it.effect && /haste\s*\+?(\d+)\s*%/i.exec(it.effect);
      if (m) h = Math.max(h, +m[1]);
    }
    return h;
  })(itemsBy);
  $: hasteTotal = Math.min(
    wornHaste + buffStats.haste,
    level ? hasteCap(level) : 100,
  );
  $: atkTotal = (gearStats ? gearStats.atk : 0) + buffStats.atk;

  // Regen from worn effects: Aura of Battle +2 HP each, Fungal Regrowth +15
  // HP each; Flowing Thought I–V gives mana regen equal to the numeral, but
  // only UNIQUE tiers count (two FT I items still total +1). Buffs can add
  // HP regen (hpregen mod).
  const FT_TIERS = { i: 1, ii: 2, iii: 3, iv: 4, v: 5 };
  $: regen = ((map) => {
    let hp = 0;
    const ft = new Set();
    for (const slot of WORN_ORDER) {
      const it = itemFor(worn[slot], map);
      const eff = ((it && it.effect) || "").toLowerCase();
      if (!eff) continue;
      if (eff.includes("aura of battle")) hp += 2;
      if (eff.includes("fungal regrowth")) hp += 15;
      if (eff.includes("regeneration")) hp += 5;
      const m = /flowing thought ([ivx]+)/.exec(eff);
      if (m && FT_TIERS[m[1]]) ft.add(FT_TIERS[m[1]]);
    }
    let mana = 0;
    for (const t of ft) mana += t;
    return { hp: hp + buffStats.hpregen, mana };
  })(itemsBy);

  // Carried weight: worn + General slots + bag contents, with each bag's
  // weight reduction applied to its contents (a 50% WR bag holding 5.0 wt
  // contributes 2.5). Bank never counts, and a STACK weighs its stated item
  // weight regardless of how many are in it — no count multipliers anywhere.
  // Rounded to the nearest integer for the WEIGHT/STR readout.
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
        if (it) w += it.wt || 0;
      }
      return Math.round(w);
    }
    for (const b of bags) {
      const bagIt = itemFor(b.bag, map);
      const wr = bagIt ? (bagIt.wr || 0) / 100 : 0;
      // The container (or a lone item/stack sitting in the slot) — full rate.
      if (bagIt) w += bagIt.wt || 0;
      let inner = 0;
      for (const c of b.contents) {
        const it = itemFor(c, map);
        if (it) inner += it.wt || 0;
      }
      w += inner * (1 - wr);
    }
    return Math.round(w);
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
        class:mg-btn-danger={delArm}
        title="Delete this magelo"
        on:click={deleteMagelo}
        on:mouseleave={() => {
          clearTimeout(delArmTimer);
          delArm = false;
        }}>{delArm ? "Really delete?" : "Delete"}</button
      >
    {/if}
    {#if mgErr && !nameDlg}<span class="mg-err">{mgErr}</span>{/if}
  </div>

  <div class="mg-row">
    <!-- left: stats panel -->
    <div class="mg-stats" class:mg-float={floating}>
      <div class="mg-name">{charName}</div>
      {#if info && info.class}
        <div class="mg-sub">{level} {info.class} ({info.race || "?"})</div>
      {/if}
      <div
        class="mg-est"
        title="Race base + class bonus + assigned creation points + worn gear. The green middle column is what worn gear alone contributes; the right column is the final value. Creation points are yours to assign below."
      >
        Estimates
      </div>

      <!-- Each row is label / worn-gear contribution (green) / final value. -->
      <div class="mg-core">
        <div>
          <span>HP</span>
          <span class="mg-core-g">{gearBadge(gs.hp)}</span>
          <span class="mg-core-v">{hpEst || "—"}</span>
        </div>
        <div>
          <span>MANA</span>
          <span class="mg-core-g">{gearBadge(gs.mana)}</span>
          <span class="mg-core-v">{manaEst >= 0 ? manaEst : "—"}</span>
        </div>
        <div>
          <span>AC</span>
          <span class="mg-core-g">{gearBadge(gs.ac)}</span>
          <span class="mg-core-v">{(gs.ac || 0) + buffStats.ac}</span>
        </div>
        <div>
          <span>HASTE</span>
          <span class="mg-core-g">{gearBadge(wornHaste, "%")}</span>
          <span class="mg-core-v">{hasteTotal}%</span>
        </div>
        <div>
          <span>ATK</span>
          <span class="mg-core-g">{gearBadge(gs.atk)}</span>
          <span class="mg-core-v">{atkTotal}</span>
        </div>
        <!-- Regen and weight are already pure item/buff sums — no base to split
             a gear contribution out of, so the middle cell only holds the column. -->
        <div>
          <span>HP REGEN</span>
          <span class="mg-core-g"></span>
          <span class="mg-core-v">+{regen.hp}</span>
        </div>
        <div>
          <span>MANA REGEN</span>
          <span class="mg-core-g"></span>
          <span class="mg-core-v">+{regen.mana}</span>
        </div>
        <!-- WEIGHT/STRENGTH — red when over STR (encumbered). -->
        <div>
          <span>WT/STR</span>
          <span class="mg-core-g"></span>
          <span class="mg-wt" class:enc={strTotal > 0 && weight > strTotal}
            >{weight}{strTotal > 0 ? `/${strTotal}` : ""}</span
          >
        </div>
      </div>

      <div class="mg-attrs">
        {#each STAT_NAMES as n, i}
          <div class="mg-attr">
            <span class="mg-attr-n">{n}</span>
            <span class="mg-attr-g">{gearBadge(gs[GKEY[i]])}</span>
            <span class="mg-attr-v">{base ? totals[i] : "—"}</span>
          </div>
        {/each}
      </div>

      <div class="mg-resists">
        {#each resists as [n, v], i}
          <div class="mg-res">
            <span>{n}</span>
            <span class="mg-res-g">{gearBadge(gs[RESIST_GEAR_KEY[i]])}</span>
            <span class="mg-res-v">{v}</span>
          </div>
        {/each}
      </div>

      <button class="mg-btn" on:click={() => (showAssign = !showAssign)}
        >Assign Points ({budget - spent} left)</button
      >
      {#if showAssign}
        <div class="mg-assign">
          <div class="mg-assign-note">
            Your {budget} creation points — set them as you did at character creation.
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
        <div
          class="mg-grid"
          class:mg-float={floating}
          style="animation-delay: -1.1s"
        >
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
                  <img
                    class="mg-icon"
                    src={iconBase + it.icon}
                    alt={inv.name}
                  />
                {:else}
                  <a
                    href={it.link || wikiLink(inv.name)}
                    target="_blank"
                    rel="noreferrer"
                  >
                    <img
                      class="mg-icon"
                      src={iconBase + it.icon}
                      alt={inv.name}
                    />
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
        <div
          class="mg-general-col"
          class:mg-float={floating}
          style="animation-delay: -2.3s"
        >
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
                    <img
                      class="mg-icon"
                      src={iconBase + it.icon}
                      alt={g.name}
                    />
                  {:else}
                    <a
                      href={it.link || wikiLink(g.name)}
                      target="_blank"
                      rel="noreferrer"
                    >
                      <img
                        class="mg-icon"
                        src={iconBase + it.icon}
                        alt={g.name}
                      />
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
                {#if g && g.count > 1}<span class="mg-count">{g.count}</span
                  >{/if}
              </div>
            {/each}
          </div>
        </div>
      </div>

      <!-- Active buffs, beneath the worn grid — full row width so complex
         buffs can list all their stat effects on one line. -->
      <div class="mg-buffs">
        {#each activeBuffs as b (b.name)}
          <div class="mg-buff" title={b.note || b.name}>
            {#if b.icon && iconBase}
              <img class="mg-buff-ico" src={iconBase + b.icon} alt="" />
            {/if}
            <span class="mg-buff-name">{b.name}</span>
            <span class="mg-buff-mods">{modText(b.mods)}</span>
            <button
              class="mg-buff-x"
              title="Remove buff"
              on:click={() => removeBuff(b.name)}>✕</button
            >
          </div>
        {/each}
        <button
          class="mg-btn mg-buff-add"
          on:click={() => {
            showBuffPick = true;
            buffErr = "";
          }}>+ Buff</button
        >
        {#if buffErr && !showBuffPick}<div class="mg-err">{buffErr}</div>{/if}
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
                        <a
                          href={it.link || wikiLink(c.name)}
                          target="_blank"
                          rel="noreferrer"
                        >
                          <img
                            class="mg-icon"
                            src={iconBase + it.icon}
                            alt={c.name}
                          />
                        </a>
                      {:else}
                        <a
                          class="mg-noicon"
                          href={wikiLink(c.name)}
                          target="_blank"
                          rel="noreferrer">{c.name.slice(0, 10)}</a
                        >
                      {/if}
                      {#if c.count > 1}<span class="mg-count">{c.count}</span
                        >{/if}
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
                  <a
                    href={it.link || wikiLink(b.bag.name)}
                    target="_blank"
                    rel="noreferrer"
                  >
                    <img
                      class="mg-icon"
                      src={iconBase + it.icon}
                      alt={b.bag.name}
                    />
                  </a>
                {:else}
                  <a
                    class="mg-noicon"
                    href={wikiLink(b.bag.name)}
                    target="_blank"
                    rel="noreferrer">{b.bag.name.slice(0, 10)}</a
                  >
                {/if}
                {#if b.bag.count > 1}<span class="mg-count">{b.bag.count}</span
                  >{/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  {/each}

  <!-- Item/buff admin tools: their endpoints are officer-gated server-side,
       so the buttons only show in admin mode now that everyone sees Magelo. -->
  <div class="mg-addrow">
    {#if isAdmin}
      <button class="mg-btn" on:click={openBuffAdmin}>Add Buffs…</button>
      <button class="mg-btn" on:click={openFails}>Failed Scrapes…</button>
      <button class="mg-btn" on:click={() => (showAdd = true)}>Add Item…</button
      >
    {/if}
  </div>
</div>

{#if showIconPick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <!-- mg-ov-top: opens on top of the Add Item dialog, which renders later in
       the DOM and would otherwise paint over it at the shared z-index. -->
  <div class="mg-ov mg-ov-top" on:click|self={() => (showIconPick = false)}>
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

{#if showBuffPick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={() => (showBuffPick = false)}>
    <div class="mg-dlg mg-dlg-sm">
      <div class="mg-dlg-title">Add Buff</div>
      {#if buffErr}<div class="mg-err">{buffErr}</div>{/if}
      <div class="mg-sugs">
        {#each allBuffs.filter((b) => !activeBuffKeys.has(beNameKey(b.name))) as b (b.name)}
          <button
            class="mg-sug mg-sug-buff"
            title={b.note || ""}
            on:click={() => addBuff(b)}
          >
            {#if b.icon && iconBase}
              <img class="mg-buff-ico" src={iconBase + b.icon} alt="" />
            {/if}
            <span class="mg-buff-name">{b.name}</span>
            <span class="mg-buff-mods">{modText(b.mods)}</span>
          </button>
        {:else}
          <div class="mg-dlg-note">
            Every buff in the catalog is already active.
          </div>
        {/each}
      </div>
      <div class="mg-dlg-btns">
        <button class="mg-btn" on:click={() => (showBuffPick = false)}
          >Close</button
        >
      </div>
    </div>
  </div>
{/if}

{#if showBuffAdmin}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={() => (showBuffAdmin = false)}>
    <div class="mg-dlg">
      <div class="mg-dlg-title">Add Buff to Catalog</div>
      <div class="mg-dlg-row">
        <input
          class="mg-in mg-in-link"
          placeholder="Paste the spell's wiki link…"
          bind:value={buffLink}
          on:keydown={(e) => e.key === "Enter" && buffUrlScrape()}
        />
        <button
          class="mg-btn"
          disabled={beBusy || !buffLink}
          on:click={buffUrlScrape}
          >{beBusy && !be ? "Scraping…" : "Scrape"}</button
        >
        <button class="mg-btn" disabled={beBusy} on:click={buffManual}
          >Manual</button
        >
      </div>
      <div class="mg-dlg-row">
        <select
          class="mg-in mg-in-link"
          on:change={(e) => buffEdLoad(e.target.value)}
        >
          <option value="">— or pick an existing buff to edit —</option>
          {#each allBuffs as b (b.name)}
            <option value={b.name} selected={beSel === b.name}>{b.name}</option>
          {/each}
        </select>
      </div>
      {#if be}
        <div class="mg-form">
          <label class="mg-f mg-f-wide">
            <span
              >Name (exact wiki spell name — looked up when you tab out)</span
            >
            <input class="mg-in" bind:value={be.name} on:blur={buffNameBlur} />
          </label>
          <label class="mg-f mg-f-wide">
            <span>Icon (blank = auto-scrape from the wiki)</span>
            <input
              class="mg-in"
              bind:value={be.icon}
              placeholder="Spellicon_X.png"
            />
          </label>
          <label class="mg-f mg-f-wide">
            <span>Doesn't stack with (comma-separated buff names)</span>
            <input class="mg-in" bind:value={be.conflicts} />
          </label>
          {#each BUFF_MOD_KEYS as [k, label]}
            <label class="mg-f">
              <span>{label}</span>
              <input class="mg-in" type="number" bind:value={be.mods[k]} />
            </label>
          {/each}
          <label class="mg-f mg-f-wide">
            <span>Stacking notes (reference only)</span>
            <input class="mg-in" bind:value={be.note} />
          </label>
        </div>
      {/if}
      {#if buffAdmMsg}<div class="mg-ok">{buffAdmMsg}</div>{/if}
      <div class="mg-dlg-btns">
        {#if beExisting}
          <button class="mg-btn" on:click={buffEdDelete}>Delete</button>
        {/if}
        <button class="mg-btn" on:click={() => (showBuffAdmin = false)}
          >Close</button
        >
        <button
          class="mg-btn mg-btn-go"
          disabled={!be || !be.name.trim() || beBusy}
          on:click={buffEdSave}
          >{beExisting ? "Update Buff" : "Add Buff"}</button
        >
      </div>
    </div>
  </div>
{/if}

{#if showLib}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="mg-ov" on:click|self={() => (showLib = false)}>
    <div class="mg-dlg mg-dlg-lib">
      <div class="mg-dlg-title">🌐 Fuse Shared Magelos</div>

      {#if libShareOpen}
        <div class="mg-lib-share">
          <input
            class="mg-in mg-in-link"
            maxlength="50"
            placeholder="Name this magelo (50 characters max)…"
            bind:value={libShareName}
            on:keydown={(e) => e.key === "Enter" && libShareGo()}
            use:focusIt
          />
          <div class="mg-lib-tagrow">
            {#each LIB_TAGS as t}
              <label class="mg-lib-tag-pick">
                <input type="checkbox" bind:checked={libShareTags[t]} />{t}
              </label>
            {/each}
            <span class="mg-tab-sp"></span>
            <button class="mg-btn" on:click={() => (libShareOpen = false)}
              >Cancel</button
            >
            <button
              class="mg-btn mg-btn-go"
              disabled={libShareBusy || !libShareName.trim()}
              on:click={libShareGo}
              >{libShareBusy ? "Sharing…" : "Share"}</button
            >
          </div>
        </div>
      {:else}
        <div class="mg-dlg-row">
          <button class="mg-btn" on:click={libShareStart}
            >Share "{curMagelo || "Current"}" ({charName})…</button
          >
        </div>
      {/if}

      <div class="mg-lib-filters">
        <button
          class="mg-tab"
          class:active={!libSelClasses.length && !libSelTags.length}
          on:click={() => {
            libSelClasses = [];
            libSelTags = [];
          }}>All</button
        >
        {#each libClasses as c}
          <button
            class="mg-tab"
            class:active={libSelClasses.includes(c)}
            title={c}
            on:click={() => libToggleClass(c)}>{classAbbr(c) || c}</button
          >
        {/each}
        {#if libTagsPresent.length}
          <span class="mg-lib-fsep"></span>
          {#each libTagsPresent as t}
            <button
              class="mg-tab"
              class:active={libSelTags.includes(t)}
              on:click={() => libToggleTag(t)}>{t}</button
            >
          {/each}
        {/if}
      </div>

      {#if libErr}<div class="mg-err">{libErr}</div>{/if}
      {#if libMsg}<div class="mg-ok">{libMsg}</div>{/if}

      <div class="mg-lib-list">
        {#each libShown as r (r.id)}
          <div class="mg-lib-row">
            <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
            <div class="mg-lib-head" on:click={() => libToggle(r)}>
              <span class="mg-lib-cls" title={r.class}
                >{classAbbr(r.class) || r.class || "?"}</span
              >
              <span class="mg-lib-name">{libName(r)}</span>
              {#each (r.tags || "")
                .split(",")
                .map((t) => t.trim())
                .filter(Boolean) as t}
                <span class="mg-lib-tag">{t}</span>
              {/each}
              <span class="mg-lib-by">
                {r.level ? r.level + " " : ""}{r.class}
                {r.toon ? `· ${r.toon}` : ""} — shared by {r.shared_by || "?"}
              </span>
              <button
                class="mg-lib-vote"
                class:voted={r.my_vote}
                title={r.my_vote ? "Remove your thumbs-up" : "Thumbs up"}
                on:click|stopPropagation={() => libVote(r)}>👍 {r.votes}</button
              >
              {#if r.mine || isAdmin}
                <button
                  class="mg-buff-x"
                  title="Remove from library"
                  on:click|stopPropagation={() => libDelete(r)}>✕</button
                >
              {/if}
            </div>
            {#if libOpen === r.id}
              <div class="mg-lib-body">
                {#if libSlots[r.id]}
                  {@const lw = libWornMap(libSlots[r.id])}
                  {@const ls = libStats(
                    libSlots[r.id],
                    itemsBy,
                    r.class,
                    r.race,
                    r.level,
                  )}
                  <div class="mg-lib-sheet">
                    <div class="mg-lib-stats">
                      <div
                        class="mg-est"
                        title="Race base + class bonus + worn gear (green middle column) — creation points and buffs don't travel with a share, so the sharer's real numbers may sit a bit higher."
                      >
                        Estimates
                      </div>
                      <div class="mg-core">
                        <div>
                          <span>HP</span>
                          <span class="mg-core-g">{gearBadge(ls.gear.hp)}</span>
                          <span class="mg-core-v">{ls.hp || "—"}</span>
                        </div>
                        <div>
                          <span>MANA</span>
                          <span class="mg-core-g"
                            >{gearBadge(ls.gear.mana)}</span
                          >
                          <span class="mg-core-v"
                            >{ls.mana >= 0 ? ls.mana : "—"}</span
                          >
                        </div>
                        <!-- A shared snapshot carries no buffs, so AC/HASTE/ATK
                             here ARE the gear sum — badging them would just
                             print the same number twice. -->
                        <div>
                          <span>AC</span>
                          <span class="mg-core-g"></span>
                          <span class="mg-core-v">{ls.ac}</span>
                        </div>
                        <div>
                          <span>HASTE</span>
                          <span class="mg-core-g"></span>
                          <span class="mg-core-v">{ls.haste}%</span>
                        </div>
                        <div>
                          <span>ATK</span>
                          <span class="mg-core-g"></span>
                          <span class="mg-core-v">{ls.atk}</span>
                        </div>
                        <div>
                          <span>WT/STR</span>
                          <span class="mg-core-g"></span>
                          <span
                            class="mg-wt"
                            class:enc={ls.str > 0 && ls.weight > ls.str}
                            >{ls.weight}{ls.str > 0 ? `/${ls.str}` : ""}</span
                          >
                        </div>
                      </div>
                      <div class="mg-attrs">
                        {#each STAT_NAMES as n, i}
                          <div class="mg-attr">
                            <span class="mg-attr-n">{n}</span>
                            <span class="mg-attr-g"
                              >{gearBadge(ls.gear[GKEY[i]])}</span
                            >
                            <span class="mg-attr-v"
                              >{ls.hasBase ? ls.totals[i] : "—"}</span
                            >
                          </div>
                        {/each}
                      </div>
                      <div class="mg-resists">
                        {#each ls.resists as [n, v], i}
                          <div class="mg-res">
                            <span>{n}</span>
                            <span class="mg-res-g"
                              >{gearBadge(ls.gear[RESIST_GEAR_KEY[i]])}</span
                            >
                            <span class="mg-res-v">{v}</span>
                          </div>
                        {/each}
                      </div>
                    </div>
                    <div class="mg-grid">
                      {#each WORN_ORDER as slot}
                        {@const inv = lw[slot]}
                        {@const it = itemFor(inv, itemsBy)}
                        <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
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
                            <a
                              href={it.link || wikiLink(inv.name)}
                              target="_blank"
                              rel="noreferrer"
                            >
                              <img
                                class="mg-icon"
                                src={iconBase + it.icon}
                                alt={inv.name}
                              />
                            </a>
                          {:else if inv}
                            <a
                              class="mg-noicon"
                              href={wikiLink(inv.name)}
                              target="_blank"
                              rel="noreferrer">{inv.name.slice(0, 12)}</a
                            >
                          {:else}
                            <span class="mg-slot-label"
                              >{slot.replace(/[12]$/, "")}</span
                            >
                          {/if}
                        </div>
                      {/each}
                    </div>
                    <div class="mg-general">
                      {#each libGeneral(libSlots[r.id]) as g, gi}
                        {@const it = itemFor(g, itemsBy)}
                        <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
                        <div
                          class="mg-slot"
                          class:filled={!!g}
                          on:mouseenter={(e) => g && showTip(e, g)}
                          on:mousemove={(e) => g && showTip(e, g)}
                          on:mouseleave={hideTip}
                          role="img"
                        >
                          {#if g && it && it.icon}
                            <a
                              href={it.link || wikiLink(g.name)}
                              target="_blank"
                              rel="noreferrer"
                            >
                              <img
                                class="mg-icon"
                                src={iconBase + it.icon}
                                alt={g.name}
                              />
                            </a>
                          {:else if g}
                            <a
                              class="mg-noicon"
                              href={wikiLink(g.name)}
                              target="_blank"
                              rel="noreferrer">{g.name.slice(0, 12)}</a
                            >
                          {:else}
                            <span class="mg-slot-label">{gi + 1}</span>
                          {/if}
                          {#if g && g.count > 1}<span class="mg-count"
                              >{g.count}</span
                            >{/if}
                        </div>
                      {/each}
                    </div>
                  </div>
                  <div class="mg-lib-copy">
                    {#if libTargets(r).length}
                      <span class="mg-lib-copy-lbl">Save a copy to</span>
                      <select
                        class="mg-in mg-lib-sel"
                        bind:value={libCopySel[r.id]}
                      >
                        {#each libTargets(r) as c}
                          <option value={c.name}>{c.name}</option>
                        {/each}
                      </select>
                      <button
                        class="mg-btn"
                        disabled={libCopyBusy === r.id}
                        on:click={() => libSaveCopy(r)}
                        >{libCopyBusy === r.id
                          ? "Saving…"
                          : "Save Copy"}</button
                      >
                    {:else}
                      <span class="mg-lib-copy-lbl">
                        No {r.class || "matching-class"} characters of yours to copy
                        this to.
                      </span>
                    {/if}
                  </div>
                {:else}
                  <div class="mg-dlg-note">Loading…</div>
                {/if}
              </div>
            {/if}
          </div>
        {:else}
          <div class="mg-dlg-note">
            Nothing in the library yet — share one of your magelos to start it
            off.
          </div>
        {/each}
      </div>

      <div class="mg-dlg-btns">
        <button class="mg-btn" on:click={() => (showLib = false)}>Close</button>
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
        <button class="mg-btn" on:click={() => (showFails = false)}
          >Close</button
        >
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
                <th>INT</th><th>WIS</th><th>CHA</th><th>SvM</th>
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
                  <td>{t.cha || ""}</td>
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
        {nameDlg === "new" ? "New Magelo" : "Rename Magelo"}
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
        <button
          class="mg-btn"
          disabled={addBusy || !addLink}
          on:click={addPreview}
          >{addBusy && !addItem ? "Scraping…" : "Scrape"}</button
        >
        <button class="mg-btn" disabled={addBusy} on:click={addManual}
          >Manual</button
        >
      </div>

      {#if addItem}
        <div class="mg-dlg-note">
          Review the fields — correct anything that's wrong, then save. Name is
          required; use the exact in-game spelling so inventory lookups match.
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
  /* ── levitate flavor ── */
  /* A levitation buff sets the sheet's panels gently bobbing; the worn grid
     and General column carry inline animation-delays so the three drift out
     of phase instead of moving as one slab. transform doesn't affect layout,
     so nothing else shifts. */
  .mg-float {
    animation: mg-bob 3.4s ease-in-out infinite;
  }
  @keyframes mg-bob {
    0%,
    100% {
      transform: translateY(0);
    }
    50% {
      transform: translateY(-6px);
    }
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
  /* The 8 primary slots, EQ-style: two columns filled DOWNWARD, so the layout
     reads 1 5 / 2 6 / 3 7 / 4 8 the way the in-game inventory does. Fixing the
     rows and flowing by column does this purely visually — the markup still
     emits General1-8 in order, so slot indices and click targets are unchanged.
     Bags and bank keep their row-major fill (.mg-bag-grid). */
  .mg-general {
    display: grid;
    grid-template-rows: repeat(4, 46px);
    grid-auto-flow: column;
    grid-auto-columns: 46px;
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
  /* Row layout: label | gear contribution (green) | final value. The gear cell
     takes the slack so the two right columns stay in line down the panel. */
  .mg-core div {
    display: flex;
    align-items: baseline;
    gap: 6px;
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
  /* Values below use a double class to outrank the .mg-core span label styling. */
  .mg-core span.mg-core-g {
    color: var(--success);
    font-size: 10.5px;
    letter-spacing: normal;
    font-variant-numeric: tabular-nums;
    margin-left: auto;
    text-align: right;
  }
  .mg-core span.mg-core-v,
  .mg-core span.mg-wt {
    color: var(--text-primary);
    font-size: 12.5px;
    letter-spacing: normal;
    font-variant-numeric: tabular-nums;
    min-width: 48px;
    text-align: right;
  }
  .mg-core span.mg-wt.enc {
    color: #ff5555;
    font-weight: 700;
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
  /* Same three columns as .mg-core: name | gear (green) | final value. */
  .mg-attr-v {
    color: var(--text-primary);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    min-width: 34px;
    text-align: right;
  }
  .mg-attr-g {
    color: var(--success);
    font-size: 10.5px;
    font-variant-numeric: tabular-nums;
    margin-left: auto;
    text-align: right;
  }
  .mg-resists {
    border-top: 1px solid var(--border);
    padding-top: 6px;
  }
  /* Same three columns as .mg-core: label | gear (green) | final value. */
  .mg-res {
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: 11.5px;
    color: var(--text-primary);
    padding: 1px 0;
  }
  .mg-res span {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
  }
  .mg-res span.mg-res-g {
    color: var(--success);
    font-size: 10px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    margin-left: auto;
    text-align: right;
  }
  .mg-res span.mg-res-v {
    color: var(--text-primary);
    font-size: 11.5px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    min-width: 34px;
    text-align: right;
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
  /* Armed delete confirm — same red as errors. */
  .mg-btn.mg-btn-danger {
    color: #ff6b6b;
    border-color: #ff6b6b;
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

  /* ── buffs (beneath the General grid) ── */
  .mg-buffs {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-top: 20px;
    max-width: 300px;
  }
  /* ── Fuse Shared Magelos ── */
  .mg-dlg-lib {
    width: 720px;
    max-width: 94vw;
  }
  .mg-lib-share {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .mg-lib-tagrow {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .mg-lib-tag-pick {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .mg-lib-filters {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
  }
  /* Divider between the class chips and the tag chips. */
  .mg-lib-fsep {
    width: 1px;
    align-self: stretch;
    background: var(--border);
    margin: 0 4px;
  }
  .mg-lib-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
    max-height: 58vh;
    overflow-y: auto;
  }
  .mg-lib-row {
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--bg-panel);
  }
  .mg-lib-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 8px;
    font-size: 12px;
    cursor: pointer;
  }
  .mg-lib-head:hover {
    background: var(--bg-input);
  }
  .mg-lib-cls {
    color: var(--accent);
    font-weight: 700;
    font-size: 11px;
    width: 30px;
    flex: none;
  }
  .mg-lib-name {
    color: var(--text-primary);
    font-weight: 600;
    white-space: nowrap;
  }
  .mg-lib-tag {
    border: 1px solid var(--accent-dim);
    color: var(--accent);
    border-radius: 8px;
    font-size: 10px;
    padding: 0 6px;
    flex: none;
  }
  .mg-lib-by {
    color: var(--text-muted);
    font-size: 11px;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    text-align: right;
  }
  .mg-lib-vote {
    background: none;
    border: 1px solid var(--border);
    border-radius: 10px;
    color: var(--text-secondary);
    font-size: 11px;
    padding: 1px 8px;
    cursor: pointer;
    flex: none;
  }
  .mg-lib-vote.voted {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .mg-lib-body {
    border-top: 1px solid var(--border);
    padding: 8px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .mg-lib-sheet {
    display: flex;
    gap: 14px;
    align-items: flex-start;
  }
  /* Compact stats bar on the left of an expanded entry — reuses the main
     panel's .mg-core/.mg-attrs/.mg-resists row styles. */
  .mg-lib-stats {
    flex: 0 0 170px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .mg-lib-copy {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .mg-lib-copy-lbl {
    font-size: 11px;
    color: var(--text-muted);
  }
  .mg-lib-sel {
    width: 170px;
    flex: none;
  }
  .mg-buff {
    display: flex;
    align-items: center;
    gap: 6px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 2px 6px;
    font-size: 11.5px;
  }
  .mg-buff-ico {
    width: 18px;
    height: 18px;
    flex: none;
  }
  .mg-buff-name {
    color: var(--text-primary);
    font-weight: 600;
    white-space: nowrap;
  }
  .mg-buff-mods {
    color: var(--text-secondary);
    font-size: 10.5px;
    min-width: 0;
  }
  .mg-buff-x {
    margin-left: auto;
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 11px;
    padding: 0 2px;
  }
  .mg-buff-x:hover {
    color: #ff5555;
  }
  .mg-buff-add {
    align-self: flex-start;
    margin-top: 2px;
  }
  .mg-sug-buff {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  /* ── Add Item icon browser ── */
  /* Double class: .mg-ov is defined later in the file and would otherwise
     win the z-index at equal specificity (same trap as .mg-gen-title). */
  .mg-ov.mg-ov-top {
    z-index: 320; /* above the Add Item dialog's .mg-ov (300) */
  }
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
