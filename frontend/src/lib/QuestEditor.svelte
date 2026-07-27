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

  let quests = null; // null = loading
  let err = "";
  let busy = false;

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

  function padded(list, n) {
    const out = (list || []).slice(0, n);
    while (out.length < n) out.push("");
    return out;
  }

  function newQuest() {
    form = {
      id: 0,
      name: "",
      faction: "",
      faction_group: "",
      rewards: padded([], MAX_REWARDS),
      items: padded([], MAX_ITEMS),
    };
    closePick();
  }

  function editQuest(q) {
    form = {
      id: q.id,
      name: q.name || "",
      faction: q.faction || "",
      faction_group: q.faction_group || "",
      rewards: padded(q.rewards, MAX_REWARDS),
      items: padded(q.items, MAX_ITEMS),
    };
    closePick();
  }

  function closePick() {
    pick = null;
    pickQ = "";
    pickSugs = [];
    clearTimeout(pickTimer);
  }

  // Opening a slot seeds the box with whatever is already in it, so editing an
  // existing pick starts from the current name rather than a blank search.
  function openPick(list, i) {
    pick = { list, i };
    pickQ = form[list][i];
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
    form[pick.list][pick.i] = name;
    form = form;
    closePick();
  }

  function clearSlot(list, i) {
    form[list][i] = "";
    form = form;
    closePick();
  }

  // Rejected here so the officer sees which field is wrong; the server checks
  // all of it again. An item on both sides of ONE quest is a loop with no exit
  // — chains across separate quests are the normal case and fine.
  $: rewards = form ? form.rewards.filter((n) => n.trim()) : [];
  $: items = form ? form.items.filter((n) => n.trim()) : [];
  $: dup = (() => {
    for (const list of [rewards, items]) {
      const seen = new Set();
      for (const n of list) {
        const k = n.toLowerCase();
        if (seen.has(k)) return n;
        seen.add(k);
      }
    }
    return "";
  })();
  $: overlap = rewards.find((r) =>
    items.some((i) => i.toLowerCase() === r.toLowerCase()),
  );
  // A level with nobody to hold it, or a group with no level, is half a
  // requirement — the server rejects either, so catch it here where the field
  // is in front of you.
  $: factionGroupName = form ? form.faction_group.trim() : "";
  $: formErr = !form
    ? ""
    : !rewards.length
      ? "Pick at least one reward item."
      : !items.length
        ? "Pick at least one item to hand in."
        : dup
          ? `"${dup}" is listed twice.`
          : overlap
            ? `"${overlap}" can't be both a reward and a turn-in of the same quest.`
            : form.faction && !factionGroupName
              ? "Pick who the faction requirement is with, or set Faction to None."
              : factionGroupName && !form.faction
                ? `Pick the faction level required with ${factionGroupName}.`
                : "";

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
        <div class="q-list">
          {#each quests as q (q.id)}
            {@const req = [q.faction, q.faction_group]
              .filter(Boolean)
              .join(" with ")}
            <div class="q-row">
              <div class="q-row-main">
                <div class="q-reward">
                  {q.rewards.length ? q.rewards.join(" · ") : "no reward set"}
                </div>
                <div class="q-items">
                  ← {q.items.length
                    ? q.items.join(" · ")
                    : "no turn-in items"}
                </div>
                {#if q.name || req}
                  <div class="q-qname">
                    {q.name}{q.name && req ? " · " : ""}{req}
                  </div>
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
        placeholder="e.g. Wraithbone Bracer turn-in"
        bind:value={form.name}
      />

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

      <div class="q-label">Hand in (up to {MAX_ITEMS})</div>
      <div class="q-slots">
        {#each form.items as it, i}
          <div class="q-slot-wrap">
            <button
              class="q-slot"
              class:q-empty={!it}
              on:click={() => openPick("items", i)}
            >
              {it || `Item ${i + 1}…`}
            </button>
            {#if it}
              <button
                class="q-x"
                title="Clear"
                aria-label="Clear item {i + 1}"
                on:click={() => clearSlot("items", i)}>×</button
              >
            {/if}
          </div>
        {/each}
      </div>

      {#if formErr}<div class="q-note q-warn">{formErr}</div>{/if}
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
