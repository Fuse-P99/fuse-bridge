<script>
  // Quest turn-in editor (admin mode). A quest links the component items you
  // hand in to the item (or items) you get back. Raid gear is handed out this
  // way rather than auctioned, so the reward has no DKP history of its own —
  // recording the components is what lets the Magelo tooltip price it.
  //
  // Turn-ins chain: a reward here is routinely a turn-in of the next quest.
  // Nothing special is needed to express that — enter each step as its own
  // quest and the server follows the links when it prices them.
  //
  // Every field is an item NAME resolved against the server's item DB on save.
  // A name that isn't in the DB fails the whole save rather than storing a
  // recipe that would price nothing, so the picker only offers real rows.
  import { onMount } from "svelte";
  import {
    ListQuests,
    ListQuestFactions,
    SaveQuest,
    DeleteQuest,
    SearchItems,
    SearchQuestMobs,
    SaveQuestMob,
    ListQuestZones,
  } from "../../bindings/FuseBridge/app.js";

  export let onClose;

  // Mirror maxQuestComponents / maxQuestRewards on the server.
  const MAX_ITEMS = 6;
  const MAX_REWARDS = 6;

  // Minimum standing the turn-in requires. Mirrors questFactions in quests.go
  // (best first) — both sides must agree or the server rejects the save. Only
  // the con word is stored; the numeric bands behind each are a game mechanic
  // this doesn't model. "" is None, the default.
  const FACTIONS = [
    "Ally",
    "Warmly",
    "Kindly",
    "Amiable",
    "Indifferent",
    "Apprehensive",
    "Dubious",
    "Threatening",
    "Scowls",
  ];

  // Mirrors questClasses in quests.go. Every class epic is class-locked; this
  // is mainly how a forty-step chain gets grouped, since the components are
  // class-locked items anyway. "" is Any.
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

  // Mirrors questStepKinds in quests.go. Not every step is a hand-in: the
  // Warrior's dragon-head hilts are picked up off the ground, the Ranger's
  // roots are foraged, the Enchanter combines four items in a sack five
  // separate times. The kind drives the labels below and nothing else — a step
  // is priced by what it consumes regardless of how you reach it.
  const STEP_KINDS = [
    { id: "handin", label: "Hand-in", npc: "Hand in to" },
    { id: "combine", label: "Tradeskill combine", npc: "Combined by" },
    { id: "kill", label: "Kill / loot", npc: "Dropped by" },
    { id: "ground", label: "Ground spawn / forage", npc: "Found near" },
    { id: "dialogue", label: "Dialogue", npc: "Talk to" },
  ];

  let quests = null; // null = loading
  let err = "";
  let busy = false;
  // Free-text filter over the list. A class epic is twenty-odd steps and there
  // are fourteen of them, so scrolling stops being a way to find anything.
  let filter = "";

  // form is null on the list view, otherwise the recipe being edited. Both
  // lists are held at full length so the slots render as a fixed grid; blanks
  // are dropped on save.
  let form = null;
  let confirmDel = null; // quest pending delete confirmation

  // Which slot the picker is filling: {list: "rewards"|"items", i} or null.
  let pick = null;
  let pickQ = "";
  let pickSugs = [];
  let pickTimer;

  // Faction-group suggestions, already ordered by the server: the groups this
  // guild's quests actually use first, then the rest of the roster
  // alphabetically. A <datalist> renders them in document order and still
  // allows free text, so a faction missing from the list is typed, not blocked.
  let factionGroups = [];

  // ── hand-in NPC ────────────────────────────────────────────────────────────
  // The NPC is an eqmobs row, the same table the parser and raid tracker use.
  // The picker searches it and can add a row that isn't there yet, because the
  // mob DB is populated from loot and parses — a turn-in NPC that drops nothing
  // and is never fought may genuinely be missing.
  let mobPick = false;
  let mobQ = "";
  let mobSugs = [];
  let mobTimer;
  let mobBusy = false;
  let mobErr = "";
  // null = browsing the search results; an object = the add/edit NPC form.
  let mobForm = null;
  // Zones are fetched once, on first use of the NPC form, rather than on mount:
  // most editing sessions never open it.
  let zones = null;

  onMount(load);

  async function load() {
    err = "";
    try {
      quests = (await ListQuests()) || [];
    } catch (e) {
      quests = [];
      err = String(e);
    }
    // Ordering depends on the quest counts, so refresh it alongside the list
    // rather than once on mount — saving a quest changes what should be on top.
    try {
      factionGroups = (await ListQuestFactions()) || [];
    } catch {
      /* autocomplete is a convenience; the field still accepts free text */
    }
  }

  // Everything a quest is identifiable by, lowercased once per row.
  function haystack(q) {
    return [
      q.name,
      q.class,
      q.mob_name,
      q.faction,
      q.faction_group,
      ...(q.rewards || []),
      ...(q.items || []).map((t) => t.name),
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

  // Distinct names already in use, for the name field's dropdown. Steps of one
  // epic tend to share a naming scheme, so seeing the existing ones is most of
  // what keeps forty of them consistent.
  $: questNames = [...new Set((quests || []).map((q) => q.name).filter(Boolean))].sort(
    (a, b) => a.localeCompare(b),
  );

  // The quest the typed name already belongs to, when it isn't the open one.
  // Offered as an explicit load rather than swapping the form out underneath
  // you — picking from an autocomplete shouldn't discard half a step.
  $: nameMatch =
    form && form.name.trim()
      ? (quests || []).find(
          (q) =>
            q.id !== form.id &&
            q.name &&
            q.name.toLowerCase() === form.name.trim().toLowerCase(),
        ) || null
      : null;

  function padded(list, n) {
    const out = (list || []).slice(0, n);
    while (out.length < n) out.push("");
    return out;
  }

  // One slot per trade slot. Hand-ins take unstacked items, so a step wanting
  // two of something just fills two slots with it — there is no quantity, which
  // is why repeats are allowed here and rejected among the rewards. Slots carry
  // a consumed flag, so they are objects rather than bare names; blank-named
  // ones are dropped on save.
  function paddedItems(list, n) {
    const out = (list || []).slice(0, n).map((t) => ({
      name: t.name || "",
      consumed: t.consumed !== false,
    }));
    while (out.length < n) out.push({ name: "", consumed: true });
    return out;
  }

  function newQuest() {
    form = {
      id: 0,
      name: "",
      faction: "",
      faction_group: "",
      mob_id: 0,
      mob_name: "",
      class: "",
      step_kind: "handin",
      plat_cost: 0,
      rewards: padded([], MAX_REWARDS),
      items: paddedItems([], MAX_ITEMS),
    };
    closePick();
  }

  function editQuest(q) {
    form = {
      id: q.id,
      name: q.name || "",
      faction: q.faction || "",
      faction_group: q.faction_group || "",
      mob_id: q.mob_id || 0,
      mob_name: q.mob_name || "",
      class: q.class || "",
      step_kind: q.step_kind || "handin",
      plat_cost: q.plat_cost || 0,
      rewards: padded(q.rewards, MAX_REWARDS),
      items: paddedItems(q.items, MAX_ITEMS),
    };
    closePick();
  }

  function closePick() {
    pick = null;
    pickQ = "";
    pickSugs = [];
    clearTimeout(pickTimer);
  }

  // Rewards are bare names, turn-ins are objects; both are addressed by slot,
  // so the name lives behind these two.
  function slotName(list, i) {
    const v = form[list][i];
    return list === "items" ? v.name : v;
  }

  function setSlotName(list, i, name) {
    if (list === "items") form[list][i].name = name;
    else form[list][i] = name;
    form = form;
  }

  // Opening a slot seeds the box with whatever is already in it, so editing an
  // existing pick starts from the current name rather than a blank search.
  function openPick(list, i) {
    pick = { list, i };
    pickQ = slotName(list, i);
    pickSugs = [];
    onPickInput();
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

  function choose(name) {
    setSlotName(pick.list, pick.i, name);
    closePick();
  }

  function clearSlot(list, i) {
    setSlotName(list, i, "");
    // Reset the flag too — leaving Consumed off on a now-empty slot would
    // silently apply to whatever is picked next.
    if (list === "items") {
      form[list][i].consumed = true;
      form = form;
    }
    closePick();
  }

  // ── hand-in NPC picker ─────────────────────────────────────────────────────

  function openMobPick() {
    mobPick = true;
    mobForm = null;
    mobErr = "";
    // Seed from the NPC already chosen so re-picking starts from it; blank
    // opens on the flagged turn-in roster, which the server returns for a
    // query this short.
    mobQ = form.mob_name || "";
    mobSugs = [];
    onMobInput();
  }

  function closeMobPick() {
    mobPick = false;
    mobForm = null;
    mobSugs = [];
    mobErr = "";
    mobDup = "";
    clearTimeout(mobTimer);
  }

  function onMobInput() {
    clearTimeout(mobTimer);
    const q = mobQ.trim();
    // Unlike the item picker there is no minimum: a short query is what asks
    // for the known turn-in roster.
    mobTimer = setTimeout(() => {
      SearchQuestMobs(q)
        .then((mobs) => (mobSugs = mobs || []))
        .catch(() => (mobSugs = []));
    }, 250);
  }

  function chooseMob(m) {
    form.mob_id = m.id;
    form.mob_name = m.name;
    // The faction a quest is gated on is the faction of the NPC you hand to, so
    // fill it from the NPC — but only when the field is empty. An officer who
    // typed something else meant it, and a quest can gate on a faction other
    // than its NPC's.
    if (m.faction && !form.faction_group.trim()) form.faction_group = m.faction;
    form = form;
    closeMobPick();
  }

  function clearMob() {
    form.mob_id = 0;
    form.mob_name = "";
    form = form;
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
        }
      : {
          id: 0,
          // Adding from a search that found nothing: carry the text over rather
          // than making it be typed twice.
          name: mobQ.trim(),
          nicknames: "",
          zone_id: "",
          faction: "",
          // A mob added from here is a turn-in NPC by definition.
          quest_mob: true,
        };
    if (zones === null) {
      try {
        zones = (await ListQuestZones()) || [];
      } catch {
        // The zone field is optional and validated server-side; without the
        // list it's still typeable as a raw zone id.
        zones = [];
      }
    }
  }

  // Set when the server reports a same-name mob already exists. Not an error:
  // EQ reuses NPC names for different NPCs and the epics depend on telling them
  // apart, so this asks rather than refuses.
  let mobDup = "";

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
      mobDup = "";
      // Attach it straight away — you opened this to pick an NPC, and having
      // saved one you would only pick it next.
      chooseMob(res.mob);
      // A new NPC faction can be a group nobody has used yet, so refresh the
      // autocomplete rather than leaving it a save behind.
      ListQuestFactions()
        .then((g) => (factionGroups = g || []))
        .catch(() => {});
    } catch (e) {
      mobErr = String(e);
    }
    mobBusy = false;
  }

  // Rejected here so the officer sees which field is wrong; the server checks
  // all of it again. An item on both sides of ONE quest is a loop with no exit
  // — chains across separate quests are the normal case and fine.
  $: rewards = form ? form.rewards.filter((n) => n.trim()) : [];
  $: items = form
    ? form.items
        .filter((t) => t.name.trim())
        .map((t) => ({ name: t.name, consumed: t.consumed !== false }))
    : [];
  // Rewards only. A repeated turn-in is how you ask for two of something —
  // hand-ins take unstacked items, so two Bottles of Milk are two slots — but a
  // repeated reward means nothing.
  $: dup = (() => {
    const seen = new Set();
    for (const n of rewards) {
      const k = n.toLowerCase();
      if (seen.has(k)) return n;
      seen.add(k);
    }
    return "";
  })();
  // An item that comes straight back is a turn-in with Consumed off, never a
  // reward, so an overlap is still a loop with no exit.
  $: overlap = rewards.find((r) =>
    items.some((t) => t.name.toLowerCase() === r.toLowerCase()),
  );
  // A level with nobody to hold it, or a group with no level, is half a
  // requirement — the server rejects either, so catch it here where the field
  // is in front of you.
  $: factionGroupName = form ? form.faction_group.trim() : "";
  // One side may be empty, never both — a step that takes an item and returns
  // nothing is real (it spawns something), and so is one that hands you an item
  // for saying the right words.
  $: formErr = !form
    ? ""
    : !rewards.length && !items.length
      ? "A step needs something on one side — an item handed in, or one received."
      : dup
        ? `"${dup}" is listed as a reward twice.`
        : overlap
          ? `"${overlap}" can't be both a reward and a turn-in. If the step hands it straight back, untick Consumed instead.`
          : form.faction && !factionGroupName
            ? "Pick who the faction requirement is with, or set Faction to None."
            : factionGroupName && !form.faction
              ? `Pick the faction level required with ${factionGroupName}.`
              : "";

  // Not an error — a step with nothing coming back is normal, but it is worth
  // saying out loud that you meant it.
  $: formNote = !form
    ? ""
    : !rewards.length
      ? "Nothing is received — this step consumes its items (a spawn trigger)."
      : !items.length
        ? "Nothing is handed in — this step costs nothing, so it ends a chain."
        : "";

  $: stepKind =
    STEP_KINDS.find((k) => k.id === (form && form.step_kind)) || STEP_KINDS[0];

  async function save() {
    if (formErr || busy) return;
    busy = true;
    err = "";
    try {
      await SaveQuest({
        id: form.id,
        name: form.name.trim(),
        faction: form.faction,
        faction_group: factionGroupName,
        mob_id: form.mob_id,
        class: form.class,
        step_kind: form.step_kind,
        plat_cost: Math.max(0, Number(form.plat_cost) || 0),
        rewards,
        items,
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
</script>

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="q-ov" on:click|self={() => onClose && onClose()}>
  <div class="q-dlg">
    <div class="q-title">
      {form ? (form.id ? "Edit Quest" : "New Quest") : "Quests"}
    </div>

    {#if !form}
      <div class="q-note">
        A quest links the items you hand in to what you get back. Rewards are
        rarely auctioned, so their Magelo tooltip prices them by what the
        turn-ins go for. For a chain — hand in A, get X; hand in X, get Y —
        enter each step as its own quest and the pricing follows the links.
      </div>
      {#if err}<div class="q-err">{err}</div>{/if}

      {#if quests === null}
        <div class="q-note">Loading…</div>
      {:else if !quests.length}
        <div class="q-note">No quests yet.</div>
      {:else}
        <input
          class="q-in"
          placeholder="Filter — reward, turn-in, name, class, NPC or faction…"
          aria-label="Filter quests"
          bind:value={filter}
        />
        {#if !shown.length}
          <div class="q-note">Nothing matches “{filter}”.</div>
        {/if}
        <div class="q-list">
          {#each shown as q (q.id)}
            {@const sub = [
              q.class,
              q.name,
              q.mob_name && "to " + q.mob_name,
              [q.faction, q.faction_group].filter(Boolean).join(" with "),
              q.plat_cost ? q.plat_cost + "pp" : "",
            ].filter(Boolean)}
            <div class="q-row">
              <div class="q-row-main">
                <div class="q-reward">
                  {q.rewards.length ? q.rewards.join(" · ") : "nothing received"}
                </div>
                <div class="q-items">
                  ← {q.items.length
                    ? q.items
                        .map(
                          (t) =>
                            t.name + (t.consumed === false ? " (returned)" : ""),
                        )
                        .join(" · ")
                    : "nothing handed in"}
                </div>
                {#if sub.length}
                  <div class="q-qname">{sub.join(" · ")}</div>
                {/if}
              </div>
              <button class="q-btn" on:click={() => editQuest(q)}>Edit</button>
              <button class="q-btn q-del" on:click={() => (confirmDel = q)}
                >Delete</button
              >
            </div>
          {/each}
        </div>
      {/if}

      <div class="q-btns">
        <button class="q-btn q-go" on:click={newQuest}>+ New Quest</button>
        <button class="q-btn" on:click={() => onClose && onClose()}
          >Close</button
        >
      </div>
    {:else}
      <label class="q-label" for="q-name">Quest name (optional)</label>
      <input
        id="q-name"
        class="q-in"
        list="q-quest-names"
        placeholder="e.g. Wraithbone Bracer turn-in"
        bind:value={form.name}
      />
      <!-- Names already in use. Picking one only fills the box; loading that
           quest is the button below, so an autocomplete click can't throw away
           a step you were part-way through. -->
      <datalist id="q-quest-names">
        {#each questNames as n (n)}
          <option value={n}></option>
        {/each}
      </datalist>
      {#if nameMatch}
        <div class="q-note q-match">
          <span
            >A quest is already named “{nameMatch.name}” ({nameMatch.rewards
              .length
              ? nameMatch.rewards.join(", ")
              : "no reward set"}).</span
          >
          <button class="q-btn q-sm" on:click={() => editQuest(nameMatch)}
            >Load it</button
          >
        </div>
      {/if}

      <div class="q-grid3">
        <div>
          <div class="q-label">Class</div>
          <select
            class="q-in q-sel q-w"
            aria-label="Class"
            bind:value={form.class}
          >
            <option value="">Any</option>
            {#each CLASSES as c (c)}
              <option value={c}>{c}</option>
            {/each}
          </select>
        </div>
        <div>
          <div class="q-label">Step</div>
          <select
            class="q-in q-sel q-w"
            aria-label="Step kind"
            bind:value={form.step_kind}
          >
            {#each STEP_KINDS as k (k.id)}
              <option value={k.id}>{k.label}</option>
            {/each}
          </select>
        </div>
        <div>
          <div class="q-label">Platinum</div>
          <!-- Coin demanded alongside the items — Eldreth wants 100pp with the
               Rogue parchment. Shown, never added to the DKP total. -->
          <input
            class="q-in q-w"
            type="number"
            min="0"
            aria-label="Platinum cost"
            bind:value={form.plat_cost}
          />
        </div>
      </div>

      <div class="q-label">Faction</div>
      <div class="q-faction">
        <select
          class="q-in q-sel"
          aria-label="Faction level"
          bind:value={form.faction}
        >
          <option value="">None</option>
          {#each FACTIONS as f (f)}
            <option value={f}>{f}</option>
          {/each}
        </select>
        <input
          class="q-in"
          list="q-faction-groups"
          placeholder="with… (e.g. Coldain)"
          aria-label="Faction group"
          bind:value={form.faction_group}
        />
        <!-- Ordered by the server: factions this guild's quests already use
             first. Free text, so one missing from the roster is still typeable
             and joins the list once saved. -->
        <datalist id="q-faction-groups">
          {#each factionGroups as g (g.name)}
            <option value={g.name}></option>
          {/each}
        </datalist>
      </div>

      <div class="q-label">{stepKind.npc}</div>
      <div class="q-slot-wrap">
        <button
          class="q-slot"
          class:q-empty={!form.mob_name}
          on:click={openMobPick}
        >
          {form.mob_name || "Pick an NPC…"}
        </button>
        {#if form.mob_name}
          <button
            class="q-x"
            title="Clear"
            aria-label="Clear hand-in NPC"
            on:click={clearMob}>×</button
          >
        {/if}
      </div>

      <div class="q-label">Rewards (up to {MAX_REWARDS})</div>
      <div class="q-slots">
        {#each form.rewards as rw, i}
          <div class="q-slot-wrap">
            <button
              class="q-slot q-slot-reward"
              class:q-empty={!rw}
              on:click={() => openPick("rewards", i)}
            >
              {rw || `Reward ${i + 1}…`}
            </button>
            {#if rw}
              <button
                class="q-x"
                title="Clear"
                aria-label="Clear reward {i + 1}"
                on:click={() => clearSlot("rewards", i)}>×</button
              >
            {/if}
          </div>
        {/each}
      </div>

      <div class="q-label">Hand in (up to {MAX_ITEMS} slots)</div>
      <!-- One row per trade slot. Turn-ins take unstacked items, so a step
           wanting two of something fills two slots with it. -->
      <div class="q-turnins">
        {#each form.items as it, i}
          <div class="q-turnin">
            <button
              class="q-slot"
              class:q-empty={!it.name}
              on:click={() => openPick("items", i)}
            >
              {it.name || `Slot ${i + 1}…`}
            </button>
            <label
              class="q-chk q-consumed"
              class:q-off={!it.name}
              title="Untick when the step hands this item straight back — the Enchanter's Jeb's Seal goes to all four masters and returns each time. A returned item costs nothing and is left out of the total."
            >
              <input
                type="checkbox"
                disabled={!it.name}
                bind:checked={it.consumed}
              />
              Consumed
            </label>
            {#if it.name}
              <button
                class="q-x q-x-inline"
                title="Clear"
                aria-label="Clear slot {i + 1}"
                on:click={() => clearSlot("items", i)}>×</button
              >
            {/if}
          </div>
        {/each}
      </div>

      {#if formErr}<div class="q-note q-warn">{formErr}</div>{/if}
      {#if !formErr && formNote}<div class="q-note">{formNote}</div>{/if}
      {#if err}<div class="q-err">{err}</div>{/if}

      <div class="q-btns">
        <button class="q-btn" on:click={() => (form = null)}>Cancel</button>
        <button class="q-btn q-go" disabled={!!formErr || busy} on:click={save}
          >{busy ? "Saving…" : "Save"}</button
        >
      </div>
    {/if}
  </div>
</div>

<!-- item picker: sits above the form, which stays visible behind it -->
{#if pick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="q-ov q-ov-top" on:click|self={closePick}>
    <div class="q-dlg q-dlg-sm">
      <div class="q-title">
        {pick.list === "rewards" ? "Reward" : "Turn-in"}
        {pick.i + 1}
      </div>
      <input
        class="q-in"
        placeholder="Search the item DB…"
        bind:value={pickQ}
        on:input={onPickInput}
        use:focusIt
      />
      <div class="q-sugs">
        {#each pickSugs as s (s)}
          <button class="q-sug" on:click={() => choose(s)}>{s}</button>
        {:else}
          <div class="q-note">
            {pickQ.trim().length < 2
              ? "Type at least two characters."
              : "No items match. Only items already in the DB can be used — add it with “Add Item…” first."}
          </div>
        {/each}
      </div>
      <div class="q-btns">
        <button class="q-btn" on:click={closePick}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- hand-in NPC picker: search the mob DB, or add a row that isn't in it -->
{#if mobPick}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="q-ov q-ov-top" on:click|self={closeMobPick}>
    <div class="q-dlg q-dlg-sm">
      {#if !mobForm}
        <div class="q-title">Hand-in NPC</div>
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
                ? "No NPCs are flagged as turn-in NPCs yet. Type a name to search the mob DB."
                : "No mobs match. Turn-in NPCs often drop nothing and are never parsed, so they may not be in the DB — add it below."}
            </div>
          {/each}
        </div>
        <div class="q-btns">
          <button class="q-btn q-go" on:click={() => openMobForm(null)}
            >+ Add NPC</button
          >
          <button class="q-btn" on:click={closeMobPick}>Cancel</button>
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
        <!-- Values are zone ids because that's what eqmobs stores; the label is
             the zone name, which is what an officer knows it by. -->
        <datalist id="q-zones">
          {#each zones || [] as z (z.id)}
            <option value={z.id}>{z.name}</option>
          {/each}
        </datalist>

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
          Turn-in NPC — list this one first when picking
        </label>

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

{#if confirmDel}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="q-ov q-ov-top" on:click|self={() => (confirmDel = null)}>
    <div class="q-dlg q-dlg-sm">
      <div class="q-title">Delete Quest</div>
      <div class="q-note">
        Delete the quest for <strong
          >{confirmDel.rewards.join(", ") || "(no reward)"}</strong
        >? Its reward and turn-in lists go with it, and those items lose their
        component pricing. Quests that chain off them keep working — they just
        stop resolving a value through this step.
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
  /* Picker and delete confirm render after the main dialog and must paint
     above it. */
  .q-ov-top {
    z-index: 210;
  }
  .q-dlg {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    width: 520px;
    max-width: 94vw;
    max-height: 86vh;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .q-dlg-sm {
    width: 380px;
  }
  .q-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .q-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.05em;
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
  /* The name collides with an existing quest: a prompt to jump to it, not a
     problem, so it keeps the muted note colour. */
  .q-match {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .q-match span {
    flex: 1;
    min-width: 0;
  }
  .q-sm {
    padding: 3px 9px;
    font-size: 11.5px;
    white-space: nowrap;
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
  /* Level and group read as one requirement, so they sit on one row. The group
     takes the remaining width — faction names run long. */
  .q-faction {
    display: grid;
    grid-template-columns: 150px 1fr;
    gap: 6px;
  }
  /* Class, step kind and platinum are all short and unrelated to each other,
     so they share a row rather than costing three. */
  .q-grid3 {
    display: grid;
    grid-template-columns: 1fr 1fr 90px;
    gap: 6px;
    align-items: end;
  }
  .q-w {
    width: 100%;
    box-sizing: border-box;
  }

  /* ── turn-in rows ── */
  /* One row per slot rather than the two-column grid the rewards use: a turn-in
     carries a consumed flag, which doesn't fit beside a name at half width. */
  .q-turnins {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .q-turnin {
    display: grid;
    grid-template-columns: 1fr auto 20px;
    gap: 6px;
    align-items: center;
  }
  .q-consumed {
    white-space: nowrap;
    font-size: 11px;
  }
  .q-consumed.q-off {
    opacity: 0.35;
  }
  /* The clear button is a grid cell here, not an overlay on the name button. */
  .q-x-inline {
    position: static;
    transform: none;
  }

  /* ── quest list ── */
  .q-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    max-height: 46vh;
    overflow-y: auto;
  }
  .q-row {
    display: flex;
    align-items: center;
    gap: 8px;
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 7px 9px;
  }
  .q-row-main {
    flex: 1;
    min-width: 0;
  }
  .q-reward {
    font-size: 12.5px;
    font-weight: 600;
    color: var(--accent);
  }
  .q-items {
    font-size: 11px;
    color: var(--text-secondary);
    word-break: break-word;
  }
  .q-qname {
    font-size: 10.5px;
    color: var(--text-muted);
    font-style: italic;
  }

  /* ── slots ── */
  .q-slots {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
  }
  .q-slot-wrap {
    position: relative;
    display: flex;
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

  /* ── NPC results ── */
  /* Each result is two controls: pick it, or edit its record. */
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
  /* Zone and faction stay muted on hover — the name is what's being picked. */
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
  .q-chk {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }

  /* ── buttons ── */
  .q-btns {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .q-btn {
    background: none;
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 5px;
    padding: 5px 14px;
    font-size: 12.5px;
    cursor: pointer;
  }
  .q-btn:hover {
    background: rgba(255, 255, 255, 0.06);
  }
  .q-go {
    border-color: var(--accent);
    color: var(--accent);
  }
  .q-del {
    border-color: #a04a4a;
    color: #e07b7b;
  }
  .q-btn:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
