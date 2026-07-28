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
  } from "../../bindings/FuseBridge/app.js";
  import { scale } from "./scale.js";
  import { tipStats } from "./itemTip.js";
  import QuestImport from "./QuestImport.svelte";

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
  const KIND_LABEL = {
    handin: "Hand In",
    combine: "Tradeskill combine",
    loot: "Loot",
    acquire: "Acquire",
    dialogue: "Dialogue",
  };
  // How an acquire step reads once the method is known. The label is the step's
  // heading; the verb is the walkthrough sentence ("Pick the pocket of …").
  const METHOD_LABEL = {
    ground: "Ground spawn",
    forage: "Forage",
    fish: "Fish",
    purchase: "Purchase",
    pickpocket: "Pickpocket",
  };
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
  // Free-text filter over the list. Fourteen class epics is a lot of rows.
  let filter = "";

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

  $: shown =
    quests === null
      ? null
      : filter.trim()
        ? quests.filter((q) => haystack(q).includes(filter.trim().toLowerCase()))
        : quests;

  function rewardText(r) {
    if (r.kind === "faction") {
      return `${r.faction_delta > 0 ? "+" : ""}${r.faction_delta} ${r.faction_group}`;
    }
    if (r.kind === "cycle") return (r.cycle || []).join(" → ");
    return r.name;
  }

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

  function stepIns(s) {
    return (s.items || []).filter((i) => i.role !== "out");
  }
  function stepOuts(s) {
    return (s.items || []).filter((i) => i.role === "out");
  }
  // Every item that satisfies one slot, joined — "Yelinak's Talisman or
  // Lendiniara's Talisman or …".
  function slotNames(i) {
    return [i.name, ...(i.alts || [])];
  }
  // Where the mobs are, when they agree on a zone. Loot steps usually name
  // several mobs in one zone, and repeating it per mob is noise.
  function mobZone(s) {
    const zones = [...new Set((s.mobs || []).map((m) => m.zone).filter(Boolean))];
    return zones.length === 1 ? zones[0] : "";
  }

  // The single spot a step happens at, or null.
  //
  // Deliberately null whenever there is more than one candidate: several mobs
  // can drop the item, or the one mob wanders and has no recorded spot. A map
  // marker asserts "it is HERE", and drawing one of four possible places — or
  // one point for a mob that roams the zone — would be a guess dressed as a
  // fact. No marker is the honest answer; the zone name still tells you where
  // to go.
  function stepPoint(s) {
    if (s.has_loc) return { y: s.loc_y, x: s.loc_x, what: "here" };
    const located = (s.mobs || []).filter((m) => m.has_loc);
    if ((s.mobs || []).length === 1 && located.length === 1) {
      return { y: located[0].loc_y, x: located[0].loc_x, what: located[0].name };
    }
    return null;
  }
  // What to call a step. Acquire is a family rather than a single action, so it
  // shows the specific one — "Pickpocket", "Purchase" — and falls back to the
  // bare kind only when the method wasn't recorded.
  function kindLabel(s) {
    if (s.kind === "acquire" && METHOD_LABEL[s.method]) {
      return METHOD_LABEL[s.method];
    }
    return KIND_LABEL[s.kind] || s.kind;
  }
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
  // The headline for a step: what it gets you, or what it does when it gets you
  // nothing (a spawn trigger).
  function stepHead(s) {
    const outs = stepOuts(s).map((o) => o.name);
    if (outs.length) return outs.join(", ");
    switch (s.kind) {
      case "handin":
        return "hand in — nothing received";
      case "dialogue":
        return "talk — nothing received";
      default:
        return kindLabel(s);
    }
  }

  // A dialogue step is often a whole conversation, written as one field with
  // the replies separated by an arrow: "I will take it → What task? → Agreed".
  // Those are typed into the game ONE AT A TIME, so the chain is what to show
  // and a single line is what to copy — copying the joined string hands over
  // something no NPC will ever answer to.
  //
  // "->" is accepted alongside "→" because it's what gets typed when a quest is
  // entered by hand.
  function sayLines(text) {
    return String(text || "")
      .split(/\s*(?:→|->)\s*/)
      .map((s) => s.trim())
      .filter(Boolean);
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

  function newQuest() {
    form = {
      id: 0,
      name: "",
      class: "",
      wiki_url: "",
      prereqs: [],
      rewards: [],
      steps: [],
    };
    openStep = -1;
    closeAll();
  }

  function editQuest(q) {
    form = {
      id: q.id,
      name: q.name || "",
      class: q.class || "",
      wiki_url: q.wiki_url || "",
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

  let pick = null; // { title, apply(name) }
  let pickQ = "";
  let pickSugs = [];
  let pickTimer;

  function openItemPick(title, current, apply) {
    pick = { title, apply };
    pickQ = current || "";
    pickSugs = [];
    onPickInput();
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
    const q = pickQ.trim();
    if (q.length < 2) {
      pickSugs = [];
      return;
    }
    pickTimer = setTimeout(() => {
      // No slot/class/race filter — a quest item can be anything, including
      // components no character can equip.
      SearchItems(q, "", "", "")
        .then((names) => (pickSugs = names || []))
        .catch(() => (pickSugs = []));
    }, 250);
  }

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
    const q = mobQ.trim();
    // No minimum: a short query is what asks for the known roster.
    mobTimer = setTimeout(() => {
      SearchQuestMobs(q)
        .then((mobs) => (mobSugs = mobs || []))
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
        <button class="q-btn q-go" on:click={newQuest}>+ New Quest</button>
        <button class="q-btn" on:click={() => (showImport = true)}>Import…</button>
      </div>
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
          <div class="q-big">Nothing matches “{filter}”</div>
          <div class="q-hint">
            {quests.length} quest{quests.length === 1 ? "" : "s"} are recorded.
          </div>
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
                <button class="q-btn" on:click={() => editQuest(q)}>Edit</button>
                <button class="q-btn q-del" on:click={() => (confirmDel = q)}
                  >Delete</button
                >
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
                    {@const zone = mobZone(s) || s.zone_name || s.zone_id}
                    <div class="q-check" class:q-ticked={done.has(`${q.id}:${i}`)}>
                      <input
                        type="checkbox"
                        aria-label="Step {i + 1} done"
                        checked={done.has(`${q.id}:${i}`)}
                        on:change={() => toggleDone(q, i)}
                      />
                      <span class="q-pos">{i + 1}</span>
                      <div class="q-checktext">
                        <div class="q-stephead">
                          <span class="q-stepkind">{kindLabel(s)}</span>
                          <span class="q-stepwhat">{stepHead(s)}</span>
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

                        {#if s.mobs && s.mobs.length}
                          <div class="q-fact">
                            <span class="q-fkey">{mobLabel(s)}</span>
                            <span>{s.mobs.map((m) => m.name).join(", ")}</span>
                          </div>
                        {/if}
                        {#if zone}
                          {@const pt = stepPoint(s)}
                          <div class="q-fact">
                            <span class="q-fkey">Zone</span>
                            <span
                              >{zone}{#if pt}<span class="q-loc"
                                  >{pt.y}, {pt.x}</span
                                >{/if}</span
                            >
                          </div>
                        {/if}
                        {#if s.tradeskill || s.skill_req}
                          <div class="q-fact">
                            <span class="q-fkey">Skill</span>
                            <span
                              >{s.tradeskill || "any"}{s.skill_req
                                ? ` ${s.skill_req}`
                                : ""}</span
                            >
                          </div>
                        {/if}
                        {#if s.say}
                          <!-- Click to copy: these get typed into the game
                               verbatim, and getting them wrong wastes a trip.
                               One button per reply, because a conversation is
                               typed a line at a time — copying the whole chain
                               gives you something no NPC answers to. -->
                          <div class="q-fact">
                            <span class="q-fkey">Say</span>
                            <span class="q-says">
                              {#each sayLines(s.say) as line, k (k)}
                                {#if k > 0}<span class="q-saysep">→</span>{/if}
                                <button
                                  class="q-say"
                                  class:q-said={copied === `${q.id}:${i}:${k}`}
                                  title="Click to copy"
                                  on:click|stopPropagation={() =>
                                    copySay(`${q.id}:${i}:${k}`, line)}
                                  >“{line}”</button
                                >
                              {/each}
                              {#if copied.startsWith(`${q.id}:${i}:`)}
                                <span class="q-copied">copied</span>
                              {/if}
                            </span>
                          </div>
                        {/if}
                        {#if s.faction_level}
                          <div class="q-fact">
                            <span class="q-fkey">Faction</span>
                            <span class="q-gate"
                              >{s.faction_level} with {s.faction_group}</span
                            >
                          </div>
                        {/if}
                        {#if stepIns(s).length || s.plat_cost}
                          <div class="q-fact">
                            <span class="q-fkey"
                              >{s.kind === "combine" ? "Combine" : "Give"}</span
                            >
                            <span>
                              {#each stepIns(s) as it, k}
                                {#if k > 0}<span class="q-sep">+</span>{/if}
                                {#each slotNames(it) as nm, ai}
                                  {#if ai > 0}<span class="q-sep">or</span>{/if}
                                  <button
                                    class="q-iname"
                                    on:mouseenter={(e) => showItemTip(e, nm)}
                                    on:mousemove={moveItemTip}
                                    on:mouseleave={hideItemTip}>{nm}</button
                                  >
                                {/each}
                                {#if it.consumed_ok === false}<span class="q-back"
                                    >(returned)</span
                                  >{/if}
                              {/each}
                              {#if s.plat_cost}
                                {#if stepIns(s).length}<span class="q-sep">+</span
                                  >{/if}<span>{s.plat_cost}pp</span>
                              {/if}
                            </span>
                          </div>
                        {/if}
                        {#if stepOuts(s).length}
                          <div class="q-fact">
                            <span class="q-fkey">Receive</span>
                            <span>
                              {#each stepOuts(s) as o, k}
                                {#if k > 0}<span class="q-sep">+</span>{/if}
                                <button
                                  class="q-iname q-iname-out"
                                  on:mouseenter={(e) => showItemTip(e, o.name)}
                                  on:mousemove={moveItemTip}
                                  on:mouseleave={hideItemTip}>{o.name}</button
                                >
                              {/each}
                            </span>
                          </div>
                        {/if}
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
               reference and every quest has one. -->
          <input
            class="q-in q-w"
            placeholder="https://wiki.project1999.com/…"
            aria-label="Wiki link"
            bind:value={form.wiki_url}
          />
        </div>
      </div>

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
        use:focusIt
      />
      <div class="q-sugs">
        {#each pickSugs as s (s)}
          <button class="q-sug" on:click={() => chooseItem(s)}>{s}</button>
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
          use:focusIt
        />
        <div class="q-sugs">
          {#each mobSugs as m (m.id)}
            {@const where = [m.zone_name || m.zone_id, m.faction]
              .filter(Boolean)
              .join(" · ")}
            <div class="q-mob">
              <button class="q-sug q-mob-pick" on:click={() => chooseMob(m)}>
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
        use:focusIt
      />
      <div class="q-sugs">
        {#each prereqChoices as q (q.id)}
          <button class="q-sug" on:click={() => addPrereq(q)}
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
      {#each tipStats(tip.item) as l}<div class="q-tip-line">{l}</div>{/each}
    {:else}
      <div class="q-tip-line q-tip-dim">
        Not in the item DB — it can't be used in a quest until it is.
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
  /* Ticked steps recede rather than vanish — struck through only on the
     headline, so the detail stays readable if you need to check it. */
  .q-ticked {
    opacity: 0.45;
  }
  .q-ticked .q-stepwhat {
    text-decoration: line-through;
  }
  .q-stephead {
    display: flex;
    align-items: baseline;
    gap: 6px;
  }
  .q-stepkind {
    flex: none;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }
  .q-stepwhat {
    font-weight: 600;
    color: var(--text-primary);
    word-break: break-word;
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
