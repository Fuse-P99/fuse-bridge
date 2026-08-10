<script>
  // Quest editor (admin mode; its endpoints are officer-gated too).
  //
  // A quest is a named walkthrough: ordered steps that produce something worth
  // having. A class epic is ONE quest with twenty steps, not twenty quests.
  //
  // The form starts as three fields and grows only as steps are added — each
  // step declares its kind, and only that kind's fields appear. Steps collapse
  // to a one-line summary once entered, so a twenty-step epic stays readable.
  //
  // Pricing lives on the server and follows one rule worth knowing here: only
  // items brought in from OUTSIDE cost anything. An item an earlier step handed
  // you is already paid for, so entering the intermediate steps of a chain
  // makes the total more accurate, never larger.
  import { onMount } from "svelte";
  import { slide } from "svelte/transition";
  import {
    ListQuests,
    ListQuestFactions,
    ListQuestVocab,
    SaveQuest,
    DeleteQuest,
    SearchItems,
    SearchQuestMobs,
    SaveQuestMob,
    ListQuestZones,
    GetItemByName,
    ItemDroppers,
    AddQuestMarker,
    IsOfficer,
    IsAdminMode,
    WhoHasItem,
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "./scale.js";
  import { tipStats, TIP_RULE } from "./itemTip.js";
  import {
    stepIns,
    stepOuts,
    slotNames,
    rollIns,
    mobZone,
    stepPoint,
    stepLine,
    sayLines,
    KIND_LABEL,
    METHOD_LABEL,
    kindLabel,
    rewardText,
  } from "./questSteps.js";
  import QuestImport from "./QuestImport.svelte";

  // Everyone can read the walkthroughs; only officers can change them. This
  // hides the editing affordances — the server enforces the same rule on every
  // write, so this is about not offering an action that would be refused.
  let canEdit = false;
  onMount(async () => {
    try {
      canEdit = (await IsOfficer()) || (await IsAdminMode());
    } catch {
      canEdit = false; // unknown → read-only, never the other way round
    }
  });

  // Unset when rendered as the Quests tab rather than as a dialog over the
  // Magelo sheet.
  export let onClose = null;
  // embedded drops the modal chrome — no dimmed backdrop, no fixed-size panel,
  // no Close button — so the same component serves as a full tab. The pickers
  // and the item card stay fixed-position overlays either way.
  export let embedded = false;

  // Which fields each step kind uses. Mirrors the column map in
  // dbConnector.go's quest_steps comment; the server ignores anything else.
  // mobs: 1 means exactly one NPC; "many" means any number — an item routinely
  // drops from several mobs, and one quest names six.
  const FIELDS = {
    handin: { mob: 1, faction: 1, plat: 1, into: 1, out: 1 },
    combine: { skill: 1, into: 1, out: 1, fail: 1 },
    loot: { mob: "many", out: 1 },
    // Acquire covers every no-combat way of coming by an item. The NPC is
    // optional — a ground spawn has none, a purchase has a merchant and a
    // pickpocket has a victim — and so is the coin, which is why one kind
    // serves all of them.
    acquire: { method: 1, mob: 1, zone: 1, plat: 1, out: 1 },
    dialogue: { mob: 1, faction: 1, say: 1, out: 1 },
  };
  // KIND_LABEL/METHOD_LABEL/kindLabel/rewardText live in questSteps.js — the
  // per-character Quests sub-tab renders the same labels.
  // What the NPC field is called for each kind — "Hand in to" and "Dropped by"
  // are the same column and very different questions. Acquire asks it of the
  // method instead, since buying from a merchant and picking someone's pocket
  // are not the same relationship.
  const MOB_LABEL = {
    handin: "Hand in to",
    loot: "Dropped by (any of)",
    dialogue: "Talk to",
  };
  // Only two of the acquire methods involve anyone: nobody hands you a ground
  // spawn. Keyed off the same map as the labels below so the two can't drift.
  const METHOD_MOB_LABEL = {
    purchase: "Bought from",
    pickpocket: "Picked from",
  };
  const REWARD_LABEL = {
    item: "Item",
    faction: "Faction",
    cycle: "Cycled reward",
  };

  // Server-owned vocabularies, fetched so a dropdown can't offer a value the
  // save would reject. Seeded with the same lists as a fallback for the one
  // render before they arrive.
  let vocab = {
    step_kinds: ["handin", "combine", "loot", "acquire", "dialogue"],
    acquire_methods: ["ground", "forage", "fish", "purchase", "pickpocket"],
    tradeskills: [],
    classes: [],
    factions: [],
    reward_kinds: ["item", "faction", "cycle"],
  };

  let quests = null; // null = loading
  let err = "";
  let busy = false;
  // Free-text filter over the list, plus the quick filters beside it. Fourteen
  // class epics was already a lot of rows; the Velious armor sets are 300 more.
  let filter = "";
  let fClass = "";
  let fSlot = "";
  let fEpic = false;
  let fFaction = false;
  $: filtersOn = !!(filter.trim() || fClass || fSlot || fEpic || fFaction);
  function clearFilters() {
    filter = "";
    fClass = "";
    fSlot = "";
    fEpic = false;
    fFaction = false;
  }

  let form = null; // null on the list view
  let confirmDel = null;
  let showImport = false;
  // Which step cards are expanded, by index. Steps collapse once entered so a
  // long quest stays scannable; a newly added one opens automatically.
  let openStep = -1;

  let factionGroups = [];
  let zones = null; // fetched on first use of a ground-spawn or NPC form

  onMount(async () => {
    await load();
    try {
      vocab = (await ListQuestVocab()) || vocab;
    } catch {
      /* the fallbacks above keep the dropdowns usable */
    }
  });

  async function load() {
    err = "";
    try {
      quests = (await ListQuests()) || [];
    } catch (e) {
      quests = [];
      err = String(e);
    }
    try {
      factionGroups = (await ListQuestFactions()) || [];
    } catch {
      /* autocomplete is a convenience; the fields still take free text */
    }
  }

  async function needZones() {
    if (zones !== null) return;
    try {
      zones = (await ListQuestZones()) || [];
    } catch {
      // Optional and validated server-side; without the list a zone id is
      // still typeable.
      zones = [];
    }
  }

  // ── list ───────────────────────────────────────────────────────────────────

  function haystack(q) {
    return [
      q.name,
      q.class,
      ...(q.steps || []).flatMap((s) => [
        ...(s.mobs || []).map((m) => m.name),
        s.zone_name,
        s.faction_group,
        ...(s.items || []).map((i) => i.name),
      ]),
      ...(q.rewards || []).flatMap((r) => [
        r.name,
        r.faction_group,
        ...(r.cycle || []),
      ]),
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
  }

  // The dropdown lists canonical SINGLE equip slots, named and ordered the way
  // the outputfile inventory names them (Charm excluded — nothing quest-y goes
  // there). The item DB stores the wiki's slot strings ("PRIMARY SECONDARY",
  // "FINGER"), so reward slots are tokenized and mapped onto these; a quest
  // matches a slot when ANY of its rewards — any cycle member included, the
  // server unions those in — can be equipped there.
  const SLOT_ORDER = [
    "Ear", "Head", "Face", "Neck", "Shoulders", "Arms", "Back", "Wrist",
    "Range", "Hands", "Primary", "Secondary", "Fingers", "Chest", "Legs",
    "Feet", "Waist", "Ammo",
  ];
  const SLOT_CANON = {
    ear: "Ear", head: "Head", face: "Face", neck: "Neck",
    shoulders: "Shoulders", shoulder: "Shoulders", arms: "Arms", arm: "Arms",
    back: "Back", wrist: "Wrist", wrists: "Wrist", range: "Range",
    ranged: "Range", hands: "Hands", hand: "Hands", primary: "Primary",
    secondary: "Secondary", finger: "Fingers", fingers: "Fingers",
    chest: "Chest", legs: "Legs", feet: "Feet", waist: "Waist", ammo: "Ammo",
  };
  function slotTokens(str) {
    const out = new Set();
    for (const w of String(str || "").split(/[\s,/]+/)) {
      const c = SLOT_CANON[w.toLowerCase()];
      if (c) out.add(c);
    }
    return out;
  }
  function questSlots(q) {
    const out = new Set();
    for (const r of q.rewards || [])
      for (const t of slotTokens(r.slot)) out.add(t);
    return out;
  }
  // Only slots actually present in the data are offered, in outputfile order —
  // an option that matches nothing is worse than no option.
  $: slotOptions = (() => {
    const present = new Set();
    for (const q of quests || [])
      for (const t of questSlots(q)) present.add(t);
    return SLOT_ORDER.filter((s) => present.has(s));
  })();

  // An epic is recognised by name. There's no flag for it, and adding one would
  // mean maintaining it by hand forever — every class epic is titled
  // "<Class> Epic: <item>", and anything else named "epic" is one too.
  const isEpic = (q) => /\bepic\b/i.test(q.name || "");

  // Faction-only: what you get is standing, not an item. Quests with no reward
  // recorded at all are excluded — that's missing data, not a faction reward.
  //
  // Coin isn't in here because coin isn't a reward kind: plat_cost is a step's
  // COST. If a quest ever pays you money we'd need a reward kind for it.
  const isFactionOnly = (q) =>
    (q.rewards || []).length > 0 &&
    (q.rewards || []).every((r) => r.kind === "faction");

  // Takes the filters as arguments rather than reading them off the component.
  // The reactive statement below only re-runs when it MENTIONS a variable, so
  // a matches(q) that closed over them would leave the list stale until
  // something else happened to invalidate it.
  function matches(q, t, cls, slot, epic, faction) {
    if (cls && q.class !== cls) return false;
    if (slot && !questSlots(q).has(slot)) return false;
    if (epic && !isEpic(q)) return false;
    if (faction && !isFactionOnly(q)) return false;
    return !t || haystack(q).includes(t);
  }

  $: shown =
    quests === null
      ? null
      : quests.filter((q) =>
          matches(
            q,
            filter.trim().toLowerCase(),
            fClass,
            fSlot,
            fEpic,
            fFaction,
          ),
        );

  // ── walkthrough ────────────────────────────────────────────────────────────
  // Clicking a quest slides its steps out as a checklist you can work through.

  let openQuest = 0; // quest id, 0 for none
  // Ticked steps, keyed "questId:stepIndex". Kept in localStorage rather than
  // the server: it's one person's progress on one machine, not guild data, and
  // it has to survive closing the editor to be worth anything.
  let done = loadDone();

  function loadDone() {
    try {
      return new Set(JSON.parse(localStorage.getItem("fuse.questDone") || "[]"));
    } catch {
      return new Set();
    }
  }

  // Earlier steps that MUST already be done for step i to be done, traced back
  // through the quest the same way the seed's reachability check works.
  //
  // Two things imply a step: an input that an earlier step is the only producer
  // of, and the `follows` flag, which marks a step that required the one before
  // it with nothing passing between them (hand in X, a mob spawns, kill it).
  //
  // "Only producer" is deliberate. Where several earlier steps could have
  // satisfied a slot — alternatives, or a branch you hold nothing from — the
  // route can't be known, so nothing is ticked. That's the same rule the map
  // marker follows: when the data is ambiguous, say nothing rather than guess.
  // A wrong tick is worse than a missing one, because it hides work still to do.
  function impliedSteps(q, i) {
    const steps = q.steps || [];
    const need = new Set();
    const visit = (n) => {
      const s = steps[n];
      if (!s) return;
      if (s.follows && n > 0 && !need.has(n - 1)) {
        need.add(n - 1);
        visit(n - 1);
      }
      for (const slot of stepIns(s)) {
        const names = slotNames(slot);
        const producers = [];
        for (let m = 0; m < n; m++) {
          if (stepOuts(steps[m]).some((o) => names.includes(o.name))) {
            producers.push(m);
          }
        }
        if (producers.length === 1 && !need.has(producers[0])) {
          need.add(producers[0]);
          visit(producers[0]);
        }
      }
    };
    visit(i);
    need.delete(i);
    return need;
  }

  // Transient "…and the N steps it needed" note, keyed by quest:step.
  let autoTicked = "";
  let autoTickedN = 0;
  let autoTickTimer;

  function toggleDone(q, i) {
    const questId = q.id;
    const k = `${questId}:${i}`;
    const wasDone = done.has(k);
    if (wasDone) done.delete(k);
    else done.add(k);
    // Ticking a step implies everything it took to get there; unticking only
    // unticks the one clicked. The asymmetry is deliberate — deciding you
    // haven't done step 9 says nothing about step 2, and silently clearing
    // earlier progress would be destructive.
    let added = 0;
    if (!wasDone) {
      for (const n of impliedSteps(q, i)) {
        const pk = `${questId}:${n}`;
        if (!done.has(pk)) {
          done.add(pk);
          added++;
        }
      }
    }
    done = done;
    clearTimeout(autoTickTimer);
    autoTicked = added ? k : "";
    autoTickedN = added;
    if (added) {
      autoTickTimer = setTimeout(() => (autoTicked = ""), 2600);
    }
    try {
      localStorage.setItem("fuse.questDone", JSON.stringify([...done]));
    } catch {
      /* progress ticks are a convenience; a full quota isn't worth an error */
    }
  }

  // ── step detail ────────────────────────────────────────────────────────────
  // A step is rendered as labelled facts rather than one sentence. "Loot it"
  // told you nothing; what you need is the item, the mobs and the zone, each
  // where you can find it at a glance.

  // stepIns/stepOuts/slotNames/rollIns/mobZone/stepPoint/stepLine/sayLines
  // moved to questSteps.js — the per-character Quests sub-tab renders steps
  // with the same functions, and two copies would drift.
  // What to call the NPC on a step, for the same reason: you buy from a
  // merchant and pick from a victim, and "NPC" says neither.
  function mobLabel(s) {
    if (s.kind === "acquire") return METHOD_MOB_LABEL[s.method] || "NPC";
    return MOB_LABEL[s.kind] || "NPC";
  }
  // Whether to offer the NPC field at all. Every kind that declares one wants
  // it; an acquire only does when its method involves somebody.
  function wantsMob(s) {
    if (s.kind === "acquire") return !!METHOD_MOB_LABEL[s.method];
    return !!(FIELDS[s.kind] || {}).mob;
  }
  // Clicking a step's loc drops a temporary quest waypoint on that zone's map
  // (questmarkers.go): it survives restarts and retires itself once the zone
  // has been visited and then left (or the player camps). Label prefers the
  // NPC at the spot; a bare ground loc gets the quest name.
  let marked = "";
  let markedTimer;
  async function dropMarker(q, i, tk) {
    try {
      const label = tk.pt.what !== "here" ? tk.pt.what : q.name;
      await AddQuestMarker(tk.name, tk.pt.y, tk.pt.x, label);
      clearTimeout(markedTimer);
      marked = `${q.id}:${i}`;
      markedTimer = setTimeout(() => (marked = ""), 2600);
    } catch {
      /* the waypoint is a convenience — the loc is still on screen */
    }
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

  // ── reward tooltip ─────────────────────────────────────────────────────────
  // The same card the Magelo sheet shows for an inventory item, so a reward can
  // be judged without leaving the list. Items are fetched once per name and
  // cached; a name not in the item DB says so rather than showing a blank card.

  let itemCache = {};
  let tip = null; // { name, item, x, y }

  // "Also held by" — the user's own characters holding the hovered item
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
    // Positioned inside the zoomed shell like MageloView's, so cursor
    // coordinates divide by the UI scale or the card drifts at Medium/Large.
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

  // ── form ───────────────────────────────────────────────────────────────────

  function blankStep(kind) {
    return {
      kind,
      tradeskill: "",
      skill_req: 0,
      method: kind === "acquire" ? "ground" : "",
      mobs: [],
      zone_id: "",
      zone_name: "",
      loc_y: 0,
      loc_x: 0,
      has_loc: false,
      say: "",
      faction_level: "",
      faction_group: "",
      plat_cost: 0,
      follows: false,
      note: "",
      // Held apart by role rather than as one list with a role field: the two
      // read as different questions and are edited independently.
      into: [],
      out: [],
    };
  }

  // ── name ⇄ wiki link ───────────────────────────────────────────────────────
  // Quest pages on the P99 wiki are titled exactly as the quest is named, with
  // spaces as underscores, so each field can fill the other and neither has to
  // be typed twice.
  const WIKI_BASE = "https://wiki.project1999.com/";

  function wikiFromName(name) {
    const t = (name || "").trim();
    // encodeURI, not encodeURIComponent: the wiki's own titles carry colons
    // ("Bard Epic: Singing Short Sword") and apostrophes, and escaping those
    // gives a URL that works but reads as line noise.
    return t ? WIKI_BASE + encodeURI(t.replace(/\s+/g, "_")) : "";
  }

  function nameFromWiki(url) {
    let s = (url || "").trim();
    if (!s) return "";
    s = s.split(/[?#]/)[0].replace(/\/+$/, "");
    s = s.slice(s.lastIndexOf("/") + 1);
    try {
      s = decodeURIComponent(s);
    } catch {
      /* a hand-typed link can contain a stray %; keep it as written */
    }
    return s.replace(/_/g, " ").trim();
  }

  // Whether wiki_url is ours to keep updating. Set when we fill it, cleared the
  // moment the field is edited by hand — renaming a quest should carry the link
  // along, but never overwrite a link someone chose deliberately.
  let wikiAuto = false;

  function onNameInput() {
    if (!wikiAuto && form.wiki_url.trim()) return;
    form.wiki_url = wikiFromName(form.name);
    wikiAuto = true;
  }

  function onWikiInput() {
    // Typed over: hands off from here.
    wikiAuto = false;
    if (!form.name.trim()) {
      form.name = nameFromWiki(form.wiki_url);
    }
  }

  function newQuest() {
    form = {
      id: 0,
      name: "",
      class: "",
      wiki_url: "",
      direct_rewards: false,
      prereqs: [],
      rewards: [],
      steps: [],
    };
    wikiAuto = false;
    openStep = -1;
    closeAll();
  }

  function editQuest(q) {
    // A stored link that is exactly what the name derives goes back under
    // automatic upkeep; anything else was chosen and is left alone. An empty
    // one counts as automatic so renaming fills it.
    wikiAuto =
      !(q.wiki_url || "").trim() ||
      (q.wiki_url || "").trim() === wikiFromName(q.name);
    form = {
      id: q.id,
      name: q.name || "",
      class: q.class || "",
      wiki_url: q.wiki_url || "",
      direct_rewards: !!q.direct_rewards,
      prereqs: (q.prereqs || []).map((p) => ({ id: p.id, name: p.name })),
      rewards: (q.rewards || []).map((r) => ({
        kind: r.kind,
        name: r.name || "",
        faction_group: r.faction_group || "",
        faction_delta: r.faction_delta || 0,
        cycle: [...(r.cycle || [])],
      })),
      steps: (q.steps || []).map((s) => {
        const st = blankStep(s.kind);
        Object.assign(st, {
          tradeskill: s.tradeskill || "",
          skill_req: s.skill_req || 0,
          method: s.method || (s.kind === "acquire" ? "ground" : ""),
          mobs: (s.mobs || []).map((m) => ({
            id: m.id,
            name: m.name,
            zone: m.zone || "",
            loc_y: m.loc_y || 0,
            loc_x: m.loc_x || 0,
            has_loc: !!m.has_loc,
          })),
          zone_id: s.zone_id || "",
          zone_name: s.zone_name || "",
          loc_y: s.loc_y || 0,
          loc_x: s.loc_x || 0,
          has_loc: !!s.has_loc,
          say: s.say || "",
          faction_level: s.faction_level || "",
          faction_group: s.faction_group || "",
          plat_cost: s.plat_cost || 0,
          follows: !!s.follows,
          note: s.note || "",
        });
        for (const it of s.items || []) {
          if (it.role === "out") st.out.push({ name: it.name });
          else
            // One entry per SLOT. alts holds every item that satisfies it —
            // one normally, several when any of them will do.
            st.into.push({
              alts: [it.name, ...(it.alts || [])],
              consumed_ok: it.consumed_ok !== false,
              consumed_fail: it.consumed_fail !== false,
            });
        }
        return st;
      }),
    };
    openStep = -1;
    closeAll();
  }

  function addStep(kind) {
    form.steps = [...form.steps, blankStep(kind)];
    openStep = form.steps.length - 1;
    if (kind === "acquire") needZones();
    form = form;
  }

  function removeStep(i) {
    form.steps.splice(i, 1);
    if (openStep === i) openStep = -1;
    else if (openStep > i) openStep -= 1;
    form = form;
  }

  // Picking the item on a loot step should answer "who drops this" without
  // anyone typing it — the mapping is already in the DB from the loot browser.
  // Additive and non-destructive: known droppers are appended to whatever is
  // already listed, and anything wrong can be removed. A miss is normal, since
  // the mapping only holds items somebody has looked up.
  let dropNote = "";
  let dropTimer;
  async function fillDroppers(st, itemName) {
    if (st.kind !== "loot" || !itemName) return;
    try {
      const found = (await ItemDroppers(itemName)) || [];
      const have = new Set(st.mobs.map((m) => m.id));
      const add = found.filter((m) => !have.has(m.id));
      if (add.length) {
        st.mobs = [
          ...st.mobs,
          ...add.map((m) => ({
            id: m.id,
            name: m.name,
            zone: m.zone_name || m.zone_id || "",
          })),
        ];
        form = form;
        dropNote = `Added ${add.length} known dropper${
          add.length === 1 ? "" : "s"
        } of ${itemName}.`;
      } else if (!found.length) {
        dropNote = `Nothing in the DB drops ${itemName} — add the NPCs by hand.`;
      } else {
        dropNote = `Its known droppers are already listed.`;
      }
      clearTimeout(dropTimer);
      dropTimer = setTimeout(() => (dropNote = ""), 4000);
    } catch {
      /* a convenience; the NPC list is editable by hand regardless */
    }
  }

  function moveStep(i, by) {
    const j = i + by;
    if (j < 0 || j >= form.steps.length) return;
    const [s] = form.steps.splice(i, 1);
    form.steps.splice(j, 0, s);
    if (openStep === i) openStep = j;
    form = form;
  }

  // The last item any step hands you — the default reward, since that is
  // overwhelmingly what a quest is for.
  $: lastReceived = form
    ? [...form.steps]
        .reverse()
        .flatMap((s) => [...s.out].reverse())
        .map((o) => o.name)
        .find((n) => n && n.trim()) || ""
    : "";

  function addReward(kind) {
    const r = {
      kind,
      name: "",
      faction_group: "",
      faction_delta: 0,
      cycle: [],
    };
    // Prefill the first item reward from the last thing a step hands you.
    // Prefilled, then stored: editing a step afterwards won't silently rewrite
    // a reward you have already looked at and accepted.
    if (kind === "item" && !form.rewards.length && lastReceived) {
      r.name = lastReceived;
    }
    if (kind === "cycle") {
      r.cycle = lastReceived ? [lastReceived, ""] : ["", ""];
    }
    form.rewards = [...form.rewards, r];
    form = form;
  }

  function removeReward(i) {
    form.rewards.splice(i, 1);
    form = form;
  }

  function moveCycle(r, k, by) {
    const j = k + by;
    if (j < 0 || j >= r.cycle.length) return;
    const [n] = r.cycle.splice(k, 1);
    r.cycle.splice(j, 0, n);
    form = form;
  }

  // ── pickers ────────────────────────────────────────────────────────────────
  // Each picker takes a closure rather than a path into the form, so the same
  // overlay serves step items, reward items and cycle links without knowing
  // where any of them live.

  let pick = null; // { title, apply(name), suggest[] }
  let pickQ = "";
  let pickSugs = [];
  let pickTimer;
  // Keyboard highlight for each picker. 0 rather than -1 so Enter takes the
  // top match without arrowing to it first, which is what an autocomplete is
  // for.
  let pickIdx = 0;
  let mobIdx = 0;
  let prereqIdx = 0;

  // suggest is what an earlier step of this quest already produces. Offered
  // before anything is typed, because that IS the common case for a hand-in —
  // but only offered, never enforced: three quarters of the Velious hand-in
  // slots are gems and other things no step of the quest produces, and the
  // whole DKP figure comes from exactly those.
  function openItemPick(title, current, apply, suggest = []) {
    pick = { title, apply, suggest };
    pickQ = current || "";
    pickSugs = [];
    pickIdx = 0;
    onPickInput();
  }

  // Every item name a step's hand-in slots already hold, so the shortlist can
  // leave out what's been picked.
  function slotNamesOf(st) {
    return (st.into || []).flatMap((i) => i.alts || []);
  }

  // What earlier steps of this quest hand you, for the picker's shortlist.
  // Everything a step produces, ordered, deduped, minus what this step already
  // takes — offering the item that's already in the slot is noise.
  function earlierOutputs(stepIndex, taken) {
    const seen = new Set((taken || []).map((n) => (n || "").toLowerCase()));
    const out = [];
    for (let i = 0; i < stepIndex && form && i < form.steps.length; i++) {
      for (const o of form.steps[i].out || []) {
        const n = (o.name || "").trim();
        if (!n || seen.has(n.toLowerCase())) continue;
        seen.add(n.toLowerCase());
        out.push(n);
      }
    }
    return out;
  }

  // Arrow-key navigation, shared by all three search pickers: Down/Up move the
  // highlight and wrap, Enter takes it, Escape closes. Written once — three
  // near-identical copies is how one of them quietly stops wrapping.
  function pickerKey(e, count, index, onMove, onChoose, onClose) {
    switch (e.key) {
      case "ArrowDown":
        if (!count) return;
        e.preventDefault();
        onMove(index + 1 >= count ? 0 : index + 1);
        break;
      case "ArrowUp":
        if (!count) return;
        e.preventDefault();
        onMove(index <= 0 ? count - 1 : index - 1);
        break;
      case "Enter":
        if (index >= 0 && index < count) {
          e.preventDefault();
          onChoose(index);
        }
        break;
      case "Escape":
        e.preventDefault();
        onClose();
        break;
    }
  }

  // Keeps the highlighted row visible when the selection is moved by keyboard
  // rather than by the cursor. "nearest" so it only scrolls when it has to.
  function keepInView(node, on) {
    const go = (v) => v && node.scrollIntoView({ block: "nearest" });
    go(on);
    return { update: go };
  }

  function closeAll() {
    pick = null;
    pickQ = "";
    pickSugs = [];
    clearTimeout(pickTimer);
    mobPick = null;
    mobForm = null;
    mobSugs = [];
    mobErr = "";
    mobDup = "";
    clearTimeout(mobTimer);
    prereqPick = false;
  }

  function onPickInput() {
    clearTimeout(pickTimer);
    pickIdx = 0;
    const q = pickQ.trim();
    if (q.length < 2) {
      pickSugs = [];
      return;
    }
    pickTimer = setTimeout(() => {
      // No slot/class/race filter — a quest item can be anything, including
      // components no character can equip.
      SearchItems(q, "", "", "")
        .then((names) => {
          pickSugs = names || [];
          pickIdx = 0;
        })
        .catch(() => (pickSugs = []));
    }, 250);
  }

  // What the picker actually lists: the search results once there's a query,
  // otherwise this quest's own earlier outputs as a shortcut.
  $: pickRows =
    pick && pickQ.trim().length < 2 ? pick.suggest || [] : pickSugs;

  function chooseItem(name) {
    pick.apply(name);
    form = form;
    pick = null;
    pickQ = "";
    pickSugs = [];
  }

  // NPC picker, shared by every kind with a mob field.
  let mobPick = null; // { apply(mob) }
  let mobQ = "";
  let mobSugs = [];
  let mobTimer;
  let mobBusy = false;
  let mobErr = "";
  let mobDup = "";
  let mobForm = null; // null = browsing; object = the add/edit NPC form

  function openMobPick(current, apply) {
    mobPick = { apply };
    mobForm = null;
    mobErr = "";
    mobDup = "";
    // Blank opens on the flagged quest-NPC roster, which the server returns
    // for a query this short.
    mobQ = current || "";
    mobSugs = [];
    onMobInput();
  }

  function onMobInput() {
    clearTimeout(mobTimer);
    mobIdx = 0;
    const q = mobQ.trim();
    // No minimum: a short query is what asks for the known roster.
    mobTimer = setTimeout(() => {
      SearchQuestMobs(q)
        .then((mobs) => {
          mobSugs = mobs || [];
          mobIdx = 0;
        })
        .catch(() => (mobSugs = []));
    }, 250);
  }

  function chooseMob(m) {
    mobPick.apply(m);
    form = form;
    mobPick = null;
    mobForm = null;
    mobSugs = [];
    mobDup = "";
  }

  async function openMobForm(m) {
    mobErr = "";
    mobDup = "";
    mobForm = m
      ? {
          id: m.id,
          name: m.name,
          nicknames: m.nicknames || "",
          zone_id: m.zone_id || "",
          faction: m.faction || "",
          quest_mob: !!m.quest_mob,
          loc_y: m.loc_y || 0,
          loc_x: m.loc_x || 0,
          has_loc: !!m.has_loc,
        }
      : {
          id: 0,
          // Adding after a search that found nothing: carry the text over
          // rather than making it be typed twice.
          name: mobQ.trim(),
          nicknames: "",
          zone_id: "",
          faction: "",
          quest_mob: true,
          loc_y: 0,
          loc_x: 0,
          has_loc: false,
        };
    await needZones();
  }

  async function saveMob(confirm = false) {
    if (mobBusy || !mobForm.name.trim()) return;
    mobBusy = true;
    mobErr = "";
    try {
      const res = await SaveQuestMob(mobForm, confirm);
      if (res.duplicate) {
        // Nothing was written. Hold the form open with the question attached.
        mobDup = res.duplicate;
        mobBusy = false;
        return;
      }
      chooseMob(res.mob);
      // A new NPC faction can be one nobody has used yet, so refresh the
      // autocomplete rather than leaving it a save behind.
      ListQuestFactions()
        .then((g) => (factionGroups = g || []))
        .catch(() => {});
    } catch (e) {
      mobErr = String(e);
    }
    mobBusy = false;
  }

  // Prerequisite picker: another quest that must be done first.
  let prereqPick = false;
  let prereqQ = "";
  $: prereqChoices =
    quests && form
      ? quests.filter(
          (q) =>
            q.id !== form.id &&
            !form.prereqs.some((p) => p.id === q.id) &&
            (!prereqQ.trim() ||
              haystack(q).includes(prereqQ.trim().toLowerCase())),
        )
      : [];

  function addPrereq(q) {
    form.prereqs = [...form.prereqs, { id: q.id, name: q.name }];
    prereqPick = false;
    prereqQ = "";
    form = form;
  }

  // ── validation ─────────────────────────────────────────────────────────────

  $: questNames = [
    ...new Set((quests || []).map((q) => q.name).filter(Boolean)),
  ].sort((a, b) => a.localeCompare(b));

  // The quest the typed name already belongs to, when it isn't the open one.
  // Offered as an explicit load rather than swapping the form out underneath
  // you — picking from an autocomplete shouldn't discard work.
  $: nameMatch =
    form && form.name.trim()
      ? (quests || []).find(
          (q) =>
            q.id !== form.id &&
            q.name &&
            q.name.toLowerCase() === form.name.trim().toLowerCase(),
        ) || null
      : null;

  function stepErr(st, n) {
    const f = FIELDS[st.kind] || {};
    if (f.faction && st.faction_level && !st.faction_group.trim())
      return `Step ${n}: pick who the faction requirement is with.`;
    if (f.faction && st.faction_group.trim() && !st.faction_level)
      return `Step ${n}: pick the faction level required with ${st.faction_group.trim()}.`;
    if (st.skill_req < 0 || st.skill_req > 255)
      return `Step ${n}: skill must be 0–255.`;
    if (st.into.some((i) => i.alts.some((a) => !a.trim())))
      return `Step ${n}: an item slot is empty — pick one or remove it.`;
    if (st.out.some((o) => !o.name.trim()))
      return `Step ${n}: an item slot is empty — pick one or remove it.`;
    return "";
  }

  $: formErr = !form
    ? ""
    : !form.name.trim()
      ? "A quest name is required."
      : form.wiki_url.trim() &&
          !/^https?:\/\//i.test(form.wiki_url.trim())
        ? "The wiki link should start with http:// or https://."
        : form.steps.map((s, i) => stepErr(s, i + 1)).find(Boolean) ||
          form.rewards
            .map((r, i) =>
              r.kind === "item" && !r.name.trim()
                ? `Reward ${i + 1}: pick an item.`
                : r.kind === "faction" && !r.faction_group.trim()
                  ? `Reward ${i + 1}: pick which faction changes.`
                  : r.kind === "faction" && !r.faction_delta
                    ? `Reward ${i + 1}: a faction reward needs a value.`
                    : r.kind === "cycle" &&
                        r.cycle.filter((c) => c.trim()).length < 2
                      ? `Reward ${i + 1}: a cycle needs at least two items.`
                      : "",
            )
            .find(Boolean) ||
          "";

  $: formNote =
    form && !formErr && !form.steps.length
      ? "No steps yet — add the first one below."
      : form && !formErr && !form.rewards.length
        ? "No reward set. Without one this quest won't price anything."
        : "";

  async function save() {
    if (formErr || busy) return;
    busy = true;
    err = "";
    try {
      await SaveQuest({
        id: form.id,
        name: form.name.trim(),
        class: form.class,
        wiki_url: form.wiki_url.trim(),
        direct_rewards: !!form.direct_rewards,
        prereqs: form.prereqs,
        rewards: form.rewards.map((r) => ({
          kind: r.kind,
          name: r.name.trim(),
          faction_group: r.faction_group.trim(),
          faction_delta: Number(r.faction_delta) || 0,
          cycle: r.cycle.filter((c) => c.trim()),
        })),
        // Flatten the two role lists back into what the server stores.
        steps: form.steps.map((s) => ({
          kind: s.kind,
          tradeskill: s.tradeskill,
          skill_req: Number(s.skill_req) || 0,
          method: s.kind === "acquire" ? s.method : "",
          mobs: s.mobs.map((m) => ({ id: m.id, name: m.name, zone: m.zone })),
          zone_id: s.zone_id,
          // A blank pair means unrecorded, not 0,0 — which is a real spot.
          loc_y: Number(s.loc_y) || 0,
          loc_x: Number(s.loc_x) || 0,
          has_loc: !!s.has_loc,
          say: s.say.trim(),
          faction_level: s.faction_level,
          faction_group: s.faction_group.trim(),
          plat_cost: Math.max(0, Number(s.plat_cost) || 0),
          follows: !!s.follows,
          note: s.note.trim(),
          items: [
            // First alternative becomes name, the rest ride along as alts —
            // the server stores them as one slot either way. The flags travel
            // with the slot through the filter; indexing back into s.into
            // afterwards would misalign once an empty slot is dropped.
            ...s.into
              .map((i) => ({
                alts: i.alts.filter((a) => a.trim()),
                consumed_ok: i.consumed_ok !== false,
                consumed_fail: i.consumed_fail !== false,
              }))
              .filter((i) => i.alts.length)
              .map((i) => ({
                name: i.alts[0],
                alts: i.alts.slice(1),
                role: "in",
                consumed_ok: i.consumed_ok,
                consumed_fail: i.consumed_fail,
              })),
            ...s.out.map((o) => ({
              name: o.name,
              alts: [],
              role: "out",
              consumed_ok: true,
              consumed_fail: true,
            })),
          ],
        })),
      });
      form = null;
      await load();
    } catch (e) {
      err = String(e);
    }
    busy = false;
  }

  async function doDelete(q) {
    busy = true;
    err = "";
    try {
      await DeleteQuest(q.id);
      confirmDel = null;
      await load();
    } catch (e) {
      err = String(e);
    }
    busy = false;
  }

  function focusIt(node) {
    node.focus();
  }

  // One-line summary for a collapsed step card.
  function stepSummary(st) {
    const bits = [];
    if (st.mobs.length) bits.push(st.mobs.map((m) => m.name).join(" / "));
    if (st.zone_name || st.zone_id) bits.push(st.zone_name || st.zone_id);
    if (st.tradeskill) bits.push(st.tradeskill);
    const outs = st.out.map((o) => o.name).filter(Boolean);
    if (outs.length) bits.push("→ " + outs.join(", "));
    return bits.join(" · ") || "not filled in";
  }
</script>

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div
  class="q-ov"
  class:q-page={embedded}
  on:click|self={() => !embedded && onClose && onClose()}
>
  <div class="q-dlg" class:q-panel={embedded}>
    <!-- In tab mode the tab is already labelled "Quests", so the heading only
         earns its place once you're inside a quest. -->
    {#if form || !embedded}
      <div class="q-title">
        {form ? (form.id ? "Edit Quest" : "New Quest") : "Quests"}
      </div>
    {/if}

    {#if !form}
      <!-- Toolbar: filter left, actions right. In tab mode this is the page
           header, so the actions live here rather than below the list. -->
      <div class="q-bar">
        <input
          class="q-in q-filter"
          placeholder="Filter — name, class, item, NPC, zone or faction…"
          aria-label="Filter quests"
          bind:value={filter}
          disabled={!quests || !quests.length}
        />
        {#if canEdit}
          <button class="q-btn q-go" on:click={newQuest}>+ New Quest</button>
          <button class="q-btn" on:click={() => (showImport = true)}
            >Import…</button
          >
        {/if}
      </div>
      {#if quests && quests.length}
        <!-- Quick filters. Narrowing by class or slot is what you actually do
             with three hundred armor quests; the text box is for everything
             else. -->
        <div class="q-bar q-bar-2">
          <select
            class="q-in q-sel q-quick"
            aria-label="Filter by class"
            bind:value={fClass}
          >
            <option value="">Any class</option>
            {#each vocab.classes as c (c)}
              <option value={c}>{c}</option>
            {/each}
          </select>
          <select
            class="q-in q-sel q-quick"
            aria-label="Filter by reward slot"
            bind:value={fSlot}
            disabled={!slotOptions.length}
          >
            <option value="">Any slot</option>
            {#each slotOptions as s (s)}
              <option value={s}>{s}</option>
            {/each}
          </select>
          <button
            class="q-chipbtn"
            class:q-chipbtn-on={fEpic}
            aria-pressed={fEpic}
            title="Quests with “epic” in the name"
            on:click={() => (fEpic = !fEpic)}>Epic</button
          >
          <button
            class="q-chipbtn"
            class:q-chipbtn-on={fFaction}
            aria-pressed={fFaction}
            title="Quests whose only reward is faction standing"
            on:click={() => (fFaction = !fFaction)}>Faction only</button
          >
          <span class="q-count"
            >{shown ? shown.length : 0} of {quests.length}</span
          >
          {#if filtersOn}
            <button class="q-btn q-sm" on:click={clearFilters}>Clear</button>
          {/if}
        </div>
      {/if}
      {#if err}<div class="q-err">{err}</div>{/if}

      {#if quests === null}
        <div class="q-empty"><div class="q-big">Loading…</div></div>
      {:else if !quests.length}
        <div class="q-empty">
          <div class="q-big">No quests yet</div>
          <div class="q-hint">
            A quest is the whole walkthrough — every step, in order. Rewards are
            rarely auctioned, so their Magelo tooltip prices them by what the
            quest needs from outside. Start one, or import a scraped file.
          </div>
        </div>
      {:else if !shown.length}
        <div class="q-empty">
          <div class="q-big">
            {filter.trim() ? `Nothing matches “${filter.trim()}”` : "Nothing matches"}
          </div>
          <div class="q-hint">
            {quests.length} quest{quests.length === 1 ? "" : "s"} are recorded.
          </div>
          <button class="q-btn q-sm" on:click={clearFilters}>Clear filters</button>
        </div>
      {:else}
        <div class="q-list">
          {#each shown as q (q.id)}
            {@const ticked = q.steps.filter((_, i) => done.has(`${q.id}:${i}`))
              .length}
            <div class="q-row-wrap" class:q-open={openQuest === q.id}>
              <div class="q-row">
                <span class="q-caret">▶</span>
                <button
                  class="q-row-main"
                  on:click={() => (openQuest = openQuest === q.id ? 0 : q.id)}
                >
                  <div class="q-reward">{q.name}</div>
                  <div class="q-qname">
                    {q.class ? q.class + " · " : ""}{q.steps.length}
                    step{q.steps.length === 1 ? "" : "s"}{ticked
                      ? ` · ${ticked} done`
                      : ""} · {q.rewards.length
                      ? q.rewards.map(rewardText).join(" · ")
                      : "no reward set"}
                  </div>
                </button>
                {#if canEdit}
                  <button class="q-btn" on:click={() => editQuest(q)}
                    >Edit</button
                  >
                  <button class="q-btn q-del" on:click={() => (confirmDel = q)}
                    >Delete</button
                  >
                {/if}
              </div>

              {#if openQuest === q.id}
                <!-- Walkthrough. Ticks are per-machine and survive closing the
                     editor, so a twenty-step epic can be worked through over
                     weeks. -->
                <div class="q-walk" transition:slide|local={{ duration: 160 }}>
                  {#if q.wiki_url}
                    <a
                      class="q-wiki"
                      href={q.wiki_url}
                      target="_blank"
                      rel="noreferrer">{q.wiki_url}</a
                    >
                  {/if}
                  {#if q.prereqs.length}
                    <div class="q-note">
                      Requires first: {q.prereqs.map((p) => p.name).join(", ")}
                    </div>
                  {/if}
                  {#if !q.steps.length}
                    <div class="q-note">No steps recorded yet.</div>
                  {/if}
                  {#each q.steps as s, i}
                    <div class="q-check" class:q-ticked={done.has(`${q.id}:${i}`)}>
                      <input
                        type="checkbox"
                        aria-label="Step {i + 1} done"
                        checked={done.has(`${q.id}:${i}`)}
                        on:change={() => toggleDone(q, i)}
                      />
                      <span class="q-pos">{i + 1}</span>
                      <div class="q-checktext">
                        <!-- One sentence per step: dim connectives, bright
                             actors. Items keep their hover tooltips and say
                             lines their click-to-copy — a conversation is
                             typed one line at a time, so each line is its own
                             copy button. -->
                        <div class="q-line">
                          <span class="q-stepkind">{kindLabel(s)}</span>
                          {#each stepLine(s) as tk, k (k)}
                            {#if tk.t === "text"}
                              <span class="q-lt">{tk.s}</span>
                            {:else if tk.t === "sep"}
                              <span class="q-sep">{tk.s}</span>
                            {:else if tk.t === "item"}
                              <button
                                class="q-iname"
                                class:q-iname-out={tk.out}
                                on:mouseenter={(e) => showItemTip(e, tk.name)}
                                on:mousemove={moveItemTip}
                                on:mouseleave={hideItemTip}>{tk.name}</button
                              >
                            {:else if tk.t === "mult"}
                              <span class="q-mult">×{tk.n}</span>
                            {:else if tk.t === "ret"}
                              <span class="q-back">(returned)</span>
                            {:else if tk.t === "mob"}
                              <span class="q-hi">{tk.names.join(", ")}</span>
                            {:else if tk.t === "zone"}
                              <span class="q-zone">{tk.name}</span>
                              {#if tk.pt}
                                <button
                                  class="q-loc q-locbtn"
                                  title="Drop a map waypoint at {tk.pt.y}, {tk
                                    .pt.x} in {tk.name} — it clears itself after
                                  you've been there and zone out or camp"
                                  on:click|stopPropagation={() =>
                                    dropMarker(q, i, tk)}
                                  >⚑ {tk.pt.y}, {tk.pt.x}</button
                                >
                                {#if marked === `${q.id}:${i}`}
                                  <span class="q-markset">waypoint set</span>
                                {/if}
                              {/if}
                            {:else if tk.t === "skill"}
                              <span class="q-skill">({tk.s})</span>
                            {:else if tk.t === "plat"}
                              <span class="q-hi">{tk.n}pp</span>
                            {:else if tk.t === "gate"}
                              <span class="q-gate">requires {tk.s}</span>
                            {:else if tk.t === "say"}
                              <span class="q-says">
                                {#each sayLines(s.say) as line, sk (sk)}
                                  {#if sk > 0}<span class="q-saysep">→</span
                                    >{/if}
                                  <button
                                    class="q-say"
                                    class:q-said={copied ===
                                      `${q.id}:${i}:${sk}`}
                                    title="Click to copy"
                                    on:click|stopPropagation={() =>
                                      copySay(`${q.id}:${i}:${sk}`, line)}
                                    >“{line}”</button
                                  >
                                {/each}
                                {#if copied.startsWith(`${q.id}:${i}:`)}
                                  <span class="q-copied">copied</span>
                                {/if}
                              </span>
                            {/if}
                          {/each}
                          {#if autoTicked === `${q.id}:${i}`}
                            <!-- Say what just happened: boxes ticking themselves
                                 further up the list is otherwise startling. -->
                            <span class="q-autotick"
                              >+{autoTickedN} earlier step{autoTickedN === 1
                                ? ""
                                : "s"}</span
                            >
                          {/if}
                        </div>
                        {#if s.note}<div class="q-stepnote">{s.note}</div>{/if}
                      </div>
                    </div>
                  {/each}
                  {#if q.rewards.length}
                    <div class="q-fact q-rewardline">
                      <span class="q-fkey">Reward</span>
                      <span>
                        {#each q.rewards as r, k}
                          {#if k > 0}<span class="q-sep">·</span>{/if}
                          {#if r.kind === "item"}
                            <button
                              class="q-iname q-iname-out"
                              on:mouseenter={(e) => showItemTip(e, r.name)}
                              on:mousemove={moveItemTip}
                              on:mouseleave={hideItemTip}>{r.name}</button
                            >
                          {:else if r.kind === "cycle"}
                            {#each r.cycle || [] as c, ci}
                              {#if ci > 0}<span class="q-sep">→</span>{/if}
                              <button
                                class="q-iname q-iname-out"
                                on:mouseenter={(e) => showItemTip(e, c)}
                                on:mousemove={moveItemTip}
                                on:mouseleave={hideItemTip}>{c}</button
                              >
                            {/each}
                          {:else}
                            <span>{rewardText(r)}</span>
                          {/if}
                        {/each}
                      </span>
                    </div>
                  {/if}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}

      {#if !embedded}
        <div class="q-btns">
          <button class="q-btn" on:click={() => onClose && onClose()}
            >Close</button
          >
        </div>
      {/if}
    {:else}
      <!-- ── header ── -->
      <label class="q-label" for="q-name">Quest name</label>
      <input
        id="q-name"
        class="q-in"
        list="q-quest-names"
        placeholder="e.g. Monk Epic: Celestial Fists"
        bind:value={form.name}
        on:input={onNameInput}
      />
      <datalist id="q-quest-names">
        {#each questNames as n (n)}
          <option value={n}></option>
        {/each}
      </datalist>
      {#if nameMatch}
        <div class="q-note q-match">
          <span>A quest is already named “{nameMatch.name}”.</span>
          <button class="q-btn q-sm" on:click={() => editQuest(nameMatch)}
            >Load it</button
          >
        </div>
      {/if}

      <div class="q-grid2">
        <div>
          <div class="q-label">Class</div>
          <select class="q-in q-sel q-w" aria-label="Class" bind:value={form.class}>
            <option value="">Any</option>
            {#each vocab.classes as c (c)}
              <option value={c}>{c}</option>
            {/each}
          </select>
        </div>
        <div>
          <div class="q-label">Wiki link</div>
          <!-- What's stored here is a summary; the wiki page is the real
               reference and every quest has one. Fills itself from the name and
               vice versa — wiki page titles are the quest name with underscores
               — and stops doing so as soon as you type over it. -->
          <input
            class="q-in q-w"
            placeholder="https://wiki.project1999.com/…"
            aria-label="Wiki link"
            bind:value={form.wiki_url}
            on:input={onWikiInput}
          />
        </div>
      </div>

      <!-- Rewards that are bid on directly (Ring War loot) must never be
           priced by their components — the reconstruction answers the wrong
           question for event loot. -->
      <label
        class="q-direct"
        title="Price this quest's rewards by their own sale history only — never by adding up the component items."
      >
        <input type="checkbox" bind:checked={form.direct_rewards} />
        Direct-priced rewards (event loot — skip component pricing)
      </label>

      <!-- ── prerequisites ── -->
      <div class="q-head">
        <span class="q-label">Requires first</span>
        <button class="q-btn q-sm" on:click={() => (prereqPick = true)}
          >+ Prerequisite</button
        >
      </div>
      {#if form.prereqs.length}
        <div class="q-chips">
          {#each form.prereqs as p (p.id)}
            <span class="q-chip"
              >{p.name}
              <button
                class="q-x q-x-inline"
                aria-label="Remove {p.name}"
                on:click={() =>
                  (form.prereqs = form.prereqs.filter((x) => x.id !== p.id))}
                >×</button
              ></span
            >
          {/each}
        </div>
      {/if}

      <!-- ── steps ── -->
      <div class="q-head">
        <span class="q-label">Steps</span>
        <span class="q-adds">
          {#each vocab.step_kinds as k (k)}
            <button class="q-btn q-sm" on:click={() => addStep(k)}
              >+ {KIND_LABEL[k] || k}</button
            >
          {/each}
        </span>
      </div>

      {#each form.steps as st, i}
        {@const f = FIELDS[st.kind] || {}}
        <div class="q-card">
          <div class="q-card-head">
            <button
              class="q-expand"
              on:click={() => (openStep = openStep === i ? -1 : i)}
            >
              <span class="q-pos">{i + 1}</span>
              <span class="q-kind">{KIND_LABEL[st.kind] || st.kind}</span>
              <span class="q-sum">{stepSummary(st)}</span>
            </button>
            <button
              class="q-x q-x-inline"
              title="Move up"
              aria-label="Move step {i + 1} up"
              disabled={i === 0}
              on:click={() => moveStep(i, -1)}>↑</button
            >
            <button
              class="q-x q-x-inline"
              title="Move down"
              aria-label="Move step {i + 1} down"
              disabled={i === form.steps.length - 1}
              on:click={() => moveStep(i, 1)}>↓</button
            >
            <button
              class="q-x q-x-inline"
              aria-label="Remove step {i + 1}"
              on:click={() => removeStep(i)}>×</button
            >
          </div>

          {#if openStep === i}
            {#if f.method}
              <div class="q-label">How</div>
              <select
                class="q-in q-sel q-w"
                aria-label="How the item is acquired"
                value={st.method}
                on:change={(e) => {
                  st.method = e.currentTarget.value;
                  // A method with nobody in it can't keep an NPC: the field
                  // disappears, and a hidden value would still be saved.
                  if (!METHOD_MOB_LABEL[st.method]) st.mobs = [];
                  form = form;
                }}
              >
                {#each vocab.acquire_methods as m (m)}
                  <option value={m}>{METHOD_LABEL[m] || m}</option>
                {/each}
              </select>
            {/if}

            {#if wantsMob(st)}
              <div class="q-head">
                <span class="q-label">{mobLabel(st)}</span>
                {#if f.mob === "many"}
                  <button
                    class="q-btn q-sm"
                    disabled={st.mobs.length >= 20}
                    on:click={() =>
                      openMobPick("", (m) => {
                        if (st.mobs.some((x) => x.id === m.id)) return;
                        st.mobs = [
                          ...st.mobs,
                          { id: m.id, name: m.name, zone: m.zone_name || "" },
                        ];
                      })}>+ NPC</button
                  >
                {/if}
              </div>
              {#each st.mobs as m, k (m.id)}
                <div class="q-itemrow">
                  <button
                    class="q-slot"
                    on:click={() =>
                      openMobPick(m.name, (n) => {
                        st.mobs[k] = {
                          id: n.id,
                          name: n.name,
                          zone: n.zone_name || "",
                        };
                        // The faction a step gates on is usually the faction of
                        // the NPC involved, so fill it — but only when empty.
                        // An officer who typed something else meant it.
                        if (n.faction && f.faction && !st.faction_group.trim()) {
                          st.faction_group = n.faction;
                        }
                      })}
                    >{m.name}{m.zone ? ` · ${m.zone}` : ""}</button
                  >
                  <button
                    class="q-x q-x-inline"
                    title="Clear"
                    aria-label="Remove {m.name}"
                    on:click={() => {
                      st.mobs = st.mobs.filter((_, x) => x !== k);
                      form = form;
                    }}>×</button
                  >
                </div>
              {/each}
              <!-- A single-NPC kind gets a picker rather than an add button, so
                   there is nothing to press twice. -->
              {#if f.mob === "many" && dropNote && openStep === i}
                <div class="q-note q-dropnote">{dropNote}</div>
              {/if}
              {#if f.mob !== "many" && !st.mobs.length}
                <button
                  class="q-slot q-empty"
                  on:click={() =>
                    openMobPick("", (m) => {
                      st.mobs = [
                        { id: m.id, name: m.name, zone: m.zone_name || "" },
                      ];
                      if (m.faction && f.faction && !st.faction_group.trim()) {
                        st.faction_group = m.faction;
                      }
                    })}>Pick an NPC…</button
                >
              {/if}
            {/if}

            {#if f.zone}
              <div class="q-label">Zone</div>
              <input
                class="q-in"
                list="q-zones"
                placeholder="where to find it"
                aria-label="Zone"
                bind:value={st.zone_id}
              />
              <label class="q-chk">
                <input
                  type="checkbox"
                  checked={st.has_loc}
                  on:change={(e) => {
                    st.has_loc = e.currentTarget.checked;
                    if (!st.has_loc) st.loc_y = st.loc_x = 0;
                    form = form;
                  }}
                />
                Known spot
              </label>
              {#if st.has_loc}
                <!-- EQ /loc order, Y then X, as the game prints it and the
                     wiki quotes it. -->
                <div class="q-grid-fac">
                  <input
                    class="q-in"
                    type="number"
                    placeholder="loc Y"
                    aria-label="Loc Y"
                    bind:value={st.loc_y}
                  />
                  <input
                    class="q-in"
                    type="number"
                    placeholder="loc X"
                    aria-label="Loc X"
                    bind:value={st.loc_x}
                  />
                </div>
              {/if}
            {/if}

            {#if f.skill}
              <div class="q-grid2">
                <div>
                  <div class="q-label">Tradeskill</div>
                  <select
                    class="q-in q-sel q-w"
                    aria-label="Tradeskill"
                    bind:value={st.tradeskill}
                  >
                    <option value="">None</option>
                    {#each vocab.tradeskills as t (t)}
                      <option value={t}>{t}</option>
                    {/each}
                  </select>
                </div>
                <div>
                  <div class="q-label">Skill required</div>
                  <input
                    class="q-in q-w"
                    type="number"
                    min="0"
                    max="255"
                    aria-label="Skill required"
                    bind:value={st.skill_req}
                  />
                </div>
              </div>
            {/if}

            {#if f.say}
              <div class="q-label">What you say</div>
              <input
                class="q-in"
                placeholder="e.g. I will take this water to him"
                aria-label="What you say"
                bind:value={st.say}
              />
            {/if}

            {#if f.faction}
              <label class="q-chk">
                <input
                  type="checkbox"
                  checked={!!st.faction_level}
                  on:change={(e) => {
                    if (!e.currentTarget.checked) {
                      st.faction_level = "";
                      st.faction_group = "";
                    } else {
                      st.faction_level = vocab.factions[0] || "Ally";
                    }
                    form = form;
                  }}
                />
                Faction required
              </label>
              {#if st.faction_level}
                <div class="q-grid-fac">
                  <select
                    class="q-in q-sel"
                    aria-label="Faction level"
                    bind:value={st.faction_level}
                  >
                    {#each vocab.factions as fl (fl)}
                      <option value={fl}>{fl}</option>
                    {/each}
                  </select>
                  <input
                    class="q-in"
                    list="q-faction-groups"
                    placeholder="with… (e.g. Coldain)"
                    aria-label="Faction"
                    bind:value={st.faction_group}
                  />
                </div>
              {/if}
            {/if}

            {#if f.into}
              <div class="q-head">
                <span class="q-label"
                  >{st.kind === "combine" ? "Components" : "Hand in"}</span
                >
                <button
                  class="q-btn q-sm"
                  disabled={st.into.length >= 12}
                  on:click={() => {
                    st.into = [
                      ...st.into,
                      { alts: [""], consumed_ok: true, consumed_fail: true },
                    ];
                    form = form;
                    openItemPick(
                      "Item handed over",
                      "",
                      (n) => (st.into[st.into.length - 1].alts[0] = n),
                      earlierOutputs(i, slotNamesOf(st)),
                    );
                  }}>+ Item</button
                >
              </div>
              <!-- One block per SLOT. A slot normally holds one item; adding
                   alternatives makes any ONE of them satisfy it, which is how
                   the Essence Lens quest takes a talisman from any of four
                   dragons. Pricing uses whichever is cheapest. -->
              {#each st.into as it, j}
                <div class="q-slotgroup" class:q-alts={it.alts.length > 1}>
                  {#each it.alts as a, ai}
                    <div class="q-itemrow" class:q-altrow={ai > 0}>
                      {#if ai > 0}<span class="q-or">or</span>{/if}
                      <button
                        class="q-slot"
                        class:q-empty={!a}
                        on:click={() =>
                          openItemPick(
                            ai > 0 ? "Alternative item" : "Item handed over",
                            a,
                            (n) => (it.alts[ai] = n),
                            earlierOutputs(i, slotNamesOf(st)),
                          )}>{a || "Pick an item…"}</button
                      >
                      {#if ai === 0}
                        <label
                          class="q-chk q-tight"
                          title="Untick when the step hands this item straight back — the Enchanter's Jeb's Seal returns from all four masters. A returned item costs nothing."
                        >
                          <input type="checkbox" bind:checked={it.consumed_ok} />
                          {f.fail ? "Lost on success" : "Consumed"}
                        </label>
                        {#if f.fail}
                          <label
                            class="q-chk q-tight"
                            title="Whether a failed combine destroys this component. Recorded for reference — the DKP figure prices one successful combine."
                          >
                            <input
                              type="checkbox"
                              bind:checked={it.consumed_fail}
                            />
                            Lost on failure
                          </label>
                        {/if}
                        <button
                          class="q-btn q-sm"
                          title="Add an item that would satisfy this slot instead"
                          disabled={it.alts.length >= 10}
                          on:click={() => {
                            it.alts = [...it.alts, ""];
                            form = form;
                            openItemPick(
                              "Alternative item",
                              "",
                              (n) => (it.alts[it.alts.length - 1] = n),
                            );
                          }}>+ or</button
                        >
                      {/if}
                      <button
                        class="q-x q-x-inline"
                        aria-label={ai > 0
                          ? `Remove alternative ${ai}`
                          : `Remove item ${j + 1}`}
                        on:click={() => {
                          if (ai > 0) it.alts.splice(ai, 1);
                          else st.into.splice(j, 1);
                          form = form;
                        }}>×</button
                      >
                    </div>
                  {/each}
                </div>
              {/each}
            {/if}

            {#if f.plat}
              <div class="q-label">Platinum</div>
              <!-- Coin demanded alongside the items — Eldreth wants 100pp with
                   the Rogue parchment. Shown, never added to the DKP total. -->
              <input
                class="q-in q-narrow"
                type="number"
                min="0"
                aria-label="Platinum"
                bind:value={st.plat_cost}
              />
            {/if}

            {#if f.out}
              <div class="q-head">
                <span class="q-label">You receive</span>
                <button
                  class="q-btn q-sm"
                  disabled={st.out.length >= 12}
                  on:click={() => {
                    st.out = [...st.out, { name: "" }];
                    form = form;
                    openItemPick("Item received", "", (n) => {
                      st.out[st.out.length - 1].name = n;
                      fillDroppers(st, n);
                    });
                  }}>+ Item</button
                >
              </div>
              {#each st.out as o, j}
                <div class="q-itemrow">
                  <button
                    class="q-slot q-slot-reward"
                    class:q-empty={!o.name}
                    on:click={() =>
                      openItemPick("Item received", o.name, (n) => {
                        o.name = n;
                        fillDroppers(st, n);
                      })}>{o.name || "Pick an item…"}</button
                  >
                  <button
                    class="q-x q-x-inline"
                    aria-label="Remove received item {j + 1}"
                    on:click={() => {
                      st.out.splice(j, 1);
                      form = form;
                    }}>×</button
                  >
                </div>
              {/each}
              {#if !st.out.length}
                <div class="q-note">
                  Nothing received — this step spends its items to make
                  something happen.
                </div>
              {/if}
            {/if}

            {#if i > 0}
              <!-- What makes a character's progress traceable past a kill that
                   consumed nothing. Without it the walk backwards from an item
                   in their bags stops at the first spawn trigger. -->
              <label
                class="q-chk"
                title="Tick when this step was only possible because of the step above — a mob it spawned, a door it opened. Lets progress be traced back through it from an item you're carrying."
              >
                <input type="checkbox" bind:checked={st.follows} />
                Requires the step above
              </label>
            {/if}

            <div class="q-label">Note</div>
            <input
              class="q-in"
              placeholder="anything the wiki page won't tell you"
              aria-label="Note"
              bind:value={st.note}
            />
          {/if}
        </div>
      {/each}

      <!-- ── rewards ── -->
      <div class="q-head">
        <span class="q-label">Rewards</span>
        <span class="q-adds">
          {#each vocab.reward_kinds as k (k)}
            <button
              class="q-btn q-sm"
              disabled={form.rewards.length >= 8}
              on:click={() => addReward(k)}>+ {REWARD_LABEL[k] || k}</button
            >
          {/each}
        </span>
      </div>
      {#each form.rewards as r, i}
        <div class="q-card">
          <div class="q-card-head">
            <span class="q-kind">{REWARD_LABEL[r.kind] || r.kind}</span>
            <button
              class="q-x q-x-inline"
              aria-label="Remove reward {i + 1}"
              on:click={() => removeReward(i)}>×</button
            >
          </div>
          {#if r.kind === "item"}
            <button
              class="q-slot"
              class:q-empty={!r.name}
              on:click={() =>
                openItemPick("Reward item", r.name, (n) => (r.name = n))}
              >{r.name || "Pick an item…"}</button
            >
          {:else if r.kind === "faction"}
            <div class="q-grid-fac">
              <input
                class="q-in"
                type="number"
                aria-label="Faction change"
                title="How much the standing changes — negative to lose faction"
                bind:value={r.faction_delta}
              />
              <input
                class="q-in"
                list="q-faction-groups"
                placeholder="with… (e.g. Coldain)"
                aria-label="Faction"
                bind:value={r.faction_group}
              />
            </div>
          {:else}
            <div class="q-note">
              Items that are interchangeable outcomes of the same work — the
              final hand-in gives the first, and it can be traded up the list.
              All of them cost the same to reach.
            </div>
            {#each r.cycle as c, k}
              <div class="q-cycle">
                <span class="q-pos">{k + 1}</span>
                <button
                  class="q-slot"
                  class:q-empty={!c}
                  on:click={() =>
                    openItemPick("Cycle item", c, (n) => (r.cycle[k] = n))}
                  >{c || "Pick an item…"}</button
                >
                <button
                  class="q-x q-x-inline"
                  title="Move up"
                  aria-label="Move cycle item {k + 1} up"
                  disabled={k === 0}
                  on:click={() => moveCycle(r, k, -1)}>↑</button
                >
                <button
                  class="q-x q-x-inline"
                  title="Move down"
                  aria-label="Move cycle item {k + 1} down"
                  disabled={k === r.cycle.length - 1}
                  on:click={() => moveCycle(r, k, 1)}>↓</button
                >
                <button
                  class="q-x q-x-inline"
                  aria-label="Remove cycle item {k + 1}"
                  on:click={() => {
                    r.cycle.splice(k, 1);
                    form = form;
                  }}>×</button
                >
              </div>
            {/each}
            <button
              class="q-btn q-sm"
              on:click={() => {
                r.cycle = [...r.cycle, ""];
                form = form;
              }}>+ Item</button
            >
          {/if}
        </div>
      {/each}

      <!-- Shared by every faction field on the form. Ordered by the server:
           factions this guild's quests already use first. Free text, so one
           missing from the roster is still typeable. -->
      <datalist id="q-faction-groups">
        {#each factionGroups as g (g.name)}
          <option value={g.name}></option>
        {/each}
      </datalist>
      <!-- Values are zone ids because that is what eqmobs and quest_steps
           store; the label is the zone name, which is what an officer knows it
           by. -->
      <datalist id="q-zones">
        {#each zones || [] as z (z.id)}
          <option value={z.id}>{z.name}</option>
        {/each}
      </datalist>

      {#if formErr}<div class="q-note q-warn">{formErr}</div>{/if}
      {#if !formErr && formNote}<div class="q-note">{formNote}</div>{/if}
      {#if err}<div class="q-err">{err}</div>{/if}

      <div class="q-btns">
        <button class="q-btn" on:click={() => (form = null)}>Cancel</button>
        <button class="q-btn q-go" disabled={!!formErr || busy} on:click={save}
          >{busy ? "Saving…" : "Save Quest"}</button
        >
      </div>
    {/if}
  </div>
</div>

<!-- item picker -->
{#if pick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="q-ov q-ov-top" on:click|self={() => (pick = null)}>
    <div class="q-dlg q-dlg-sm">
      <div class="q-title">{pick.title}</div>
      <input
        class="q-in"
        placeholder="Search the item DB…"
        bind:value={pickQ}
        on:input={onPickInput}
        on:keydown={(e) =>
          pickerKey(
            e,
            pickRows.length,
            pickIdx,
            (n) => (pickIdx = n),
            (n) => chooseItem(pickRows[n]),
            () => (pick = null),
          )}
        use:focusIt
      />
      {#if pickQ.trim().length < 2 && (pick.suggest || []).length}
        <div class="q-note">From earlier steps of this quest:</div>
      {/if}
      <div class="q-sugs">
        {#each pickRows as s, i (s)}
          <button
            class="q-sug"
            class:q-sug-on={pickIdx === i}
            use:keepInView={pickIdx === i}
            on:mouseenter={() => (pickIdx = i)}
            on:click={() => chooseItem(s)}>{s}</button
          >
        {:else}
          <div class="q-note">
            {pickQ.trim().length < 2
              ? "Type at least two characters."
              : "No items match. Only items already in the DB can be used — add it with “Add Item…” first."}
          </div>
        {/each}
      </div>
      <div class="q-btns">
        <button class="q-btn" on:click={() => (pick = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- NPC picker: search the mob DB, or add a row that isn't in it -->
{#if mobPick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="q-ov q-ov-top" on:click|self={() => (mobPick = null)}>
    <div class="q-dlg q-dlg-sm">
      {#if !mobForm}
        <div class="q-title">NPC</div>
        <input
          class="q-in"
          placeholder="Search the mob DB…"
          bind:value={mobQ}
          on:input={onMobInput}
          on:keydown={(e) =>
            pickerKey(
              e,
              mobSugs.length,
              mobIdx,
              (n) => (mobIdx = n),
              (n) => chooseMob(mobSugs[n]),
              () => (mobPick = null),
            )}
          use:focusIt
        />
        <div class="q-sugs">
          {#each mobSugs as m, i (m.id)}
            {@const where = [m.zone_name || m.zone_id, m.faction]
              .filter(Boolean)
              .join(" · ")}
            <div class="q-mob" class:q-sug-on={mobIdx === i}>
              <button
                class="q-sug q-mob-pick"
                use:keepInView={mobIdx === i}
                on:mouseenter={() => (mobIdx = i)}
                on:click={() => chooseMob(m)}
              >
                <span class="q-mob-name">{m.name}</span>
                {#if where}<span class="q-mob-sub">{where}</span>{/if}
              </button>
              <button
                class="q-mob-edit"
                title="Edit this NPC"
                aria-label="Edit {m.name}"
                on:click={() => openMobForm(m)}>✎</button
              >
            </div>
          {:else}
            <div class="q-note">
              {mobQ.trim().length < 2
                ? "No NPCs are flagged as quest NPCs yet. Type a name to search the mob DB."
                : "No mobs match. Quest NPCs often drop nothing and are never parsed, so they may not be in the DB — add it below."}
            </div>
          {/each}
        </div>
        <div class="q-btns">
          <button class="q-btn q-go" on:click={() => openMobForm(null)}
            >+ Add NPC</button
          >
          <button class="q-btn" on:click={() => (mobPick = null)}>Cancel</button>
        </div>
      {:else}
        <div class="q-title">{mobForm.id ? "Edit NPC" : "Add NPC"}</div>
        <div class="q-note">
          This writes to the shared mob DB — the same records the parser and
          raid tracker use — so give it the NPC's exact in-game name.
        </div>

        <label class="q-label" for="q-mob-name">Name</label>
        <input
          id="q-mob-name"
          class="q-in"
          placeholder="e.g. Kirtan Skyrender"
          bind:value={mobForm.name}
          use:focusIt
        />

        <label class="q-label" for="q-mob-zone">Zone</label>
        <input
          id="q-mob-zone"
          class="q-in"
          list="q-zones"
          placeholder="optional — where the NPC stands"
          bind:value={mobForm.zone_id}
        />

        <label class="q-label" for="q-mob-faction">Faction</label>
        <input
          id="q-mob-faction"
          class="q-in"
          list="q-faction-groups"
          placeholder="optional — the faction this NPC holds"
          bind:value={mobForm.faction}
        />

        <label class="q-label" for="q-mob-nicks">Nicknames</label>
        <input
          id="q-mob-nicks"
          class="q-in"
          placeholder="optional — other names, separated by ::"
          bind:value={mobForm.nicknames}
        />

        <label class="q-chk">
          <input type="checkbox" bind:checked={mobForm.quest_mob} />
          Quest NPC — list this one first when picking
        </label>

        <label class="q-chk">
          <input
            type="checkbox"
            checked={mobForm.has_loc}
            on:change={(e) => {
              mobForm.has_loc = e.currentTarget.checked;
              if (!mobForm.has_loc) mobForm.loc_y = mobForm.loc_x = 0;
            }}
          />
          Stands in one known spot
        </label>
        {#if mobForm.has_loc}
          <!-- EQ /loc order, Y then X — "2777, 159" off the wiki page goes in
               left to right. -->
          <div class="q-grid-fac">
            <input
              class="q-in"
              type="number"
              placeholder="loc Y"
              aria-label="Loc Y"
              bind:value={mobForm.loc_y}
            />
            <input
              class="q-in"
              type="number"
              placeholder="loc X"
              aria-label="Loc X"
              bind:value={mobForm.loc_x}
            />
          </div>
        {:else}
          <div class="q-note">
            Leave off for a wanderer, or a mob type with many spawn points — a
            single marker would be a guess, so the map overlay is suppressed
            without one.
          </div>
        {/if}

        {#if mobErr}<div class="q-err">{mobErr}</div>{/if}
        {#if mobDup}
          <!-- Not an error. EQ reuses NPC names for genuinely different NPCs,
               and the epics turn that into a trap: the Monk epic's mad and sane
               Kaiaren share a zone, and handing to the wrong one destroys the
               item. Nothing has been written yet. -->
          <div class="q-note q-warn">
            {mobDup} Add a second one only if this is genuinely a different NPC —
            otherwise go back and pick the existing one.
          </div>
        {/if}
        <div class="q-btns">
          <button class="q-btn" on:click={() => (mobForm = null)}>Back</button>
          {#if mobDup}
            <button
              class="q-btn q-del"
              disabled={mobBusy}
              on:click={() => saveMob(true)}
              >{mobBusy ? "Saving…" : "Add anyway"}</button
            >
          {:else}
            <button
              class="q-btn q-go"
              disabled={mobBusy || !mobForm.name.trim()}
              on:click={() => saveMob(false)}
              >{mobBusy ? "Saving…" : "Save & Use"}</button
            >
          {/if}
        </div>
      {/if}
    </div>
  </div>
{/if}

<!-- prerequisite picker -->
{#if prereqPick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="q-ov q-ov-top" on:click|self={() => (prereqPick = false)}>
    <div class="q-dlg q-dlg-sm">
      <div class="q-title">Requires first</div>
      <div class="q-note">
        Another quest that must be done before this one. Recorded, not priced —
        where a prerequisite costs you something it does so through an item, and
        that item is already followed.
      </div>
      <input
        class="q-in"
        placeholder="Search quests…"
        bind:value={prereqQ}
        on:input={() => (prereqIdx = 0)}
        on:keydown={(e) =>
          pickerKey(
            e,
            prereqChoices.length,
            prereqIdx,
            (n) => (prereqIdx = n),
            (n) => addPrereq(prereqChoices[n]),
            () => (prereqPick = false),
          )}
        use:focusIt
      />
      <div class="q-sugs">
        {#each prereqChoices as q, i (q.id)}
          <button
            class="q-sug"
            class:q-sug-on={prereqIdx === i}
            use:keepInView={prereqIdx === i}
            on:mouseenter={() => (prereqIdx = i)}
            on:click={() => addPrereq(q)}
            >{q.name}{q.class ? " · " + q.class : ""}</button
          >
        {:else}
          <div class="q-note">No other quests to pick from.</div>
        {/each}
      </div>
      <div class="q-btns">
        <button class="q-btn" on:click={() => (prereqPick = false)}
          >Cancel</button
        >
      </div>
    </div>
  </div>
{/if}

{#if showImport}
  <QuestImport onClose={() => (showImport = false)} onImported={load} />
{/if}

<!-- Item card, the same one the Magelo sheet shows for inventory. -->
{#if tip}
  <div class="q-tip" style="left:{tip.x}px;top:{tip.y}px">
    <div class="q-tip-name">{tip.name}</div>
    {#if tip.item}
      {#each tipStats(tip.item) as l}{#if l === TIP_RULE}<div
            class="q-tip-rule"
          ></div>{:else}<div class="q-tip-line">{l}</div>{/if}{/each}
    {:else}
      <div class="q-tip-line q-tip-dim">
        Not in the item DB — it can't be used in a quest until it is.
      </div>
    {/if}
    {#if tipHolders.length}
      <div class="q-tip-rule"></div>
      <div
        class="q-tip-line q-tip-dim"
        title={tipHolders.map((h) => `${h.char}: ${h.where}`).join("\n")}
      >
        Also held by: {tipHolders
          .map((h) => h.char + (h.count > 1 ? ` ×${h.count}` : ""))
          .join(", ")}
      </div>
    {/if}
  </div>
{/if}

{#if confirmDel}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="q-ov q-ov-top" on:click|self={() => (confirmDel = null)}>
    <div class="q-dlg q-dlg-sm">
      <div class="q-title">Delete Quest</div>
      <div class="q-note">
        Delete <strong>{confirmDel.name}</strong>? Its steps and rewards go with
        it, and its rewards lose their pricing. Quests that chain off them keep
        working — they just stop resolving a value through this one.
      </div>
      <div class="q-btns">
        <button class="q-btn" on:click={() => (confirmDel = null)}
          >Cancel</button
        >
        <button
          class="q-btn q-del"
          disabled={busy}
          on:click={() => doDelete(confirmDel)}>Delete</button
        >
      </div>
    </div>
  </div>
{/if}

<style>
  .q-ov {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 200;
  }
  /* Pickers render after the main dialog and must paint above it. */
  .q-ov-top {
    z-index: 210;
  }
  .q-dlg {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    width: 560px;
    max-width: 94vw;
    max-height: 88vh;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .q-dlg-sm {
    width: 380px;
  }
  /* Tab mode: neutralise the modal chrome so the same markup fills the page,
     matching the other tabs — full height, one scrolling body. The pickers and
     the item card keep their own fixed overlays. */
  .q-page {
    position: static;
    background: none;
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
    z-index: auto;
  }
  .q-panel {
    flex: 1;
    width: 100%;
    max-width: none;
    max-height: none;
    border: none;
    background: none;
    border-radius: 0;
    padding: 10px 14px;
    gap: 10px;
  }
  /* The dialog gives the list its own scrollbox; as a tab the page scrolls
     instead, so a long quest isn't trapped in a short box. */
  .q-panel .q-list {
    max-height: none;
    overflow-y: visible;
  }
  /* ── toolbar ── */
  .q-bar {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .q-filter {
    flex: 1;
    min-width: 0;
  }
  .q-in:disabled {
    opacity: 0.5;
  }
  /* Second toolbar row: the quick filters. Wraps rather than squeezing the
     dropdowns to nothing in a narrow window. */
  .q-bar-2 {
    flex-wrap: wrap;
    gap: 6px;
  }
  .q-quick {
    width: auto;
    min-width: 110px;
    padding: 4px 6px;
    font-size: 11.5px;
  }
  /* A filter that's either on or off reads better as a toggle than as a
     checkbox with a label beside it. */
  .q-chipbtn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 4px 10px;
    font-size: 11.5px;
    cursor: pointer;
    transition:
      background 0.15s,
      border-color 0.15s,
      color 0.15s;
  }
  .q-chipbtn:hover {
    border-color: var(--border-hover);
    color: var(--text-primary);
  }
  .q-chipbtn-on,
  .q-chipbtn-on:hover {
    background: rgba(200, 169, 81, 0.14);
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .q-count {
    margin-left: auto;
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
  }

  /* ── empty states ── */
  .q-empty {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
    padding: 40px 0;
    text-align: center;
  }
  .q-big {
    color: var(--text-secondary);
    font-size: 15px;
    font-weight: 600;
  }
  .q-hint {
    font-size: 12px;
    color: var(--text-muted);
    max-width: 420px;
    line-height: 1.5;
  }
  .q-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--text-primary);
  }
  /* Matches the section titles the other tabs use. */
  .q-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .q-note {
    font-size: 11.5px;
    color: var(--text-muted);
    line-height: 1.5;
  }
  .q-warn {
    color: var(--accent);
  }
  .q-err {
    font-size: 12px;
    color: #ff6b6b;
  }
  .q-in {
    background: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 6px 8px;
    font-size: 13px;
  }
  /* The dropdown list itself is UA-rendered; without a dark color-scheme it
     paints white behind our light text. */
  .q-sel {
    color-scheme: dark;
  }
  .q-sel option {
    background: var(--bg-secondary);
    color: var(--text-primary);
  }
  .q-w {
    width: 100%;
    box-sizing: border-box;
  }
  .q-narrow {
    width: 100px;
  }
  .q-grid2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
    align-items: end;
  }
  /* Level and group read as one requirement, so they sit on one row. The group
     takes the remaining width — faction names run long. */
  .q-grid-fac {
    display: grid;
    grid-template-columns: 150px 1fr;
    gap: 6px;
  }
  /* A section title with its add buttons on the same line. */
  .q-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 4px;
  }
  .q-head .q-label {
    flex: 1;
  }
  .q-adds {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    justify-content: flex-end;
  }
  .q-match {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .q-match span {
    flex: 1;
    min-width: 0;
  }

  /* ── quest list ── */
  .q-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    max-height: 50vh;
    overflow-y: auto;
  }
  .q-row-wrap {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    transition:
      border-color 0.12s,
      background 0.12s;
  }
  .q-row-wrap:hover {
    border-color: var(--border-hover);
  }
  /* Expanded reads as selected, the same accent tint the other tabs use. */
  .q-row-wrap.q-open {
    border-color: var(--accent-dim);
    background: rgba(200, 169, 81, 0.06);
  }
  .q-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
  }
  /* The whole summary is the expand target, so the hit area is the row. */
  .q-row-main {
    flex: 1;
    min-width: 0;
    background: none;
    border: none;
    padding: 0;
    text-align: left;
    cursor: pointer;
  }
  .q-row-main:hover .q-reward {
    color: var(--text-primary);
  }
  /* A quiet caret that turns as the walkthrough opens — the row is otherwise
     indistinguishable from something that just sits there. */
  .q-caret {
    flex: none;
    color: var(--text-muted);
    font-size: 9px;
    transition: transform 0.12s;
  }
  .q-open .q-caret {
    transform: rotate(90deg);
    color: var(--accent);
  }

  /* ── walkthrough ── */
  .q-walk {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 2px 12px 10px 12px;
    /* Set apart from the summary row it belongs to. */
    border-top: 1px solid var(--border);
    margin-top: 2px;
  }
  .q-wiki {
    font-size: 11px;
    color: var(--text-secondary);
    word-break: break-all;
    text-decoration: none;
    padding: 2px 0 4px;
  }
  .q-wiki:hover {
    color: var(--accent);
    text-decoration: underline;
  }
  .q-check {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    font-size: 11.5px;
    color: var(--text-secondary);
    line-height: 1.45;
    padding: 4px 0;
    border-top: 1px solid rgba(255, 255, 255, 0.05);
  }
  .q-check input {
    margin-top: 3px;
    flex: none;
    cursor: pointer;
  }
  .q-check .q-pos {
    margin-top: 2px;
  }
  .q-checktext {
    flex: 1;
    min-width: 0;
  }
  /* Ticked steps recede rather than vanish, so the detail stays readable if
     you need to check it. */
  .q-ticked {
    opacity: 0.45;
  }
  .q-stepkind {
    flex: none;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
    margin-right: 2px;
  }
  /* The step as one sentence: dim connective words, bright actors. Flex with a
     gap supplies the inter-word spacing — Svelte trims the whitespace between
     the token blocks, so without it the sentence runs together. A size up from
     the old fact rows, and item names at full contrast: the sentence is the
     walkthrough now, not a detail panel. */
  .q-line {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    column-gap: 4px;
    row-gap: 2px;
    font-size: 12.5px;
    word-break: break-word;
  }
  .q-line .q-iname {
    font-size: 12.5px;
    color: var(--text-primary);
    font-weight: 600;
    border-bottom: 1px dotted var(--text-muted);
  }
  .q-line .q-say {
    font-size: 12px;
  }
  .q-line .q-loc {
    font-size: 11px;
  }
  /* the loc doubles as a "drop a waypoint" button */
  .q-locbtn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
  }
  .q-locbtn:hover {
    text-decoration: underline;
  }
  .q-markset {
    color: var(--success, #6fbf73);
    font-size: 10.5px;
  }
  /* Connectives sit one shade below the actors, not two — muted was
     unreadable against the panel background. The who/where/what each get
     their own treatment so a line parses at a glance: mobs bright and bold,
     zones in accent, items underlined (those are the hoverable ones). */
  .q-lt {
    color: var(--text-secondary);
  }
  .q-hi {
    color: var(--text-primary);
    font-weight: 700;
  }
  .q-zone {
    color: var(--accent);
    font-weight: 600;
  }
  .q-skill {
    color: var(--text-secondary);
    font-size: 10.5px;
  }
  /* the direct-priced-rewards toggle on the quest form */
  .q-direct {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11.5px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .q-direct input {
    accent-color: var(--accent);
  }
  /* One labelled fact per line — the label column keeps them scannable down
     the left rather than running together into prose. */
  .q-fact {
    display: flex;
    gap: 6px;
    font-size: 11px;
    margin-top: 1px;
  }
  .q-fkey {
    flex: none;
    width: 62px;
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .q-sep {
    color: var(--text-muted);
    margin: 0 3px;
    font-size: 10.5px;
  }
  .q-back {
    color: var(--text-muted);
    font-size: 10px;
    margin-left: 3px;
  }
  /* How many of the same item the step wants. Tight to the name it counts,
     and accented so it doesn't read as part of the name. */
  .q-mult {
    color: var(--accent);
    font-size: 11px;
    margin-left: 2px;
  }
  /* Only shown when the spot is unambiguous — see stepPoint. */
  .q-loc {
    margin-left: 6px;
    font-size: 10px;
    color: var(--accent);
    font-variant-numeric: tabular-nums;
  }
  .q-gate {
    color: var(--accent);
  }
  .q-stepnote {
    font-size: 10.5px;
    color: var(--text-muted);
    font-style: italic;
    margin-top: 2px;
  }
  .q-rewardline {
    border-top: 1px solid rgba(255, 255, 255, 0.05);
    padding-top: 5px;
    margin-top: 2px;
  }
  /* Item names are hover targets for the stat card, so they read as such. */
  .q-iname {
    background: none;
    border: none;
    padding: 0;
    font-size: 11px;
    color: var(--text-secondary);
    cursor: help;
    text-decoration: underline dotted rgba(255, 255, 255, 0.25);
    text-underline-offset: 2px;
  }
  .q-iname:hover {
    color: var(--accent);
  }
  .q-iname-out {
    color: var(--accent);
  }
  /* A conversation wraps as a sequence of separate copy targets. */
  .q-says {
    display: inline;
  }
  .q-saysep {
    color: var(--text-muted);
    margin: 0 3px;
  }
  .q-say {
    background: none;
    border: none;
    padding: 0 2px;
    border-radius: 3px;
    font-size: 11px;
    font-style: italic;
    color: var(--text-secondary);
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
  .q-copied {
    margin-left: 6px;
    font-style: normal;
    font-size: 10px;
    color: var(--accent);
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

  /* ── item card ── */
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
  /* Splits the item's stats from what it costs — two different kinds of fact
     that otherwise run together as one wall of lines. */
  .q-tip-rule {
    height: 1px;
    margin: 5px 0 4px;
    background: rgba(255, 255, 255, 0.14);
  }
  .q-tip-dim {
    color: var(--text-muted);
    font-style: italic;
  }
  .q-reward {
    font-size: 12.5px;
    font-weight: 600;
    color: var(--accent);
    word-break: break-word;
    transition: color 0.12s;
  }
  .q-qname {
    font-size: 10.5px;
    color: var(--text-muted);
    margin-top: 1px;
  }

  /* ── step & reward cards ── */
  .q-card {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 9px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    transition: border-color 0.12s;
  }
  .q-card:hover {
    border-color: var(--border-hover);
  }
  .q-card-head {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  /* The whole summary line is the expand target — a 20-step quest is mostly
     collapsed, so the hit area should be the row, not a chevron. */
  .q-expand {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 7px;
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    text-align: left;
  }
  .q-pos {
    flex: none;
    width: 18px;
    text-align: center;
    font-size: 10.5px;
    color: var(--text-muted);
    border: 1px solid var(--border);
    border-radius: 3px;
  }
  .q-kind {
    flex: none;
    font-size: 11.5px;
    font-weight: 600;
    color: var(--text-primary);
  }
  .q-sum {
    flex: 1;
    min-width: 0;
    font-size: 11px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .q-expand:hover .q-kind {
    color: var(--accent);
  }

  /* ── item rows ── */
  /* Flex, not grid: a row carries a variable set of controls — consumed, lost
     on failure, "+ or" — and only the item name should absorb the slack. */
  .q-itemrow {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .q-itemrow .q-slot {
    flex: 1;
    min-width: 0;
  }
  /* A slot and its alternatives read as one requirement, so the rule down the
     left binds them together. */
  .q-slotgroup {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .q-alts {
    border-left: 2px solid var(--border);
    padding-left: 6px;
  }
  .q-altrow {
    padding-left: 12px;
  }
  .q-or {
    flex: none;
    font-size: 10.5px;
    font-style: italic;
    color: var(--text-muted);
  }
  /* Transient confirmation that the dropper lookup ran and what it found. */
  .q-dropnote {
    color: var(--accent);
  }
  .q-cycle {
    display: grid;
    grid-template-columns: 18px 1fr 20px 20px 20px;
    gap: 6px;
    align-items: center;
  }
  .q-slot {
    flex: 1;
    min-width: 0;
    text-align: left;
    background: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 6px 8px;
    font-size: 12.5px;
    cursor: pointer;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .q-slot:hover {
    border-color: var(--accent);
  }
  .q-slot-reward {
    border-color: var(--accent);
    color: var(--accent);
  }
  .q-slot.q-empty {
    color: var(--text-muted);
    font-style: italic;
  }
  .q-chk {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .q-tight {
    font-size: 10.5px;
    white-space: nowrap;
  }
  .q-x {
    position: absolute;
    right: 4px;
    top: 50%;
    transform: translateY(-50%);
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 14px;
    line-height: 1;
    cursor: pointer;
    padding: 2px 4px;
  }
  .q-x:hover {
    color: #ff6b6b;
  }
  /* In a grid cell rather than overlaid on the field beside it. */
  .q-x-inline {
    position: static;
    transform: none;
  }
  .q-x-inline:disabled {
    opacity: 0.25;
    cursor: default;
  }
  .q-x-inline:disabled:hover {
    color: var(--text-muted);
  }

  /* ── chips ── */
  .q-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }
  .q-chip {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    font-size: 11.5px;
    color: var(--text-secondary);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2px 4px 2px 10px;
  }

  /* ── suggestions ── */
  .q-sugs {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 300px;
    overflow-y: auto;
  }
  .q-sug {
    text-align: left;
    background: none;
    border: none;
    color: var(--text-primary);
    font-size: 12.5px;
    padding: 5px 7px;
    border-radius: 4px;
    cursor: pointer;
  }
  .q-sug:hover {
    background: rgba(255, 255, 255, 0.07);
    color: var(--accent);
  }
  /* Keyboard highlight. Stronger than :hover so it stays readable when the
     cursor happens to be resting on a different row. */
  .q-sug-on,
  .q-sug-on .q-sug,
  .q-sug-on:hover {
    background: rgba(200, 169, 81, 0.16);
    color: var(--accent);
  }
  .q-mob {
    display: flex;
    align-items: stretch;
    gap: 2px;
  }
  .q-mob-pick {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .q-mob-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .q-mob-sub {
    font-size: 10.5px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .q-mob-edit {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 12px;
    padding: 0 7px;
    border-radius: 4px;
    cursor: pointer;
  }
  .q-mob-edit:hover {
    background: rgba(255, 255, 255, 0.07);
    color: var(--accent);
  }

  /* ── buttons ── */
  .q-btns {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .q-btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 6px 14px;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
    transition:
      background 0.15s,
      border-color 0.15s,
      color 0.15s;
  }
  .q-btn:hover {
    background: var(--bg-input);
    border-color: var(--border-hover);
    color: var(--text-primary);
  }
  .q-sm {
    padding: 3px 9px;
    font-size: 11.5px;
    white-space: nowrap;
  }
  .q-go {
    border-color: var(--accent);
    color: var(--accent);
  }
  .q-go:hover {
    border-color: var(--accent);
    color: var(--accent);
  }
  .q-del {
    border-color: #a04a4a;
    color: #e07b7b;
  }
  .q-del:hover {
    border-color: #c25a5a;
    color: #f09090;
  }
  .q-btn:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
