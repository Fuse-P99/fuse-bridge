<script>
  import { onMount, tick } from "svelte";
  import { slide } from "svelte/transition";
  import {
    GetCharNames,
    GetCharContent,
    GetCharInventory,
    GetCharSpellbook,
    GetCharClassWithInference,
    GetSpellsForClass,
    GetCharInfos,
    RefreshCharInfos,
    GetCharTable,
    SearchCharItems,
    GetSettings,
    SaveSettings,
    IsFilteredToon,
    ToggleFilteredToon,
    SetFilteredToons,
    IsAdminMode,
    IsOfficer,
    GetToonQuests,
    AssignToonQuest,
    UnassignToonQuest,
    SetToonQuestStep,
    ListAssignableQuests,
    WhoHasMedallionPieces,
    RefreshQuestDefs,
    GetItemByName,
    WhoHasItem,
    AddQuestMarker,
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "../lib/scale.js";
  import { classAbbr } from "../lib/classAbbr.js";
  import {
    stepLine,
    sayLines,
    kindLabel,
    rewardText,
  } from "../lib/questSteps.js";
  import { tipStats, TIP_RULE } from "../lib/itemTip.js";
  import AddSpellModal from "../lib/AddSpellModal.svelte";
  import MageloView from "../lib/MageloView.svelte";

  let isAdmin = false;
  let isOfficer = false;
  let showAddSpell = false;

  let chars = []; // CharEntry[]
  let selected = "";
  let rawContent = "";
  let highlighted = "";
  let query = "";
  let excludeBots = true;
  let excludeFiltered = true;
  let charInfos = {}; // lower(name) -> { level, class }
  let matchOffsets = [];
  let matchIdx = 0;
  let detailEl;

  let detailTab = "all"; // 'all' | 'inventory' | 'spells'
  let inventoryItems = []; // InventoryItem[]
  // Bumped by the filter-row "Fuse Shared Magelos" button; MageloView opens
  // its library dialog on each bump (the button also jumps to the sub-tab).
  // Reset on character switch: {#key selected} recreates MageloView, and a
  // stale non-zero request would re-open the library on every fresh mount.
  let libraryOpenReq = 0;
  $: selected, (libraryOpenReq = 0);

  // ── quick results ───────────────────────────────────────────────────────────
  // Cross-character item/spell search. The detail pane answers "what does THIS
  // character have"; this answers "who has this", which is the question you
  // actually ask before a raid. Server-side (one bridge call per search) —
  // doing it here would mean two file reads per character per keystroke.
  const QUICK_MIN = 2; // mirrors charSearchMinQuery in charsearch.go
  let quickRows = [];
  let quickTotal = 0;
  let quickTruncated = false;
  let quickLoading = false;
  let quickOpen = true;
  let quickTimer;

  $: quickVisible = query.trim().length >= QUICK_MIN;

  // Rows arrive sorted by name, so a run-length pass is all the grouping needs.
  // Grouped because a substring query ("boots") legitimately matches several
  // different items, and a flat list of locations with no item name attached
  // would be unreadable.
  $: quickGroups = (() => {
    const groups = [];
    let cur = null;
    for (const r of quickRows) {
      if (!cur || cur.name !== r.name) {
        cur = { name: r.name, kind: r.kind, rows: [] };
        groups.push(cur);
      }
      cur.rows.push(r);
    }
    return groups;
  })();

  function scheduleQuickSearch() {
    clearTimeout(quickTimer);
    if (query.trim().length < QUICK_MIN) {
      quickRows = [];
      quickTotal = 0;
      quickTruncated = false;
      quickLoading = false;
      return;
    }
    quickLoading = true;
    quickTimer = setTimeout(runQuickSearch, 220);
  }

  async function runQuickSearch() {
    const q = query;
    try {
      const res = await SearchCharItems(q, excludeBots, excludeFiltered);
      // A newer keystroke owns the panel now — dropping this reply keeps a slow
      // search from overwriting a fast one that came after it.
      if (q !== query) return;
      quickRows = res?.rows || [];
      quickTotal = res?.total || 0;
      quickTruncated = !!res?.truncated;
    } catch (e) {
      if (q !== query) return;
      quickRows = [];
      quickTotal = 0;
      quickTruncated = false;
    } finally {
      if (q === query) quickLoading = false;
    }
  }

  // Jump to the character holding the item, in the detail view where the full
  // inventory is. The query stays put so the panel is still there to go back to.
  function pickQuick(name) {
    viewMode = "detail";
    detailTab = "inventory";
    selectedSet = new Set([name]);
    selectChar(name);
  }

  // ── table view ──────────────────────────────────────────────────────────────
  let viewMode = "detail"; // 'detail' | 'table'
  let tableRows = []; // CharTableRow[]
  let tableLoading = false;
  let tableEl; // scroll container, for search highlight/jump
  let tableMatchCount = 0;
  let tableMatchIdx = 0;
  let sortKey = "name";
  let sortDir = 1; // 1 asc, -1 desc

  // Class/Race/Level were folded into a tooltip on the Name cell to make room
  // for the item sections.
  const textCols = [
    { key: "name", label: "Name" },
    { key: "zone", label: "Zone" },
    { key: "bind", label: "Bind" },
  ];
  const keyCols = [
    { key: "cs", label: "CS" },
    { key: "ss", label: "SS" },
    { key: "hs", label: "HS" },
    { key: "seb", label: "Seb" },
    { key: "st", label: "ST" },
    { key: "vp", label: "VP" },
  ];
  const keyKeys = keyCols.map((c) => c.key);
  const keyTitle = {
    cs: "CS — Tooth of the Cobalt Scar (Cobalt Scar)",
    ss: "SS — Shrine Key",
    hs: "HS — Key to Charasis (Howling Stones)",
    seb: "Seb — Trakanon Idol (Sebilis)",
    st: "ST — Sleeper's Key (Sleeper's Tomb)",
    vp: "VP — Key of Veeshan (Veeshan's Peak)",
  };
  // Item sections (counted — a green badge shows the count when > 1). IDs
  // mirror itemColumns in the Go side; tooltips show nickname + exact item.
  const utilCols = [
    { key: "rp", label: "Rp", title: "Rp — Reaper (Reaper of the Dead)" },
    { key: "jb", label: "JB", title: "JB — JBoots (Journeyman's Boots)" },
    {
      key: "peg",
      label: "Peg",
      title: "Peg — Peggy Cloak (Pegasus Feather Cloak)",
    },
    {
      key: "tbw",
      label: "TBW",
      title: "TBW — Thin Boned Wand (Thin Boned Wand)",
    },
    {
      key: "ttm",
      label: "TTM",
      title: "TTM — Stun Totem (Forlorn Totem of Rolfron Zek)",
    },
    {
      key: "sof",
      label: "SOF",
      title: "SOF — Scepter (Scepter of the Forlorn)",
    },
  ];
  const mobCols = [
    {
      key: "wc",
      label: "WC",
      title: "WC — WC cap (Leatherfoot Raider Skullcap)",
    },
    { key: "ot", label: "OT", title: "OT — OT Hammer (Worker Sledgemallet)" },
    {
      key: "thg",
      label: "Thg",
      title: "Thg — Thurg Pot (Vial of Velium Vapors)",
    },
  ];
  const consCols = [
    {
      key: "sow",
      label: "SOW",
      title: "SOW — Sow Pot (10 Dose Blood of the Wolf)",
    },
    {
      key: "shr",
      label: "SHR",
      title: "SHR — Shrink Pot (10 Dose Ant's Potion)",
    },
    {
      key: "wrt",
      label: "WRT",
      title: "WRT — Wort Pot (10 Dose Potion of Stinging Wort)",
    },
    {
      key: "nul",
      label: "NUL",
      title: "NUL — Null Pot (10 Dose Greater Null Potion)",
    },
  ];
  const itemGroups = [
    { title: "Utilities", cols: utilCols },
    { title: "Mobilization", cols: mobCols },
    { title: "Consumables", cols: consCols },
  ];
  const itemKeys = itemGroups.flatMap((g) => g.cols.map((c) => c.key));

  // "60 Necromancer (Iksar)" — the Name cell's hover tooltip, replacing the
  // old Class/Race/Level columns.
  function charTitle(row) {
    let s = [row.level || "", row.class || ""].filter(Boolean).join(" ");
    if (row.race) s += (s ? " " : "") + `(${row.race})`;
    return s;
  }

  async function loadTable() {
    tableLoading = true;
    try {
      tableRows = (await GetCharTable(excludeBots, excludeFiltered)) || [];
    } catch {
      tableRows = [];
    } finally {
      tableLoading = false;
    }
  }

  async function toggleView() {
    viewMode = viewMode === "table" ? "detail" : "table";
    if (viewMode === "table") {
      await loadTable();
      tableMatchIdx = 0;
      if (query) refreshTableMarks(true);
    } else if (selected) {
      // Returning to the verbose view opens whichever row was selected in the
      // table (row clicks there only select — they don't switch views).
      selectedSet = new Set([selected]);
      selectChar(selected);
    }
  }

  function sortBy(key) {
    if (sortKey === key) sortDir = -sortDir;
    else {
      sortKey = key;
      sortDir = 1;
    }
  }

  $: sortedRows = (() => {
    const rows = [...tableRows];
    const k = sortKey;
    rows.sort((a, b) => {
      if (k === "level") {
        if (a.level !== b.level) return (a.level - b.level) * sortDir;
        return a.name.localeCompare(b.name);
      }
      if (keyKeys.includes(k)) {
        const av = a.keys && a.keys[k] ? 1 : 0;
        const bv = b.keys && b.keys[k] ? 1 : 0;
        if (av !== bv) return (av - bv) * sortDir;
        return a.name.localeCompare(b.name);
      }
      if (itemKeys.includes(k)) {
        const av = (a.items && a.items[k]) || 0;
        const bv = (b.items && b.items[k]) || 0;
        if (av !== bv) return (av - bv) * sortDir;
        return a.name.localeCompare(b.name);
      }
      const av = (a[k] || "").toLowerCase();
      const bv = (b[k] || "").toLowerCase();
      const c = av.localeCompare(bv);
      return c !== 0 ? c * sortDir : a.name.localeCompare(b.name);
    });
    return rows;
  })();

  function relTime(ms) {
    const mins = Math.floor((Date.now() - ms) / 60000);
    if (mins < 1) return "just now";
    if (mins < 60) return `${mins} minute${mins === 1 ? "" : "s"} ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return `${hrs} hour${hrs === 1 ? "" : "s"} ago`;
    const days = Math.floor(hrs / 24);
    return `${days} day${days === 1 ? "" : "s"} ago`;
  }
  function fmtUpdated(ms) {
    if (!ms) return "";
    return `Updated: ${new Date(ms).toLocaleString()} (${relTime(ms)})`;
  }

  // Cell highlight (wraps query matches in <mark>) for the table search.
  function hlCell(text, q) {
    const safe = esc(text || "");
    if (!q) return safe;
    q = q.toLowerCase();
    const lower = (text || "").toLowerCase();
    let out = "",
      last = 0;
    for (let p = 0; ; ) {
      const i = lower.indexOf(q, p);
      if (i === -1) break;
      out +=
        esc(text.slice(last, i)) +
        "<mark>" +
        esc(text.slice(i, i + q.length)) +
        "</mark>";
      last = i + q.length;
      p = last;
    }
    return out + esc(text.slice(last));
  }

  function applyTableMarks(scroll) {
    if (!tableEl) return;
    const marks = tableEl.querySelectorAll("mark");
    tableMatchCount = marks.length;
    if (tableMatchIdx >= tableMatchCount) tableMatchIdx = 0;
    marks.forEach((m, i) => m.classList.toggle("current", i === tableMatchIdx));
    if (scroll && tableMatchCount)
      marks[tableMatchIdx]?.scrollIntoView({
        block: "center",
        behavior: "smooth",
      });
  }
  async function refreshTableMarks(scroll) {
    await tick();
    applyTableMarks(scroll);
  }
  function prevTableMatch() {
    if (!tableMatchCount) return;
    tableMatchIdx = (tableMatchIdx - 1 + tableMatchCount) % tableMatchCount;
    applyTableMarks(true);
  }
  function nextTableMatch() {
    if (!tableMatchCount) return;
    tableMatchIdx = (tableMatchIdx + 1) % tableMatchCount;
    applyTableMarks(true);
  }

  // Re-apply highlight (no scroll) when the sorted rows change (sort/reload).
  // Typing is handled by handleSearch, which scrolls to the first match.
  $: if (viewMode === "table") {
    sortedRows;
    if (tableEl) refreshTableMarks(false);
  }

  // Table rows only select/highlight — the detail view opens when the user
  // toggles back to it (see toggleView).
  function selectFromTable(name) {
    selected = name;
  }

  // Class + spellbook — loaded eagerly in selectChar
  let charClass = ""; // canonical class name; '' = unknown
  let charClassLoading = false;
  let spellbook = null; // string[] from local file; null = file not found

  // Spell list — loaded lazily when Spells tab is first opened
  let spellList = [];
  let spellsLoaded = ""; // which character's spell list is cached
  let spellsLoading = false;
  let spellsError = "";

  // Classes with no player-castable spells — hide the Spells tab for these.
  const nonCasterClasses = new Set(["Monk", "Rogue", "Warrior"]);

  // Multi-select: CTRL/CMD+click toggles, CTRL/CMD+A selects all displayed.
  // `selected` still drives the detail pane (single); `selectedSet` drives the
  // highlight + batch Filter/Unfilter All.
  let selectedSet = new Set();
  let listEl;

  // Context menu. batch=true when acting on the whole selectedSet.
  let ctx = {
    visible: false,
    x: 0,
    y: 0,
    name: "",
    filtered: false,
    batch: false,
    count: 0,
  };

  // ── data loading ──────────────────────────────────────────────────────────

  async function loadChars(keepSelection = false) {
    chars = (await GetCharNames(query, excludeBots, excludeFiltered)) || [];
    if (!keepSelection && selected && !chars.some((e) => e.name === selected)) {
      selected = "";
      rawContent = "";
      highlighted = "";
      inventoryItems = [];
      clearState();
    }
    await loadCharInfos(chars.map((c) => c.name));
  }

  // Populate level/class for names not yet in this session's cache: show the
  // local %APPDATA% cache instantly, then refresh from the server (which also
  // updates the on-disk cache). Each name is resolved at most once per session.
  async function loadCharInfos(names) {
    const missing = names.filter((n) => !(n.toLowerCase() in charInfos));
    if (!missing.length) return;
    applyCharInfos(missing, (await GetCharInfos(missing)) || {}); // instant (cache)
    applyCharInfos(missing, (await RefreshCharInfos(missing)) || {}); // fresh (server)
  }

  function applyCharInfos(names, got) {
    const merged = { ...charInfos };
    for (const n of names) {
      const k = n.toLowerCase();
      if (got[k]) merged[k] = got[k];
      else if (!(k in merged)) merged[k] = { level: 0, class: "" }; // mark attempted
    }
    charInfos = merged;
  }

  // meta string ("60 ENC") for a character; '' when class is unknown. infos is
  // passed explicitly so Svelte re-renders the list when the cache updates.
  function charMeta(name, infos) {
    const ci = infos[name.toLowerCase()];
    if (!ci || !ci.class) return "";
    const ab = classAbbr(ci.class);
    if (!ab) return "";
    return ci.level > 0 ? `${ci.level} ${ab}` : ab;
  }

  // Last-seen zone for a character; '' when unknown.
  function charZone(name, infos) {
    const ci = infos[name.toLowerCase()];
    return ci && ci.zone ? ci.zone : "";
  }

  function clearState() {
    charClass = "";
    charClassLoading = false;
    spellbook = null;
    spellList = [];
    spellsLoaded = "";
    spellsError = "";
    questsData = null;
    questsLoaded = "";
    questsError = "";
    questsOpen = {};
    addQuestOpen = false;
    addQuestQuery = "";
    removeArm = 0;
  }

  async function selectChar(name) {
    selected = name;
    clearState();

    charClassLoading = true;

    // Load content, inventory, and spellbook all in parallel (all local/fast).
    const [content, inventory, sb] = await Promise.all([
      GetCharContent(name),
      GetCharInventory(name),
      GetCharSpellbook(name),
    ]);

    // Guard: user may have clicked a different character while we were loading.
    if (selected !== name) return;

    rawContent = content;
    inventoryItems = inventory || [];
    spellbook = sb; // null = file not found; [] = file present but empty
    rebuildHighlight();

    // Resolve class via server then spellbook inference (slow — server HTTP call).
    const cls = (await GetCharClassWithInference(name, sb || [])) || "";
    if (selected === name) {
      charClass = cls;
      charClassLoading = false;
    }
  }

  // Show the Spells tab unless class is definitively a non-caster.
  $: showSpellsTab = !charClass || !nonCasterClasses.has(charClass);

  async function openSpellsTab() {
    detailTab = "spells";
    await loadSpellList();
  }

  async function loadSpellList() {
    if (spellsLoaded === selected) return;
    if (!charClass) return; // nothing to load yet; tab shows "class unknown"
    spellsLoading = true;
    spellsError = "";
    spellList = [];
    try {
      spellList = (await GetSpellsForClass(charClass)) || [];
    } catch (e) {
      spellsError = String(e);
    } finally {
      spellsLoading = false;
      spellsLoaded = selected;
    }
  }

  // Reload the spell list when class resolves and the Spells tab is active.
  $: if (charClass && detailTab === "spells" && spellsLoaded !== selected) {
    loadSpellList();
  }

  // ── quests sub-tab ─────────────────────────────────────────────────────────
  // Per-character quest tracking (questprogress.go). Local-first — works
  // unlinked; the one dependency is the cached quest catalog.
  let questsData = null; // ToonQuestView
  let questsLoading = false;
  let questsError = "";
  let questsLoaded = ""; // which character the data belongs to
  let questsOpen = {}; // quest id → card expanded
  let addQuestOpen = false;
  let addQuestQuery = "";
  let assignable = [];
  let removeArm = 0; // quest id armed for removal (two-click confirm)
  let removeArmTimer;

  async function openQuestsTab() {
    detailTab = "quests";
    await loadQuests();
  }
  async function loadQuests(force = false) {
    if (!force && questsLoaded === selected) return;
    questsLoading = true;
    questsError = "";
    try {
      questsData = await GetToonQuests(selected);
      questsLoaded = selected;
    } catch (e) {
      questsError = String(e);
    } finally {
      questsLoading = false;
    }
  }
  $: if (detailTab === "quests" && selected && questsLoaded !== selected) {
    loadQuests();
  }

  async function toggleStep(qid, order, done) {
    const before = doneCountOf(qid);
    try {
      await SetToonQuestStep(selected, qid, order, done);
      await loadQuests(true);
    } catch (e) {
      questsError = String(e);
      return;
    }
    // Ticking a step ticks everything it implies (server-side); say so when it
    // happened — boxes ticking themselves further up the list is otherwise
    // startling.
    if (done) {
      const added = doneCountOf(qid) - before - 1;
      clearTimeout(autoTickTimer);
      autoTicked = added > 0 ? `${qid}:${order}` : "";
      autoTickedN = added;
      if (added > 0) autoTickTimer = setTimeout(() => (autoTicked = ""), 2600);
    }
  }
  function doneCountOf(qid) {
    const a = ((questsData && questsData.assignments) || []).find(
      (x) => x.quest.id === qid,
    );
    return a ? a.done_count : 0;
  }
  let autoTicked = "";
  let autoTickedN = 0;
  let autoTickTimer;
  async function openAddQuest() {
    addQuestOpen = !addQuestOpen;
    if (!addQuestOpen) return;
    // Force a catalog fetch: the cache refreshes on a 6h TTL, and a quest an
    // officer imported five minutes ago has to be addable now, not tonight.
    try {
      await RefreshQuestDefs();
    } catch {
      /* offline: the cached catalog still lists */
    }
    try {
      assignable = (await ListAssignableQuests(selected)) || [];
    } catch {
      assignable = [];
    }
  }
  async function addQuest(qid) {
    try {
      await AssignToonQuest(selected, qid);
      addQuestOpen = false;
      addQuestQuery = "";
      await loadQuests(true);
    } catch (e) {
      questsError = String(e);
    }
  }
  // Removal is two clicks on the same control — an epic won't auto-return,
  // so a single misclick mustn't cost one.
  async function removeQuest(qid) {
    if (removeArm !== qid) {
      removeArm = qid;
      clearTimeout(removeArmTimer);
      removeArmTimer = setTimeout(() => (removeArm = 0), 3000);
      return;
    }
    removeArm = 0;
    try {
      await UnassignToonQuest(selected, qid);
      await loadQuests(true);
    } catch (e) {
      questsError = String(e);
    }
  }
  function toggleQuestOpen(qid) {
    questsOpen = { ...questsOpen, [qid]: !questsOpen[qid] };
  }
  $: assignableFiltered = (assignable || []).filter(
    (q) =>
      !addQuestQuery ||
      q.name.toLowerCase().includes(addQuestQuery.toLowerCase()),
  );
  // Held quest items by normalized name, for the "in bags" hints.
  $: heldByName = new Map(
    ((questsData && questsData.held) || []).map((h) => [
      h.name.toLowerCase().replace(/`/g, "'"),
      h,
    ]),
  );
  function heldFor(step) {
    for (const it of step.items || []) {
      if (it.role !== "out") continue;
      const h = heldByName.get((it.name || "").toLowerCase().replace(/`/g, "'"));
      if (h) return h;
    }
    return null;
  }
  const fmtDate = (ms) => (ms ? new Date(ms).toLocaleDateString() : "");

  // The medallion grid, one row per medallion: three piece cells + the
  // turn-in cell that assembles them.
  function medallionRows(meds) {
    const rows = [];
    const at = new Map();
    for (const m of meds || []) {
      if (!at.has(m.rune)) {
        at.set(m.rune, rows.length);
        rows.push({
          rune: m.rune,
          turn_in: m.turn_in,
          rune_held: m.rune_held,
          rune_location: m.rune_location,
          pieces: [],
        });
      }
      rows[at.get(m.rune)].pieces.push(m);
    }
    return rows;
  }
  // Piece holders across the user's own characters, keyed by piece item id —
  // the nine pieces share one name, so only the id-aware lookup can say who
  // holds WHICH piece. Loaded once per pane load; shown as hover titles.
  let medHolders = {};
  async function loadMedHolders() {
    try {
      medHolders = (await WhoHasMedallionPieces()) || {};
    } catch {
      medHolders = {};
    }
  }
  $: if (
    questsData &&
    questsData.assignments.some((a) => a.uses_medallions)
  ) {
    loadMedHolders();
  }
  // The holders line at the bottom of a piece cell: which OTHER of the user's
  // characters hold this exact piece. The selected character's own copy is
  // already the cell's ✓ location line, so they're filtered out; no line at
  // all when nobody else has it.
  function pieceHolders(holders, m) {
    const me = (selected || "").toLowerCase();
    const hs = ((holders || {})[m.item_id] || []).filter(
      (h) => h.char.toLowerCase() !== me,
    );
    if (!hs.length) return "";
    return (
      "Also held by: " +
      hs
        .map(
          (h) =>
            h.char + (h.count > 1 ? ` ×${h.count}` : "") + ` (${h.where})`,
        )
        .join(", ")
    );
  }

  // ── quest step interactivity ──────────────────────────────────────────────
  // The same behaviors the quest editor's walkthrough has: hover an item name
  // for its Magelo stat card, click a say line to copy it, click a loc to drop
  // a map waypoint. Same functions, same feel — the sub-tab IS the walkthrough
  // for players now.

  let qItemCache = {};
  let qTip = null; // { name, item, x, y }
  // "Also held by" — the user's OTHER characters holding the hovered item;
  // the viewed character's own copy already shows as the "in bags" hint.
  let qTipHolders = [];
  let qTipHoldersFor = "";
  $: qTipName = qTip ? qTip.name : "";
  $: if (qTipName !== qTipHoldersFor) loadQTipHolders(qTipName);
  async function loadQTipHolders(name) {
    qTipHoldersFor = name;
    qTipHolders = [];
    if (!name) return;
    try {
      const hits = (await WhoHasItem(name)) || [];
      const rest = hits.filter(
        (h) => h.char.toLowerCase() !== selected.toLowerCase(),
      );
      if (qTipHoldersFor === name) qTipHolders = rest;
    } catch {
      /* the footer is a bonus — a failed lookup shows nothing */
    }
  }
  async function showItemTip(e, name) {
    // Positioned inside the zoomed shell, so cursor coordinates divide by the
    // UI scale or the card drifts at Medium/Large.
    const z = $scale || 1;
    const pad = 14;
    qTip = {
      name,
      item: qItemCache[name] || null,
      x: Math.min(e.clientX / z + pad, window.innerWidth / z - 280),
      y: Math.min(e.clientY / z + pad, window.innerHeight / z - 320),
    };
    if (qItemCache[name] === undefined) {
      try {
        const res = await GetItemByName(name);
        qItemCache[name] = res && res.found ? res.item : null;
      } catch {
        qItemCache[name] = null;
      }
      // Only adopt the result if the cursor is still on the same item.
      if (qTip && qTip.name === name) qTip = { ...qTip, item: qItemCache[name] };
    }
  }
  function moveItemTip(e) {
    if (!qTip) return;
    const z = $scale || 1;
    const pad = 14;
    qTip = {
      ...qTip,
      x: Math.min(e.clientX / z + pad, window.innerWidth / z - 280),
      y: Math.min(e.clientY / z + pad, window.innerHeight / z - 320),
    };
  }
  function hideItemTip() {
    qTip = null;
  }

  // Keyed by quest:step:line rather than by the text itself — the same reply
  // ("Hail", "I am ready") shows up in many quests, and matching on content
  // would light up every copy of it at once instead of the one clicked.
  let copied = "";
  let copiedTimer;
  async function copySay(key, text) {
    try {
      await navigator.clipboard.writeText(text);
      copied = key;
      clearTimeout(copiedTimer);
      copiedTimer = setTimeout(() => (copied = ""), 1400);
    } catch {
      /* clipboard can be refused; the text is on screen to type either way */
    }
  }

  // Clicking a step's loc drops a temporary quest waypoint on that zone's map
  // (questmarkers.go); it retires itself once the zone has been visited and
  // then left. Label prefers the NPC at the spot; a bare ground loc gets the
  // quest name.
  let marked = "";
  let markedTimer;
  async function dropMarker(questName, key, tk) {
    try {
      const label = tk.pt.what !== "here" ? tk.pt.what : questName;
      await AddQuestMarker(tk.name, tk.pt.y, tk.pt.x, label);
      clearTimeout(markedTimer);
      marked = key;
      markedTimer = setTimeout(() => (marked = ""), 2600);
    } catch {
      /* the waypoint is a convenience — the loc is still on screen */
    }
  }

  // ── reactive spell derivations ────────────────────────────────────────────

  // Normalize a spell name for comparison: lowercase and treat backtick as
  // apostrophe (EQ spellbook files use ` for possessives; wiki uses ').
  function normalizeName(n) {
    return n.toLowerCase().replace(/`/g, "'");
  }

  // Set of spell names from the local spellbook file (normalized for comparison).
  // null when no spellbook file exists — disables missing highlighting.
  $: spellbookSet = spellbook ? new Set(spellbook.map(normalizeName)) : null;

  // Spell list grouped by level, highest level first, alpha within each level.
  $: levelGroups = (() => {
    const groups = new Map();
    for (const s of spellList) {
      if (!groups.has(s.level)) groups.set(s.level, []);
      groups.get(s.level).push(s);
    }
    return [...groups.keys()]
      .sort((a, b) => b - a)
      .map((level) => ({ level, spells: groups.get(level) }));
  })();

  $: missingCount = spellbookSet
    ? spellList.filter((s) => !spellbookSet.has(normalizeName(s.name))).length
    : 0;

  function isMissing(spellName) {
    return spellbookSet !== null && !spellbookSet.has(normalizeName(spellName));
  }

  // ── search / highlight ────────────────────────────────────────────────────

  function esc(text) {
    return text
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }

  function rebuildHighlight() {
    if (!rawContent) {
      highlighted = "";
      matchOffsets = [];
      matchIdx = 0;
      return;
    }
    if (!query) {
      highlighted = esc(rawContent);
      matchOffsets = [];
      matchIdx = 0;
      return;
    }
    const lower = rawContent.toLowerCase();
    const lowerQ = query.toLowerCase();
    matchOffsets = [];
    for (let p = 0; ; ) {
      const i = lower.indexOf(lowerQ, p);
      if (i === -1) break;
      matchOffsets.push(i);
      p = i + lowerQ.length;
    }
    if (matchIdx >= matchOffsets.length) matchIdx = 0;
    let html = "",
      last = 0;
    for (let mi = 0; mi < matchOffsets.length; mi++) {
      const s = matchOffsets[mi],
        e = s + query.length;
      html += esc(rawContent.slice(last, s));
      const cls = mi === matchIdx ? "current" : "";
      html += `<mark class="${cls}">${esc(rawContent.slice(s, e))}</mark>`;
      last = e;
    }
    html += esc(rawContent.slice(last));
    highlighted = html;
  }

  async function scrollToCurrent() {
    await tick();
    detailEl
      ?.querySelector("mark.current")
      ?.scrollIntoView({ block: "center", behavior: "smooth" });
  }

  async function handleSearch(e) {
    query = e.target.value;
    // Quick results run in both view modes — the question they answer has
    // nothing to do with which pane is showing.
    scheduleQuickSearch();
    // Table view: highlight matching cells and jump to the first, don't reload.
    if (viewMode === "table") {
      tableMatchIdx = 0;
      refreshTableMarks(true);
      return;
    }
    matchIdx = 0;
    await loadChars(false);
    // Auto-select the top result and jump to the match in the content pane.
    if (query && chars.length) {
      detailTab = "all";
      if (!chars.some((c) => c.name === selected)) {
        await selectChar(chars[0].name);
      }
    }
    rebuildHighlight();
    if (matchOffsets.length) scrollToCurrent();
  }

  function prevMatch() {
    if (!matchOffsets.length) return;
    matchIdx = (matchIdx - 1 + matchOffsets.length) % matchOffsets.length;
    rebuildHighlight();
    scrollToCurrent();
  }

  function nextMatch() {
    if (!matchOffsets.length) return;
    matchIdx = (matchIdx + 1) % matchOffsets.length;
    rebuildHighlight();
    scrollToCurrent();
  }

  // ── inventory ─────────────────────────────────────────────────────────────

  function wikiLink(name) {
    return "https://wiki.project1999.com/" + name.replace(/ /g, "_");
  }

  $: visibleInventory = query
    ? inventoryItems.filter(
        (it) =>
          it.name.toLowerCase().includes(query.toLowerCase()) ||
          it.location.toLowerCase().includes(query.toLowerCase()),
      )
    : inventoryItems;

  // ── context menu ──────────────────────────────────────────────────────────

  // Click: plain = single-select + load detail; CTRL/CMD = toggle in the
  // multi-selection (no detail load, so building a big selection is cheap).
  function onCharClick(e, name) {
    if (e.ctrlKey || e.metaKey) {
      const s = new Set(selectedSet);
      s.has(name) ? s.delete(name) : s.add(name);
      selectedSet = s;
      return;
    }
    selectedSet = new Set([name]);
    selectChar(name);
  }

  function selectAll() {
    selectedSet = new Set(chars.map((c) => c.name));
  }
  function clearSelection() {
    selectedSet = new Set();
  }

  function onKeydown(e) {
    if (e.key === "Escape") {
      closeCtx();
      return;
    }
    // CTRL/CMD+A selects all displayed toons — but only when focus is in the
    // character list, so it doesn't hijack text selection in the search box or
    // the detail pane.
    if ((e.ctrlKey || e.metaKey) && (e.key === "a" || e.key === "A")) {
      if (listEl && listEl.contains(document.activeElement)) {
        e.preventDefault();
        selectAll();
      }
    }
  }

  async function onRightClick(e, name) {
    e.preventDefault();
    // The menu lives inside .shell (CSS zoom:$scale), which scales its coordinate
    // space — divide the viewport cursor coords by the zoom so it lands at the cursor.
    const x = e.clientX / $scale,
      y = e.clientY / $scale;
    // Right-clicking inside a multi-selection acts on the whole set; otherwise
    // it selects just this toon and shows the single-toon menu.
    if (selectedSet.size > 1 && selectedSet.has(name)) {
      ctx = {
        visible: true,
        x,
        y,
        name,
        filtered: false,
        batch: true,
        count: selectedSet.size,
      };
      return;
    }
    selectedSet = new Set([name]);
    const filtered = await IsFilteredToon(name);
    ctx = { visible: true, x, y, name, filtered, batch: false, count: 1 };
  }

  function closeCtx() {
    ctx = { ...ctx, visible: false };
  }

  async function toggleFilter() {
    await ToggleFilteredToon(ctx.name);
    closeCtx();
    await loadChars();
  }

  async function filterSelection(filtered) {
    await SetFilteredToons([...selectedSet], filtered);
    clearSelection();
    closeCtx();
    await loadChars();
  }

  // ── clipboard commands ────────────────────────────────────────────────────

  let copyMsg = "";
  let copyTimer;

  function copyCommand(cmd) {
    navigator.clipboard.writeText(cmd);
    copyMsg = `Command copied to clipboard — ${cmd}`;
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (copyMsg = ""), 3000);
  }

  // ── lifecycle ─────────────────────────────────────────────────────────────

  onMount(async () => {
    const s = await GetSettings();
    excludeBots = s.exclude_bots ?? true;
    excludeFiltered = s.exclude_filtered ?? true;
    isAdmin = await IsAdminMode();
    try {
      isOfficer = await IsOfficer();
    } catch {
      isOfficer = false;
    }
    await loadChars(true);
    window.addEventListener("click", closeCtx);
    return () => window.removeEventListener("click", closeCtx);
  });

  // After adding a spell, drop the cached list so the Spells tab re-fetches
  // (the new spell then shows for its class).
  function onSpellAdded() {
    spellsLoaded = "";
    if (detailTab === "spells") loadSpellList();
  }

  async function onExcludeChange() {
    const s = await GetSettings();
    await SaveSettings({
      ...s,
      exclude_bots: excludeBots,
      exclude_filtered: excludeFiltered,
    });
    loadChars(true);
    if (viewMode === "table") loadTable();
    // The filters decide which characters the search may report from, so a
    // stale panel would point at a bot the user just hid.
    scheduleQuickSearch();
  }
</script>

<svelte:window on:keydown={onKeydown} />

<div class="chars">
  <!-- toolbar -->
  <div class="toolbar">
    <div class="search-row">
      <input
        class="search"
        type="text"
        placeholder={viewMode === "table"
          ? "Search…"
          : "Search name, inventory, spells…"}
        value={query}
        on:input={handleSearch}
      />
      {#if viewMode === "table"}
        {#if query && tableMatchCount}
          <span class="match-info">{tableMatchIdx + 1}/{tableMatchCount}</span>
          <button class="nav" on:click={prevTableMatch} title="Previous"
            >↑</button
          >
          <button class="nav" on:click={nextTableMatch} title="Next">↓</button>
        {:else if query}
          <span class="match-info">0/0</span>
        {/if}
      {:else if detailTab === "all" && matchOffsets.length}
        <span class="match-info">{matchIdx + 1}/{matchOffsets.length}</span>
        <button class="nav" on:click={prevMatch} title="Previous">↑</button>
        <button class="nav" on:click={nextMatch} title="Next">↓</button>
      {/if}
      <button
        class="nav view-toggle"
        class:active={viewMode === "table"}
        on:click={toggleView}
        title={viewMode === "table" ? "List view" : "Table view"}
      >
        <svg
          width="13"
          height="13"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          stroke-width="1.3"
        >
          <rect x="1.5" y="2.5" width="13" height="11" rx="1" />
          <line x1="1.5" y1="6" x2="14.5" y2="6" />
          <line x1="1.5" y1="10" x2="14.5" y2="10" />
          <line x1="6" y1="2.5" x2="6" y2="13.5" />
        </svg>
      </button>
    </div>
    <div class="filter-row">
      <label class="chk">
        <input
          type="checkbox"
          bind:checked={excludeBots}
          on:change={onExcludeChange}
        />
        Exclude Bots<span class="dot dot-bot"></span>
      </label>
      <label class="chk">
        <input
          type="checkbox"
          bind:checked={excludeFiltered}
          on:change={onExcludeChange}
        />
        Exclude Filtered<span class="dot dot-filtered"></span>
      </label>
      <span
        class="sel-tools"
        title="Ctrl+click to multi-select, Ctrl+A to select all, then right-click for Filter/Unfilter All"
      >
        <!-- <button class="sel-btn" on:click={selectAll}>Select All</button> -->
        {#if selectedSet.size > 1}
          <!-- <button class="sel-btn" on:click={clearSelection}>Clear</button> -->
          <span class="sel-count">{selectedSet.size} selected</span>
        {/if}
      </span>
      <button
        class="lib-btn"
        disabled={!selected}
        title={selected
          ? "Browse magelos shared by the guild"
          : "Select a character first"}
        on:click={() => {
          viewMode = "detail";
          detailTab = "magelo";
          libraryOpenReq++;
        }}>🌐 Fuse Shared Magelos</button
      >
    </div>
  </div>

  <!-- quick results: where every matching item and spell actually is, across
       all visible characters. Sits between the controls and the listing so it
       reads as an answer to what was just typed. -->
  {#if quickVisible}
    <div class="quick" transition:slide|local={{ duration: 150 }}>
      <div class="quick-head">
        <button
          class="quick-toggle"
          on:click={() => (quickOpen = !quickOpen)}
          title={quickOpen ? "Collapse results" : "Expand results"}
        >
          <span class="chev" class:open={quickOpen}>▸</span>Results
        </button>
        <span class="quick-count">
          {#if quickLoading}
            searching…
          {:else if !quickRows.length}
            nothing matches in any inventory or spellbook
          {:else}
            {quickTotal}
            {quickTotal === 1 ? "match" : "matches"} across {quickGroups.length}
            {quickGroups.length === 1 ? "name" : "names"}{#if quickTruncated}
              <span class="quick-trunc">— showing first {quickRows.length}</span>
            {/if}
          {/if}
        </span>
      </div>
      <!-- The slide belongs here, not just on .quick: the outer block is created
           the moment the query hits 2 chars, when it is only the header strip,
           so its transition animates ~24px and reads as an instant pop. This is
           the block that actually has height, and it appears a round-trip later
           when the rows land. Also animates the collapse toggle. -->
      {#if quickOpen && quickRows.length}
        <div class="quick-wrap" transition:slide|local={{ duration: 150 }}>
          <table class="char-table quick-table">
            <thead>
              <tr>
                <th>Item</th>
                <th>Character</th>
                <th>Where</th>
                <th>Location</th>
                <th class="num">Qty</th>
              </tr>
            </thead>
            <tbody>
              <!-- The name repeats on every row rather than being shown once
                   per run: the panel scrolls, and a blank name cell three rows
                   into a scrolled view answers nothing. -->
              {#each quickRows as r, i (r.name + "|" + r.char + "|" + r.where + "|" + r.location + "|" + i)}
                <tr on:click={() => pickQuick(r.char)}>
                  <td class="c-item">{r.name}</td>
                  <td class="c-name">{r.char}</td>
                  {#if r.kind === "spell"}
                    <!-- The spellbook file records a page position that
                         reshuffles as spells are scribed, so level is the
                         only durable detail there is. -->
                    <td class="c-spell" colspan="3">
                      Spellbook{#if r.level}
                        ({r.level}){/if}
                    </td>
                  {:else}
                    <td>{r.where}</td>
                    <td>{r.location}</td>
                    <td class="num" class:one={r.count <= 1}>{r.count || 1}</td>
                  {/if}
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  {/if}

  <!-- table view -->
  {#if viewMode === "table"}
    <div class="table-wrap" bind:this={tableEl}>
      {#if tableLoading}
        <div class="empty">Loading…</div>
      {:else if !sortedRows.length}
        <div class="empty">No characters</div>
      {:else}
        <table class="char-table">
          <thead>
            <tr>
              {#each textCols as c}
                <th
                  rowspan="2"
                  class="sortable"
                  class:sorted={sortKey === c.key}
                  class:num={c.num}
                  on:click={() => sortBy(c.key)}
                >
                  {c.label}{#if sortKey === c.key}<span class="arrow"
                      >{sortDir === 1 ? "▲" : "▼"}</span
                    >{/if}
                </th>
              {/each}
              <th class="keys-group" colspan={keyCols.length}>Keys</th>
              {#each itemGroups as g}
                <th class="keys-group" colspan={g.cols.length}>{g.title}</th>
              {/each}
            </tr>
            <tr>
              {#each keyCols as c}
                <th
                  class="sortable key-th"
                  class:sorted={sortKey === c.key}
                  on:click={() => sortBy(c.key)}
                  title={keyTitle[c.key]}
                >
                  {c.label}{#if sortKey === c.key}<span class="arrow"
                      >{sortDir === 1 ? "▲" : "▼"}</span
                    >{/if}
                </th>
              {/each}
              {#each itemGroups as g}
                {#each g.cols as c}
                  <th
                    class="sortable key-th"
                    class:sorted={sortKey === c.key}
                    on:click={() => sortBy(c.key)}
                    title={c.title}
                  >
                    {c.label}{#if sortKey === c.key}<span class="arrow"
                        >{sortDir === 1 ? "▲" : "▼"}</span
                      >{/if}
                  </th>
                {/each}
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each sortedRows as row (row.name)}
              <tr
                class:sel={row.name === selected}
                on:click={() => selectFromTable(row.name)}
              >
                <td class="c-name" title={charTitle(row)}
                  >{@html hlCell(row.name, query)}</td
                >
                <td class="c-zone" title={fmtUpdated(row.zone_updated)}
                  >{@html hlCell(row.zone, query)}</td
                >
                <td class="c-bind" title={fmtUpdated(row.bind_updated)}
                  >{@html hlCell(row.bind, query)}</td
                >
                {#each keyCols as c}
                  <td class="key-cell" class:has={row.keys && row.keys[c.key]}
                    >{row.keys && row.keys[c.key] ? "✓" : ""}</td
                  >
                {/each}
                {#each itemGroups as g}
                  {#each g.cols as c}
                    {@const n = (row.items && row.items[c.key]) || 0}
                    <td class="key-cell" class:has={n > 0} title={c.title}>
                      {#if n > 1}<span class="cnt-badge">{n}</span
                        >{:else if n === 1}✓{/if}
                    </td>
                  {/each}
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
  {:else}
    <!-- split pane -->
    <div class="split">
      <div class="list" bind:this={listEl} tabindex="-1">
        {#each chars as entry}
          {@const meta = charMeta(entry.name, charInfos)}
          {@const zone = charZone(entry.name, charInfos)}
          <div
            class="char-item"
            class:sel={selectedSet.has(entry.name) || entry.name === selected}
            role="button"
            tabindex="0"
            on:click={(e) => onCharClick(e, entry.name)}
            on:keydown={(e) => e.key === "Enter" && onCharClick(e, entry.name)}
            on:contextmenu={(e) => onRightClick(e, entry.name)}
          >
            <div class="char-row">
              <span class="char-name"
                >{entry.name}{#if query && entry.match_count > 0}<span
                    class="match-badge">({entry.match_count})</span
                  >{/if}{#if !excludeBots && entry.is_bot}<span
                    class="dot dot-bot"
                    title="Bot"
                  ></span>{/if}{#if !excludeFiltered && entry.is_filtered}<span
                    class="dot dot-filtered"
                    title="Filtered"
                  ></span>{/if}</span
              >
              {#if meta}<span class="char-meta">{meta}</span>{/if}
            </div>
            {#if zone}<div class="char-zone">{zone}</div>{/if}
          </div>
        {:else}
          <div class="empty">No characters</div>
        {/each}
      </div>

      <div class="detail-pane">
        {#if selected}
          <!-- sub-tab bar -->
          <div class="sub-tabs">
            <!-- Magelo: released to everyone (item data needs a linked client;
               the view surfaces the link prompt itself when unlinked). -->
            <button
              class="sub-tab"
              class:active={detailTab === "magelo"}
              on:click={() => (detailTab = "magelo")}>Magelo</button
            >

            <button
              class="sub-tab"
              class:active={detailTab === "quests"}
              on:click={openQuestsTab}
            >
              Quests{#if questsLoaded === selected && questsData && questsData.assignments.length > 0}
                <span class="tab-count">({questsData.assignments.length})</span>
              {/if}
            </button>

            <button
              class="sub-tab"
              class:active={detailTab === "inventory"}
              on:click={() => (detailTab = "inventory")}
            >
              Inventory{#if inventoryItems.length > 0}<span class="tab-count"
                  >({inventoryItems.length})</span
                >{/if}
            </button>

            {#if showSpellsTab}
              <button
                class="sub-tab"
                class:active={detailTab === "spells"}
                on:click={openSpellsTab}
              >
                Spells{#if charClassLoading}
                  <span class="tab-loading">…</span>
                {:else if spellsLoaded === selected && spellList.length > 0}
                  <span class="tab-count" class:tab-missing={missingCount > 0}>
                    {#if missingCount > 0}({missingCount} missing){:else}(✓){/if}
                  </span>
                {/if}
              </button>
            {/if}

            <button
              class="sub-tab"
              class:active={detailTab === "all"}
              on:click={() => (detailTab = "all")}>All</button
            >
          </div>

          <!-- All tab -->
          {#if detailTab === "all"}
            <div class="detail" bind:this={detailEl}>
              <pre class="pre">{@html highlighted}</pre>
            </div>

            <!-- Inventory tab -->
          {:else if detailTab === "inventory"}
            <div class="detail">
              {#if visibleInventory.length === 0}
                <div class="empty">
                  {inventoryItems.length === 0
                    ? "No inventory file found"
                    : "No items match"}
                </div>
              {:else}
                <table class="inv-table">
                  <thead>
                    <tr>
                      <th class="col-slot">Slot</th>
                      <th class="col-item">Item</th>
                      <th class="col-count">#</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each visibleInventory as item}
                      <tr class:dim={item.count === 1}>
                        <td class="col-slot slot-label">{item.location}</td>
                        <td class="col-item">
                          <a
                            class="wiki-link"
                            href={wikiLink(item.name)}
                            target="_blank"
                            rel="noreferrer">{item.name}</a
                          >
                        </td>
                        <td class="col-count"
                          >{#if item.count > 1}<span class="stack"
                              >{item.count}</span
                            >{/if}</td
                        >
                      </tr>
                    {/each}
                  </tbody>
                </table>
              {/if}
            </div>

            <!-- Spells tab -->
          {:else if detailTab === "spells"}
            <div class="detail spells-pane">
              {#if charClassLoading}
                <div class="empty">Identifying class…</div>
              {:else if !charClass}
                <div class="empty">
                  Class unknown or missing spellbook file — log in and run <code
                    >/who</code
                  >
                  to set the class and run <code>/outputfile spellbook</code>.
                </div>
              {:else if spellsLoading}
                <div class="empty">Loading spells…</div>
              {:else if spellsError}
                <div class="empty">{spellsError}</div>
              {:else if spellList.length === 0}
                <div class="empty">No spells found for {charClass}</div>
              {:else}
                <!-- summary bar -->
                <div class="spell-summary">
                  <span class="spell-class">{charClass}</span>
                  <span class="spell-count-info">
                    {#if spellbook === null}
                      · {spellList.length} spells ·
                      <span class="no-sb"
                        >No spellbook file — run <code
                          >/outputfile spellbook</code
                        ></span
                      >
                    {:else}
                      · {spellList.length - missingCount}/{spellList.length} known
                      {#if missingCount > 0}<span class="missing-badge"
                          >{missingCount} missing</span
                        >{/if}
                    {/if}
                  </span>
                </div>

                <!-- spell list grouped by level (highest first) -->
                <div class="spell-list">
                  {#each levelGroups as group}
                    <div class="spell-level-header">Level {group.level}</div>
                    {#each group.spells as spell}
                      {@const missing = isMissing(spell.name)}
                      <div class="spell-row" class:spell-missing={missing}>
                        <div class="spell-name-col">
                          {#if spell.wiki_url}
                            <a
                              class="spell-link"
                              class:spell-link-missing={missing}
                              href={spell.wiki_url}
                              target="_blank"
                              rel="noreferrer"
                            >
                              {spell.name}
                            </a>
                          {:else}
                            <span
                              class="spell-link"
                              class:spell-link-missing={missing}
                              >{spell.name}</span
                            >
                          {/if}
                        </div>
                        <div class="spell-desc-col">
                          {#if spell.description}
                            <span class="spell-desc">{spell.description}</span>
                          {/if}
                        </div>
                        <div class="spell-stat-col">
                          {#if charClass === "Bard"}
                            {#if spell.spell_type}<span class="spell-stat"
                                >{spell.spell_type}</span
                              >{/if}
                          {:else if spell.mana > 0}<span class="spell-stat"
                              >{spell.mana}m</span
                            >{/if}
                        </div>
                      </div>
                    {/each}
                  {/each}
                </div>
              {/if}
            </div>

            <!-- Quests tab: per-character assignment + tracking -->
          {:else if detailTab === "quests"}
            <div class="detail quests-pane">
              {#if questsLoading && !questsData}
                <div class="empty">Loading quests…</div>
              {:else if questsError}
                <div class="empty">{questsError}</div>
              {:else if questsData && !questsData.catalog_ok}
                <div class="empty">
                  Quest catalog not downloaded yet — the app fetches it whenever
                  it can reach the server.
                </div>
              {:else if questsData}
                <div class="q-toolbar">
                  <button class="q-add" on:click={openAddQuest}
                    >{addQuestOpen ? "close" : "+ Add quest"}</button
                  >
                  {#if questsData.inv_as_of_ms}
                    <span class="q-asof"
                      >inventory as of {new Date(
                        questsData.inv_as_of_ms,
                      ).toLocaleString()}</span
                    >
                  {:else}
                    <span class="q-asof"
                      >no inventory dump — run <code
                        >/outputfile inventory</code
                      > to enable auto-tracking</span
                    >
                  {/if}
                </div>

                {#if addQuestOpen}
                  <div class="q-addpanel" transition:slide|local={{ duration: 120 }}>
                    <input
                      class="q-addsearch"
                      placeholder="Filter quests…"
                      bind:value={addQuestQuery}
                    />
                    <div class="q-addlist">
                      {#each assignableFiltered as q (q.id)}
                        <button class="q-addrow" on:click={() => addQuest(q.id)}>
                          <span>{q.name}</span>
                          {#if q.class}<span class="q-class">{q.class}</span>{/if}
                        </button>
                      {:else}
                        <div class="empty">No matching quests</div>
                      {/each}
                    </div>
                  </div>
                {/if}

                {#if questsData.assignments.length === 0}
                  <div class="empty">
                    No quests assigned. The class epic is added automatically
                    once the class is known; anything else via “Add quest”.
                  </div>
                {/if}
                {#each questsData.assignments as a (a.quest.id)}
                  <div class="q-card">
                    <div class="q-headrow">
                      <button
                        class="q-head"
                        on:click={() => toggleQuestOpen(a.quest.id)}
                      >
                        <span class="q-name">{a.quest.name}</span>
                        {#if a.source === "auto"}<span
                            class="q-autobadge"
                            title="Auto-assigned class epic">auto</span
                          >{/if}
                        {#if a.quest.class}<span class="q-class">{a.quest.class}</span
                          >{/if}
                        <span class="q-progress"
                          >{a.done_count}/{a.quest.steps.length}</span
                        >
                      </button>
                      <!-- Delete without expanding. Two clicks on purpose — a
                           removed epic never auto-returns, so one misclick
                           mustn't cost it. -->
                      <button
                        class="q-del"
                        class:armed={removeArm === a.quest.id}
                        title={removeArm === a.quest.id
                          ? "Click again to remove this quest"
                          : `Remove ${a.quest.name} from ${selected}'s list`}
                        on:click={() => removeQuest(a.quest.id)}
                        >{removeArm === a.quest.id ? "remove?" : "×"}</button
                      >
                    </div>
                    {#if questsOpen[a.quest.id]}
                      <div class="q-body" transition:slide|local={{ duration: 120 }}>
                        <div class="q-meta">
                          started {fmtDate(a.started_ms)}
                          {#if a.quest.wiki_url}
                            · <a
                              class="wiki-link"
                              href={a.quest.wiki_url}
                              target="_blank"
                              rel="noreferrer">wiki</a
                            >
                          {/if}
                        </div>
                        {#if a.quest.prereqs && a.quest.prereqs.length}
                          <div class="q-prereq">
                            Requires first: {a.quest.prereqs
                              .map((p) => p.name)
                              .join(", ")}
                          </div>
                        {/if}
                        {#each a.quest.steps as s, i}
                          {@const st = a.steps[i]}
                          {@const held = heldFor(s)}
                          <div class="q-step" class:done={st && st.done}>
                            <input
                              type="checkbox"
                              checked={st && st.done}
                              on:change={(e) =>
                                toggleStep(
                                  a.quest.id,
                                  i,
                                  e.currentTarget.checked,
                                )}
                            />
                            <span class="q-stepnum">{i + 1}.</span>
                            <div class="q-steptext">
                              <span class="q-stepkind">{kindLabel(s)}</span>
                              {#each stepLine(s) as tk}
                                {#if tk.t === "text" || tk.t === "sep"}<span
                                    class="q-dim"
                                  >
                                    {tk.s}
                                  </span>
                                {:else if tk.t === "item"}<button
                                    class="q-item"
                                    on:mouseenter={(e) =>
                                      showItemTip(e, tk.name)}
                                    on:mousemove={moveItemTip}
                                    on:mouseleave={hideItemTip}
                                    >{tk.name}</button
                                  >
                                {:else if tk.t === "zone"}<span class="q-zone"
                                    >{tk.name}</span
                                  >
                                  {#if tk.pt}
                                    <button
                                      class="q-locbtn"
                                      title="Drop a map waypoint at {tk.pt
                                        .y}, {tk.pt.x} in {tk.name} — it clears
                                      itself after you've been there and zone
                                      out or camp"
                                      on:click|stopPropagation={() =>
                                        dropMarker(
                                          a.quest.name,
                                          `${a.quest.id}:${i}`,
                                          tk,
                                        )}
                                      >⚑ {tk.pt.y}, {tk.pt.x}</button
                                    >
                                    {#if marked === `${a.quest.id}:${i}`}
                                      <span class="q-flash">waypoint set</span>
                                    {/if}
                                  {/if}
                                {:else if tk.t === "mob"}<span class="q-mob"
                                    >{tk.names.join(" / ")}</span
                                  >
                                {:else if tk.t === "say"}<span class="q-says">
                                    {#each sayLines(s.say) as line, sk (sk)}
                                      {#if sk > 0}<span class="q-dim">→</span
                                        >{/if}
                                      <button
                                        class="q-say"
                                        class:q-said={copied ===
                                          `${a.quest.id}:${i}:${sk}`}
                                        title="Click to copy"
                                        on:click|stopPropagation={() =>
                                          copySay(
                                            `${a.quest.id}:${i}:${sk}`,
                                            line,
                                          )}
                                        >“{line}”</button
                                      >
                                    {/each}
                                    {#if copied.startsWith(`${a.quest.id}:${i}:`)}
                                      <span class="q-flash">copied</span>
                                    {/if}
                                  </span>
                                {:else if tk.t === "plat"}<span class="q-plat"
                                    >{tk.n}pp</span
                                  >
                                {:else if tk.t === "skill"}<span class="q-skill"
                                    >[{tk.s}]</span
                                  >
                                {:else if tk.t === "gate"}<span class="q-gate"
                                    >({tk.s})</span
                                  >
                                {:else if tk.t === "mult"}<span class="q-dim"
                                    >×{tk.n}</span
                                  >
                                {:else if tk.t === "ret"}<span class="q-dim"
                                    >(returned)</span
                                  >
                                {/if}
                              {/each}
                              {#if held && !(st && st.done)}
                                <span
                                  class="q-held"
                                  title="Found in this character's inventory dump"
                                  >in bags: {held.location}</span
                                >
                              {/if}
                              {#if autoTicked === `${a.quest.id}:${i}`}
                                <span class="q-autotick"
                                  >+{autoTickedN} earlier step{autoTickedN === 1
                                    ? ""
                                    : "s"}</span
                                >
                              {/if}
                              {#if s.note}
                                <div class="q-stepnote">{s.note}</div>
                              {/if}
                            </div>
                            {#if st && st.done && st.at_ms}
                              <span class="q-stepwhen" title={st.detail || ""}
                                >{fmtDate(st.at_ms)}{st.source
                                  ? ` · ${st.source}`
                                  : ""}</span
                              >
                            {/if}
                          </div>
                        {/each}

                        {#if a.quest.rewards && a.quest.rewards.length}
                          <div class="q-rewardline">
                            <span class="q-dim">Reward</span>
                            {#each a.quest.rewards as r, k}
                              {#if k > 0}<span class="q-dim">·</span>{/if}
                              {#if r.kind === "item"}
                                <button
                                  class="q-item"
                                  on:mouseenter={(e) => showItemTip(e, r.name)}
                                  on:mousemove={moveItemTip}
                                  on:mouseleave={hideItemTip}>{r.name}</button
                                >
                              {:else if r.kind === "cycle"}
                                {#each r.cycle || [] as c, ci}
                                  {#if ci > 0}<span class="q-dim">→</span>{/if}
                                  <button
                                    class="q-item"
                                    on:mouseenter={(e) => showItemTip(e, c)}
                                    on:mousemove={moveItemTip}
                                    on:mouseleave={hideItemTip}>{c}</button
                                  >
                                {/each}
                              {:else}
                                <span class="q-dim">{rewardText(r)}</span>
                              {/if}
                            {/each}
                          </div>
                        {/if}

                        <!-- Medallion grid, inside the quest's own card like
                             any other detail: one row per medallion — its
                             three pieces plus the turn-in that assembles it.
                             A held piece keeps its source/zone lines (the ✓
                             says you have it, not where it came from), and the
                             bottom line names which OTHER of your characters
                             hold this exact piece (id-aware; the nine share
                             one name). -->
                        {#if a.uses_medallions && questsData.medallions.length}
                          <div class="q-section">Medallion Pieces</div>
                          <div class="q-medgrid">
                            {#each medallionRows(questsData.medallions) as row (row.rune)}
                              {#each row.pieces as m (m.item_id)}
                                {@const holdLine = pieceHolders(medHolders, m)}
                                <div class="q-med" class:held={m.held}>
                                  <div class="q-medname">
                                    {m.rune.replace("Medallion of the ", "")} —
                                    {m.piece}
                                  </div>
                                  {#if m.held}
                                    <div class="q-medloc">✓ {m.location}</div>
                                  {/if}
                                  <div class="q-medsrc">{m.source}</div>
                                  <div class="q-medzone">{m.zone}</div>
                                  {#if holdLine}
                                    <div class="q-medhold">{holdLine}</div>
                                  {/if}
                                </div>
                              {/each}
                              <!-- The completed medallion in the bags turns
                                   this cell green and names the item + slot;
                                   the turn-in description stays either way —
                                   holding the medallion is exactly when you
                                   need to know where it goes. -->
                              <div class="q-med q-medturn" class:held={row.rune_held}>
                                {#if row.rune_held}
                                  <div class="q-medname">✓ {row.rune}</div>
                                  <div class="q-medloc">{row.rune_location}</div>
                                {:else}
                                  <div class="q-medname">Turn in</div>
                                {/if}
                                <div class="q-medsrc">{row.turn_in}</div>
                              </div>
                            {/each}
                          </div>
                        {/if}
                      </div>
                    {/if}
                  </div>
                {/each}

                {#if questsData.completions.length}
                  <div class="q-section">Completed Quests</div>
                  {#each questsData.completions as c (c.quest_id)}
                    <div class="q-comp">
                      <span class="q-name">{c.quest_name}</span>
                      <button
                        class="q-compitem"
                        on:mouseenter={(e) => showItemTip(e, c.item_name)}
                        on:mousemove={moveItemTip}
                        on:mouseleave={hideItemTip}>{c.item_name}</button
                      >
                      <span class="q-compdate">{fmtDate(c.at_ms)}</span>
                    </div>
                  {/each}
                  <div class="q-mednote">
                    Detected from reward items in inventory — a completed quest
                    can still be re-added above.
                  </div>
                {/if}
              {/if}
            </div>

            <!-- Magelo tab (officers): wiki-Magelo-style character sheet -->
          {:else if detailTab === "magelo"}
            <div class="detail mg-pane">
              {#key selected}
                <MageloView
                  inventory={inventoryItems}
                  charName={selected}
                  info={charInfos[selected.toLowerCase()]}
                  {isAdmin}
                  allChars={chars.map((c) => ({
                    name: c.name,
                    class: (charInfos[c.name.toLowerCase()] || {}).class || "",
                  }))}
                  {libraryOpenReq}
                />
              {/key}
            </div>
          {/if}
        {:else}
          <div class="detail">
            <div class="empty">Select a character</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- command footer -->
  <div class="cmd-footer">
    <div class="cmd-buttons">
      <button
        class="cmd-btn"
        on:click={() => copyCommand("/outputfile inventory")}
        >Copy Inventory Command</button
      >
      <button
        class="cmd-btn"
        on:click={() => copyCommand("/outputfile spellbook")}
        >Copy Spellbook Command</button
      >
    </div>
    {#if copyMsg}
      <span class="copy-msg">{copyMsg}</span>
    {/if}
    {#if isAdmin}
      <button
        class="cmd-btn admin-add-spell"
        on:click={() => (showAddSpell = true)}>+ Add Missing Spell</button
      >
    {/if}
  </div>
</div>

{#if showAddSpell}
  <AddSpellModal
    on:close={() => (showAddSpell = false)}
    on:added={onSpellAdded}
  />
{/if}

<!-- Item card on hover — the same card the quest editor and Magelo sheet show. -->
{#if qTip}
  <div class="q-tip" style="left:{qTip.x}px;top:{qTip.y}px">
    <div class="q-tip-name">{qTip.name}</div>
    {#if qTip.item}
      {#each tipStats(qTip.item) as l}{#if l === TIP_RULE}<div
            class="q-tip-rule"
          ></div>{:else}<div class="q-tip-line">{l}</div>{/if}{/each}
    {:else}
      <div class="q-tip-line q-tip-dim">Not in the item DB yet.</div>
    {/if}
    {#if qTipHolders.length}
      <div class="q-tip-rule"></div>
      <div
        class="q-tip-line q-tip-dim"
        title={qTipHolders.map((h) => `${h.char}: ${h.where}`).join("\n")}
      >
        Also held by: {qTipHolders
          .map((h) => h.char + (h.count > 1 ? ` ×${h.count}` : ""))
          .join(", ")}
      </div>
    {/if}
  </div>
{/if}

<!-- context menu -->
{#if ctx.visible}
  <div
    class="ctx-menu"
    style="left:{ctx.x}px;top:{ctx.y}px"
    on:click|stopPropagation
  >
    {#if ctx.batch}
      <button class="ctx-item" on:click={() => filterSelection(true)}
        >Filter All ({ctx.count})</button
      >
      <button class="ctx-item" on:click={() => filterSelection(false)}
        >Unfilter All ({ctx.count})</button
      >
    {:else}
      <button class="ctx-item" on:click={toggleFilter}>
        {ctx.filtered ? "Unfilter" : "Filter"}
        {ctx.name}
      </button>
    {/if}
  </div>
{/if}

<style>
  .chars {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }

  /* toolbar */
  .toolbar {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
    flex-shrink: 0;
  }
  .search-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .search {
    flex: 1;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 5px 9px;
    outline: none;
  }
  .search:focus {
    border-color: var(--accent-dim);
  }
  .match-info {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .nav {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 2px 8px;
  }
  .nav:hover {
    border-color: var(--accent-dim);
    color: var(--accent);
  }

  .filter-row {
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .chk {
    display: flex;
    align-items: center;
    gap: 5px;
    cursor: pointer;
    font-size: 12px;
    color: var(--text-secondary);
  }
  .chk input {
    accent-color: var(--accent);
  }

  .sel-tools {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-left: auto;
  }
  .lib-btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    font-size: 11.5px;
    padding: 2px 10px;
    cursor: pointer;
    white-space: nowrap;
  }
  .lib-btn:hover:not(:disabled) {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .lib-btn:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .sel-btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 3px 9px;
    transition:
      border-color 0.15s,
      color 0.15s;
  }
  .sel-btn:hover {
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .sel-count {
    font-size: 11px;
    color: var(--accent);
  }

  /* split pane */
  .split {
    display: flex;
    flex: 1;
    overflow: hidden;
  }

  .list {
    width: 180px;
    min-width: 180px;
    overflow-y: auto;
    border-right: 1px solid var(--border);
    background: var(--bg-panel);
  }
  .char-item {
    padding: 6px 12px;
    cursor: pointer;
    font-size: 12px;
    color: var(--text-secondary);
    transition:
      background 0.1s,
      color 0.1s;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .char-item:hover {
    background: rgba(255, 255, 255, 0.04);
    color: var(--text-primary);
  }
  .char-item.sel {
    background: rgba(200, 169, 81, 0.12);
    color: var(--accent);
  }
  .char-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .char-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 600;
    color: var(--text-primary);
  }
  .char-meta {
    margin-left: auto;
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .char-zone {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    color: var(--accent);
  }
  .match-badge {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: 4px;
  }

  /* status dots — bot (blue) and filtered (yellow) */
  .dot {
    display: inline-block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    flex-shrink: 0;
    vertical-align: middle;
  }
  .dot-bot {
    background: #4a9eff;
    margin-left: 5px;
  }
  .dot-filtered {
    background: #c8a951;
    margin-left: 5px;
  }

  /* detail pane */
  .detail-pane {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
  }

  /* sub-tabs */
  .sub-tabs {
    display: flex;
    gap: 0;
    flex-shrink: 0;
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
    padding: 0 12px;
  }
  .sub-tab {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 12px;
    padding: 7px 12px 6px;
    transition:
      color 0.15s,
      border-color 0.15s;
    margin-bottom: -1px;
  }
  .sub-tab:hover {
    color: var(--text-primary);
  }
  .sub-tab.active {
    color: var(--accent);
    border-bottom-color: var(--accent);
  }
  .tab-count {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: 4px;
  }
  .tab-missing {
    color: #e05c5c;
  }
  .tab-loading {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: 3px;
  }

  .detail {
    flex: 1;
    overflow: auto;
    padding: 10px 14px;
  }
  .pre {
    font-size: 13px;
    color: var(--text-secondary);
    line-height: 1.6;
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  /* Magelo pane: the view manages its own padding/scroll */
  .mg-pane {
    padding: 0;
    display: flex;
  }

  /* inventory table */
  .inv-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .inv-table thead th {
    text-align: left;
    color: var(--text-muted);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    padding: 6px 10px 6px 0;
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    background: var(--bg-primary);
  }
  .inv-table tbody tr {
    border-bottom: 1px solid rgba(37, 40, 54, 0.6);
  }
  .inv-table tbody tr:hover {
    background: rgba(255, 255, 255, 0.03);
  }
  .inv-table td {
    padding: 5px 10px 5px 0;
    vertical-align: middle;
  }

  .col-slot {
    width: 110px;
  }
  .col-count {
    width: 36px;
    text-align: right;
    padding-right: 4px;
  }
  .slot-label {
    color: var(--text-muted);
    font-size: 11px;
  }

  .wiki-link {
    color: var(--text-primary);
    text-decoration: none;
    transition: color 0.12s;
  }
  a.wiki-link:hover {
    color: var(--accent);
    text-decoration: underline;
  }

  .stack {
    background: rgba(200, 169, 81, 0.15);
    color: var(--accent);
    border-radius: 3px;
    font-size: 11px;
    padding: 1px 5px;
  }

  .empty {
    padding: 40px 20px;
    color: var(--text-muted);
    font-size: 12px;
    text-align: center;
  }
  .empty code {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    font-size: 11px;
    padding: 1px 5px;
    color: var(--text-secondary);
  }

  /* spell list */
  .spells-pane {
    display: flex;
    flex-direction: column;
    gap: 0;
  }

  .spell-summary {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
    padding: 8px 0 6px;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }
  .spell-class {
    color: var(--accent);
    font-weight: 600;
  }
  .no-sb {
    color: var(--text-muted);
    font-style: italic;
  }
  .no-sb code {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    font-size: 11px;
    padding: 1px 5px;
    color: var(--text-secondary);
    font-style: normal;
  }
  .missing-badge {
    background: rgba(224, 92, 92, 0.12);
    color: #e05c5c;
    border-radius: 3px;
    font-size: 11px;
    padding: 1px 6px;
    margin-left: 4px;
  }

  .spell-list {
    flex: 1;
    overflow-y: auto;
  }

  .spell-level-header {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-muted);
    padding: 10px 0 3px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 1px;
  }

  .spell-row {
    display: flex;
    align-items: center;
    padding: 3px 0;
    gap: 8px;
    border-bottom: 1px solid rgba(37, 40, 54, 0.4);
  }
  .spell-row:last-child {
    border-bottom: none;
  }

  .spell-name-col {
    flex: 0 0 180px;
    min-width: 0;
  }
  .spell-desc-col {
    flex: 1;
    min-width: 0;
    overflow: hidden;
  }
  .spell-stat-col {
    flex: 0 0 70px;
    text-align: right;
  }

  .spell-link {
    font-size: 12px;
    color: var(--text-secondary);
    text-decoration: none;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    display: block;
    transition: color 0.12s;
  }
  a.spell-link:hover {
    color: var(--accent);
    text-decoration: underline;
  }

  .spell-link-missing {
    color: #e05c5c !important;
  }
  a.spell-link-missing:hover {
    color: #f07070 !important;
  }

  .spell-desc {
    font-size: 10px;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    display: block;
  }
  .spell-stat {
    font-size: 10px;
    color: var(--text-muted);
    white-space: nowrap;
  }

  /* command footer */
  .cmd-footer {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-shrink: 0;
    padding: 8px 12px;
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
  }
  .cmd-buttons {
    display: flex;
    gap: 8px;
  }
  .cmd-btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 4px 10px;
    transition:
      border-color 0.15s,
      color 0.15s;
    white-space: nowrap;
  }
  .cmd-btn:hover {
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .admin-add-spell {
    margin-left: auto;
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .copy-msg {
    font-size: 11px;
    color: var(--success);
    animation: fade-in 0.15s ease;
  }
  @keyframes fade-in {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
  }

  /* view toggle (table icon) */
  .view-toggle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 2px 6px;
  }
  .view-toggle.active {
    border-color: var(--accent-dim);
    color: var(--accent);
  }

  /* table view */
  /* quick results */
  /* The gold glow says "transient": this panel belongs to the query you just
     typed and leaves with it, unlike the listing below that persists. Inset as
     a card instead of a full-bleed strip so the border reads as an outline
     around something temporary rather than as one more divider. rgba literals
     because --accent (#c8a951) is a hex var and can't be fed to rgba(). */
  .quick {
    flex-shrink: 0;
    margin: 6px 12px;
    border: 1px solid rgba(200, 169, 81, 0.4);
    border-radius: 6px;
    background: var(--bg-secondary);
    box-shadow:
      0 0 10px rgba(200, 169, 81, 0.16),
      inset 0 0 14px rgba(200, 169, 81, 0.05);
    overflow: hidden;
  }
  .quick-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 12px;
  }
  .quick-toggle {
    display: flex;
    align-items: center;
    gap: 5px;
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }
  .quick-toggle:hover {
    color: var(--text-primary);
  }
  .chev {
    display: inline-block;
    font-size: 8px;
    transition: transform 0.15s;
  }
  .chev.open {
    transform: rotate(90deg);
  }
  .quick-count {
    color: var(--text-muted);
    font-size: 11px;
  }
  .quick-trunc {
    color: var(--accent-dim);
  }
  /* Capped rather than flexed: the panel is an answer to the search, not the
     main view, and it must never squeeze the character listing off screen. */
  .quick-wrap {
    max-height: 360px;
    overflow: auto;
    border-top: 1px solid rgba(200, 169, 81, 0.18);
  }
  .quick-table td {
    max-width: 260px;
  }
  .quick-table td.one {
    color: var(--text-muted);
  }
  .quick-table td.c-spell {
    color: var(--accent);
  }
  /* Item is what you searched for; character is what you act on. */
  .quick-table td.c-item {
    color: var(--text-primary);
    font-weight: 600;
  }
  .quick-table td.c-name {
    color: var(--accent);
    font-weight: 400;
  }

  .table-wrap {
    flex: 1;
    overflow: auto;
  }
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
  .char-table th.keys-group {
    text-align: center;
    color: var(--accent);
    border-left: 1px solid var(--border);
  }
  .char-table th.key-th {
    text-align: center;
    border-left: 1px solid rgba(37, 40, 54, 0.6);
    padding: 4px 6px;
  }
  .char-table tbody tr {
    border-bottom: 1px solid rgba(37, 40, 54, 0.6);
    cursor: pointer;
  }
  .char-table tbody tr:hover {
    background: rgba(255, 255, 255, 0.04);
  }
  .char-table tbody tr.sel {
    background: rgba(200, 169, 81, 0.12);
  }
  .char-table td {
    padding: 4px 8px;
    color: var(--text-secondary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 180px;
  }
  .char-table td.c-name {
    color: var(--text-primary);
    font-weight: 600;
  }
  .char-table td.c-zone {
    color: var(--accent);
  }
  .char-table td.num {
    text-align: right;
  }
  .char-table td.key-cell {
    text-align: center;
    border-left: 1px solid rgba(37, 40, 54, 0.6);
    color: var(--text-muted);
    max-width: none;
  }
  .char-table td.key-cell.has {
    color: var(--success, #6bd06b);
    font-weight: 700;
  }
  /* Count badge for item columns when a character carries more than one. */
  .cnt-badge {
    display: inline-block;
    min-width: 15px;
    padding: 0 4px;
    border-radius: 7px;
    background: rgba(107, 208, 107, 0.16);
    border: 1px solid rgba(107, 208, 107, 0.45);
    color: var(--success, #6bd06b);
    font-size: 10px;
    font-weight: 700;
    line-height: 14px;
    text-align: center;
  }

  /* context menu */
  .ctx-menu {
    position: fixed;
    background: var(--bg-secondary);
    border: 1px solid var(--border-hover);
    border-radius: 4px;
    padding: 4px 0;
    z-index: 1000;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.6);
    min-width: 160px;
  }
  .ctx-item {
    display: block;
    width: 100%;
    background: none;
    border: none;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 7px 14px;
    text-align: left;
    transition: background 0.1s;
  }
  .ctx-item:hover {
    background: rgba(200, 169, 81, 0.1);
    color: var(--accent);
  }

  /* ── quests sub-tab ─────────────────────────────────────────────────────── */
  .quests-pane {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px;
    overflow-y: auto;
  }
  /* Direct children of the scrolling flex column must never COMPRESS to fit —
     a flex child defaults to shrink, and a card with overflow:hidden then
     clips its own steps instead of letting the pane scroll. */
  .q-toolbar,
  .q-addpanel,
  .q-card,
  .q-section,
  .q-comp,
  .q-mednote,
  .quests-pane > .empty {
    flex-shrink: 0;
  }
  .q-toolbar {
    display: flex;
    align-items: baseline;
    gap: 10px;
  }
  .q-add {
    background: rgba(200, 169, 81, 0.12);
    border: 1px solid rgba(200, 169, 81, 0.35);
    border-radius: 4px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 4px 10px;
  }
  .q-add:hover {
    background: rgba(200, 169, 81, 0.22);
  }
  .q-asof {
    color: var(--text-muted);
    font-size: 11px;
  }
  .q-addpanel {
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .q-addsearch {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 5px 8px;
  }
  .q-addlist {
    max-height: 220px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
  }
  .q-addrow {
    background: none;
    border: none;
    border-radius: 4px;
    color: var(--text-primary);
    cursor: pointer;
    display: flex;
    font-size: 12px;
    gap: 8px;
    justify-content: space-between;
    padding: 5px 8px;
    text-align: left;
  }
  .q-addrow:hover {
    background: rgba(200, 169, 81, 0.1);
  }
  .q-class {
    color: var(--text-muted);
    font-size: 11px;
  }
  .q-card {
    border: 1px solid var(--border);
    border-radius: 6px;
    overflow: hidden;
  }
  .q-headrow {
    align-items: stretch;
    background: var(--bg-secondary);
    display: flex;
  }
  .q-head {
    align-items: baseline;
    background: none;
    border: none;
    color: var(--text-primary);
    cursor: pointer;
    display: flex;
    flex: 1;
    font-size: 13px;
    gap: 8px;
    min-width: 0;
    padding: 7px 10px;
    text-align: left;
  }
  /* The per-quest delete, visible without expanding. Quiet until hovered or
     armed — it shares the row with the expand button. */
  .q-del {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    flex-shrink: 0;
    font-size: 13px;
    padding: 0 10px;
  }
  .q-del:hover {
    color: #ff7b7b;
  }
  .q-del.armed {
    color: #ff7b7b;
    font-size: 11px;
  }
  .q-head:hover {
    background: rgba(255, 255, 255, 0.04);
  }
  .q-name {
    font-weight: 600;
  }
  .q-autobadge {
    background: rgba(110, 203, 255, 0.15);
    border-radius: 3px;
    color: #6ecbff;
    font-size: 10px;
    padding: 1px 5px;
  }
  .q-progress {
    color: var(--text-secondary);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    margin-left: auto;
  }
  .q-body {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 8px 10px;
  }
  .q-meta {
    color: var(--text-muted);
    font-size: 11px;
    margin-bottom: 4px;
  }
  .q-step {
    align-items: baseline;
    display: flex;
    font-size: 12px;
    gap: 6px;
    line-height: 1.5;
  }
  .q-step.done .q-steptext {
    opacity: 0.55;
  }
  .q-stepnum {
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
    min-width: 20px;
    text-align: right;
  }
  .q-steptext {
    flex: 1;
    min-width: 0;
  }
  .q-dim {
    color: var(--text-muted);
  }
  /* Item names are hover targets for the stat card, so they read as such. */
  .q-item {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    color: #d4af37;
    cursor: help;
    text-decoration: underline dotted rgba(255, 255, 255, 0.25);
    text-underline-offset: 2px;
  }
  .q-item:hover {
    color: var(--accent);
  }
  .q-zone {
    color: #6ecbff;
  }
  /* the loc doubles as a "drop a waypoint" button */
  .q-locbtn {
    background: none;
    border: none;
    padding: 0;
    margin-left: 4px;
    font-size: 11px;
    color: #6ecbff;
    font-variant-numeric: tabular-nums;
    cursor: pointer;
  }
  .q-locbtn:hover {
    text-decoration: underline;
  }
  .q-mob {
    color: #e0b0ff;
  }
  /* A conversation wraps as a sequence of separate copy targets. */
  .q-says {
    display: inline;
  }
  .q-say {
    background: none;
    border: none;
    padding: 0 2px;
    border-radius: 3px;
    font: inherit;
    font-style: italic;
    color: #9fdf9f;
    cursor: copy;
    text-align: left;
  }
  .q-say:hover {
    color: var(--accent);
  }
  /* The line you actually copied stays marked after the cursor moves on —
     with several replies on one row, hover alone doesn't say which one went
     to the clipboard. */
  .q-said {
    color: var(--accent);
    background: rgba(200, 169, 81, 0.14);
  }
  .q-flash {
    color: #9fdf9f;
    font-size: 10px;
  }
  /* Confirmation that ticking one box ticked others further up. */
  .q-autotick {
    font-size: 10px;
    color: var(--accent);
    background: rgba(200, 169, 81, 0.14);
    border-radius: 3px;
    padding: 1px 5px;
    white-space: nowrap;
  }
  .q-stepkind {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
    margin-right: 2px;
  }
  .q-stepnote {
    font-size: 10.5px;
    color: var(--text-muted);
    font-style: italic;
    margin-top: 2px;
  }
  .q-prereq {
    color: var(--text-muted);
    font-size: 11px;
    font-style: italic;
  }
  .q-rewardline {
    border-top: 1px solid rgba(255, 255, 255, 0.05);
    padding-top: 5px;
    margin-top: 4px;
    font-size: 12px;
  }
  .q-plat,
  .q-skill,
  .q-gate {
    color: var(--text-secondary);
    font-size: 11px;
  }
  .q-held {
    background: rgba(159, 223, 159, 0.12);
    border-radius: 3px;
    color: #9fdf9f;
    font-size: 11px;
    margin-left: 6px;
    padding: 0 5px;
    white-space: nowrap;
  }
  .q-stepwhen {
    color: var(--text-muted);
    font-size: 10px;
    white-space: nowrap;
  }
  .q-section {
    color: #e3a008;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    margin-top: 8px;
    text-transform: uppercase;
  }
  .q-medgrid {
    display: grid;
    gap: 6px;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    margin-top: 4px;
  }
  .q-med {
    border: 1px solid var(--border);
    border-radius: 5px;
    font-size: 11px;
    padding: 6px 8px;
  }
  /* The turn-in cell closes each medallion's row: who assembles the pieces. */
  .q-medturn {
    border-color: rgba(200, 169, 81, 0.35);
    background: rgba(200, 169, 81, 0.06);
  }
  .q-med.held {
    border-color: rgba(159, 223, 159, 0.5);
    background: rgba(159, 223, 159, 0.07);
  }
  .q-medname {
    font-weight: 600;
    margin-bottom: 2px;
  }
  .q-medloc {
    color: #9fdf9f;
  }
  .q-medsrc {
    color: var(--text-secondary);
  }
  .q-medzone {
    color: #6ecbff;
  }
  .q-medhold {
    border-top: 1px solid var(--border);
    color: var(--text-muted);
    margin-top: 4px;
    padding-top: 3px;
  }
  .q-mednote {
    color: var(--text-muted);
    font-size: 11px;
  }
  .q-comp {
    align-items: baseline;
    display: flex;
    font-size: 12px;
    gap: 10px;
  }
  .q-compitem {
    background: none;
    border: none;
    padding: 0;
    color: #d4af37;
    font-size: 11px;
    cursor: help;
    text-decoration: underline dotted rgba(255, 255, 255, 0.25);
    text-underline-offset: 2px;
  }
  .q-compdate {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: auto;
  }

  /* ── item card (hover) — same card the quest editor and Magelo show ── */
  .q-tip {
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
  .q-tip-name {
    font-size: 12.5px;
    font-weight: 700;
    color: var(--accent);
    margin-bottom: 4px;
  }
  .q-tip-line {
    font-size: 11px;
    color: var(--text-primary);
    line-height: 1.5;
  }
  .q-tip-rule {
    height: 1px;
    margin: 5px 0 4px;
    background: rgba(255, 255, 255, 0.14);
  }
  .q-tip-dim {
    color: var(--text-muted);
    font-style: italic;
  }
</style>
