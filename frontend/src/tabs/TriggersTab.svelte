<script>
  import { onMount, onDestroy, tick } from "svelte";
  import { fade, slide } from "svelte/transition";
  import { Events } from "@wailsio/runtime";
  import {
    GetTriggerState,
    GetTriggerTree,
    GetTriggersMeta,
    SyncTriggers,
    SaveTrigger,
    CreateTrigger,
    DeleteTrigger,
    CreateTriggerGroup,
    RenameTriggerGroup,
    DeleteTriggerGroup,
    SetTriggerGroupEnabled,
    SetTriggerEnabled,
    OpenPopout,
    DismissTimer,
    DismissTimerCategory,
    SetPopoutsHidden,
    SetAllPopoutsLocked,
    ArePopoutsManuallyHidden,
    ArePopoutsLocked,
  } from "../../bindings/FuseBridge/app.js";
  import TriggerNode from "../lib/TriggerNode.svelte";
  import { scale } from "../lib/scale.js";
  import { catColor } from "../lib/catColor.js";

  let view = "live"; // "live" | "edit"
  let state = {
    imported: true,
    character: "",
    alert: null,
    timers: [],
    activity: [],
  };
  let now = Date.now();
  let pollTimer, animReq, offTriggers;

  const ALERT_SHOW_MS = 10000;
  $: alertShown =
    state.alert && state.alert.text && now - state.alert.at_ms < ALERT_SHOW_MS;

  // Group running timers by category; a category renders only while it has
  // active timers. Soonest-ending first inside each category.
  $: activeTimers = (state.timers || []).filter((t) => t.ends_at_ms > now);
  $: cats = (() => {
    const m = new Map();
    for (const t of activeTimers) {
      const c = t.category || "Default";
      if (!m.has(c)) m.set(c, []);
      m.get(c).push(t);
    }
    const out = [...m.entries()].map(([name, timers]) => ({
      name,
      color: catColor(name),
      timers: timers.sort((a, b) => a.ends_at_ms - b.ends_at_ms),
    }));
    out.sort((a, b) => a.name.localeCompare(b.name));
    return out;
  })();

  function fmtRemain(ms) {
    const s = Math.max(0, Math.ceil(ms / 1000));
    const m = Math.floor(s / 60);
    return `${String(m).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
  }
  function fmtClock(ms) {
    return new Date(ms).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    });
  }
  function barFrac(t) {
    const total = t.ends_at_ms - t.started_at_ms;
    if (total <= 0) return 0;
    return Math.max(0, Math.min(1, (t.ends_at_ms - now) / total));
  }

  // Overlap guard: the engine pushes "triggers-changed" the instant a trigger
  // fires, so polls can arrive faster than they complete. If one is in flight,
  // remember to re-poll after it so we never settle on stale state.
  let polling = false,
    pollAgain = false;

  async function poll() {
    if (polling) {
      pollAgain = true;
      return;
    }
    polling = true;
    try {
      const prevChar = state.character;
      state = await GetTriggerState();
      // Enabled/disabled is per character, so the sliders in the edit tree
      // belong to whoever is logged in — refetch when the toon changes.
      if (treeLoaded && state.character !== prevChar) await loadTree();
    } catch {
      /* keep last state */
    }
    polling = false;
    if (pollAgain) {
      pollAgain = false;
      poll();
    }
  }

  // Dismiss running timers from the live board. Optimistically drop them from
  // local state so the bar disappears immediately (the poll is ~1s behind).
  async function dismissTimer(t) {
    state.timers = (state.timers || []).filter((x) => x.id !== t.id);
    try {
      await DismissTimer(t.id);
    } catch {
      /* poll will re-sync */
    }
  }
  async function dismissCat(c) {
    state.timers = (state.timers || []).filter(
      (x) => (x.category || "Default") !== c.name,
    );
    try {
      await DismissTimerCategory(c.name);
    } catch {
      /* poll will re-sync */
    }
  }

  // Global overlay controls: hide/restore all overlays, and lock (make them
  // click-through + non-movable) / unlock all. Unlock here is the escape hatch
  // for a locked, click-through overlay that can't unlock itself.
  let winHidden = false;
  let winLocked = false;
  async function toggleHideWindows() {
    winHidden = !winHidden;
    try {
      await SetPopoutsHidden(winHidden);
    } catch {
      /* no overlays open */
    }
  }
  async function toggleLockWindows() {
    winLocked = !winLocked;
    try {
      await SetAllPopoutsLocked(winLocked);
    } catch {
      /* no overlays open */
    }
  }

  // ── server sync + officer status ────────────────────────────────────────────
  // The Fuse Triggers set is downloaded from the server on open; officers'
  // edits sync back automatically. importMsg/importErr are transient notices.
  let importMsg = "";
  let importErr = "";
  let meta = { linked: false, officer: false };

  async function loadMeta() {
    try {
      meta = await GetTriggersMeta();
    } catch {
      /* keep last */
    }
  }
  async function syncNow() {
    try {
      await SyncTriggers(); // background fetch (and officer seed) on the Go side
    } catch {
      /* offline — the cached set stays usable */
    }
    await loadMeta();
    // Give the background sync a moment, then refresh what's on screen.
    setTimeout(async () => {
      await poll();
      if (view === "edit") await loadTree();
      await loadMeta();
    }, 2500);
  }

  // ── edit view: tree, context menu, form ─────────────────────────────────────
  let tree = [];
  let expanded = {}; // groupId -> true
  let highlightId = 0;
  let treeLoaded = false;

  async function loadTree() {
    try {
      tree = (await GetTriggerTree()) || [];
      treeLoaded = true;
    } catch {
      tree = [];
    }
  }

  async function setView(v) {
    view = v;
    if (v === "edit" && !treeLoaded) await loadTree();
  }

  // ── search: filters the tree to matching triggers/groups, ↑/↓ steps through
  // matches (like the Characters search). Matching is on trigger names AND
  // regex lines; a matching group name keeps its whole subtree visible.
  let searchQ = "";
  let matchIdx = 0;
  let searchExpanded = {}; // expansion state while a search is active
  let lastQ = "";

  function trigMatches(t, q) {
    return (
      t.name.toLowerCase().includes(q) ||
      (t.trigger_text || "").toLowerCase().includes(q)
    );
  }
  function filterTree(nodes, q) {
    const out = [];
    for (const g of nodes) {
      if (g.name.toLowerCase().includes(q)) {
        out.push(g); // group name matched — keep its whole subtree
        continue;
      }
      const kids = filterTree(g.groups || [], q);
      const trigs = (g.triggers || []).filter((t) => trigMatches(t, q));
      if (kids.length || trigs.length)
        out.push({ ...g, groups: kids, triggers: trigs });
    }
    return out;
  }
  function collectGroupIds(nodes, acc = {}) {
    for (const g of nodes) {
      acc[g.id] = true;
      collectGroupIds(g.groups || [], acc);
    }
    return acc;
  }
  // Match list mirrors the tree's visual order (subgroups render first).
  function collectMatchIds(nodes, q, acc = []) {
    for (const g of nodes) {
      collectMatchIds(g.groups || [], q, acc);
      for (const t of g.triggers || []) if (trigMatches(t, q)) acc.push(t.id);
    }
    return acc;
  }

  $: q = searchQ.trim().toLowerCase();
  $: shownTree = q ? filterTree(tree, q) : tree;
  $: matchIds = q ? collectMatchIds(shownTree, q) : [];
  $: if (q !== lastQ) {
    lastQ = q;
    matchIdx = 0;
    if (q) searchExpanded = collectGroupIds(shownTree);
  }
  $: if (q && matchIdx >= matchIds.length) matchIdx = 0;
  $: if (q) highlightId = matchIds.length ? matchIds[matchIdx] : 0;

  async function stepMatch(d) {
    if (!matchIds.length) return;
    matchIdx = (matchIdx + d + matchIds.length) % matchIds.length;
    await tick();
    const el = document.getElementById(`trig-${matchIds[matchIdx]}`);
    if (el) el.scrollIntoView({ block: "center" });
  }

  function onToggle(id) {
    if (q) {
      searchExpanded[id] = !searchExpanded[id];
      searchExpanded = searchExpanded;
    } else {
      expanded[id] = !expanded[id];
      expanded = expanded;
    }
  }

  // Enable/disable slider on a group or trigger row.
  async function onToggleEnable(kind, obj, val) {
    try {
      if (kind === "group") await SetTriggerGroupEnabled(obj.id, val);
      else await SetTriggerEnabled(obj.id, val);
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 6000);
    }
  }

  // Context menu (right-click on a group or trigger row). Only editable nodes
  // get a menu: Fuse Triggers content is officer-only (non-officers can still
  // enable/disable via the slider), while Personal is editable by everyone.
  let menu = null; // {x, y, kind: "group"|"trigger", target}
  function onMenu(e, kind, target) {
    if (!target.editable) return;
    // The shell is CSS-zoomed; convert viewport coords to layout coords.
    menu = { x: e.clientX / $scale, y: e.clientY / $scale, kind, target };
  }
  function closeMenu() {
    menu = null;
  }

  // Single-input prompt modal (group rename / new group).
  let prompt = null; // {title, value, onOK(value)}
  function openPrompt(title, value, onOK) {
    prompt = { title, value, onOK };
  }
  async function promptOK() {
    const p = prompt;
    prompt = null;
    if (p && p.value.trim()) await p.onOK(p.value.trim());
  }

  // Trigger edit/create form modal.
  let form = null; // TriggerEditUI-shaped
  let formIsNew = false;
  let formGroupId = 0;
  let formErr = "";

  function blankForm() {
    return {
      id: 0,
      group_id: 0,
      name: "",
      trigger_text: "",
      enable_regex: true,
      use_text: false,
      display_text: "",
      timer_enabled: false,
      timer_name: "",
      timer_seconds: 30,
      restart_based_on_timer_name: false,
      timer_start_behavior: "StartNewTimer",
      use_timer_ended: false,
      timer_ended_text: "",
      category: "Default",
      unsupported: false,
    };
  }

  function menuEditTrigger() {
    form = { ...menu.target };
    formIsNew = false;
    formErr = "";
    closeMenu();
  }
  function menuNewTrigger() {
    form = blankForm();
    formIsNew = true;
    formGroupId = menu.target.id;
    formErr = "";
    closeMenu();
  }
  async function menuDeleteTrigger() {
    const t = menu.target;
    closeMenu();
    if (!confirm(`Delete trigger "${t.name}"?`)) return;
    try {
      await DeleteTrigger(t.id);
      await loadTree();
    } catch (e) {
      importErr = String(e);
    }
  }
  function menuRenameGroup() {
    const g = menu.target;
    closeMenu();
    openPrompt("Rename group", g.name, async (name) => {
      await RenameTriggerGroup(g.id, name);
      await loadTree();
    });
  }
  function menuNewGroup() {
    const g = menu.target;
    closeMenu();
    openPrompt("New group name", "", async (name) => {
      await CreateTriggerGroup(g.id, name);
      expanded[g.id] = true;
      expanded = expanded;
      await loadTree();
    });
  }
  async function menuDeleteGroup() {
    const g = menu.target;
    closeMenu();
    const n = g.total_triggers || 0;
    const detail = n
      ? ` and the ${n} trigger${n === 1 ? "" : "s"} inside it`
      : "";
    if (!confirm(`Delete group "${g.name}"${detail}?`)) return;
    try {
      await DeleteTriggerGroup(g.id);
      await loadTree();
    } catch (e) {
      importErr = String(e);
      setTimeout(() => (importErr = ""), 6000);
    }
  }

  async function saveForm() {
    formErr = "";
    if (!form.name.trim()) {
      formErr = "Name is required.";
      return;
    }
    if (!form.trigger_text.trim()) {
      formErr = "Search text / regex is required.";
      return;
    }
    form.timer_seconds = Math.max(
      0,
      Math.round(Number(form.timer_seconds) || 0),
    );
    try {
      if (formIsNew) {
        const id = await CreateTrigger(formGroupId, form);
        highlightId = id;
      } else {
        await SaveTrigger(form);
        highlightId = form.id;
      }
      form = null;
      await loadTree();
    } catch (e) {
      formErr = String(e);
    }
  }

  // Activity path click → edit view, expand ancestors, highlight the trigger.
  function findTriggerPath(nodes, id, trail = []) {
    for (const g of nodes) {
      const here = [...trail, g.id];
      for (const t of g.triggers || []) {
        if (t.id === id) return here;
      }
      const deeper = findTriggerPath(g.groups || [], id, here);
      if (deeper) return deeper;
    }
    return null;
  }
  async function openInTree(triggerId) {
    await setView("edit");
    if (!treeLoaded) await loadTree();
    searchQ = ""; // a live search filter would hide the target
    const trail = findTriggerPath(tree, triggerId);
    if (trail) {
      for (const gid of trail) expanded[gid] = true;
      expanded = expanded;
    }
    highlightId = triggerId;
    await tick();
    const el = document.getElementById(`trig-${triggerId}`);
    if (el) el.scrollIntoView({ block: "center" });
  }

  function animLoop() {
    now = Date.now();
    animReq = requestAnimationFrame(animLoop);
  }

  onMount(async () => {
    await poll();
    await syncNow(); // pull the latest Fuse Triggers from the server on open
    // Reflect the real overlay state so the buttons are correct after a remount.
    try {
      winHidden = await ArePopoutsManuallyHidden();
      winLocked = await ArePopoutsLocked();
    } catch {
      /* defaults */
    }
    // Push: refresh the instant a trigger fires. The interval stays as a safety
    // net (missed event, or a change with no event).
    offTriggers = Events.On("triggers-changed", poll);
    pollTimer = setInterval(poll, 1000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (offTriggers) offTriggers();
    if (animReq) cancelAnimationFrame(animReq);
  });
</script>

<svelte:window on:click={closeMenu} />

<div class="trig-tab">
  <div class="bar">
    <span class="title">Timers</span>
    {#if state.character}
      <span class="who">for {state.character}</span>
    {/if}
    {#if importMsg}<span class="imp-ok" transition:fade>{importMsg}</span>{/if}
    {#if importErr}<span class="imp-err" transition:fade>{importErr}</span>{/if}
    <div class="bar-right">
      {#if view === "edit" && meta.linked && !meta.officer}
        <span class="ro-note" title="Only officers can edit Fuse Triggers"
          >Fuse Triggers are read-only</span
        >
      {/if}
      <button
        class="btn"
        class:on={winLocked}
        title="Lock timer overlays: click-through and non-movable (the map stays interactive). Unlock here to regain control."
        on:click={toggleLockWindows}>{winLocked ? "Unlock" : "Lock"}</button
      >
      <button
        class="btn"
        class:on={winHidden}
        title="Hide or restore all overlay windows"
        on:click={toggleHideWindows}>{winHidden ? "Show" : "Hide"}</button
      >
      <button
        class="btn icon-btn"
        title={view === "live" ? "Manage timers" : "Back to timers"}
        on:click={() => setView(view === "live" ? "edit" : "live")}
      >
        {#if view === "live"}
          <!-- pencil -->
          <svg
            viewBox="0 0 24 24"
            width="13"
            height="13"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
          </svg>
        {:else}
          <!-- list -->
          <svg
            viewBox="0 0 24 24"
            width="13"
            height="13"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M8 6h13M8 12h13M8 18h13" />
            <circle cx="3.5" cy="6" r="1" />
            <circle cx="3.5" cy="12" r="1" />
            <circle cx="3.5" cy="18" r="1" />
          </svg>
        {/if}
      </button>
    </div>
  </div>

  {#if alertShown}
    <div class="alert" transition:fade={{ duration: 250 }}>
      {state.alert.text}
    </div>
  {/if}

  <div class="main">
    {#if view === "live"}
      {#if cats.length === 0}
        <div class="hint">
          No active timers — countdown bars appear here when a trigger fires.
        </div>
      {/if}
      {#each cats as c (c.name)}
        <div class="cat" transition:slide|local={{ duration: 150 }}>
          <div class="cat-head">
            <span class="cat-dot" style="background:{c.color}"></span>
            <span class="cat-name">{c.name}</span>
            <div class="cat-actions">
              <button
                class="cat-btn"
                title="Dismiss all “{c.name}” timers"
                aria-label="Dismiss all {c.name} timers"
                on:click={() => dismissCat(c)}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="12"
                  height="12"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" />
                </svg>
              </button>
              <button
                class="cat-btn"
                title="Pop out “{c.name}” as an overlay"
                aria-label="Pop out {c.name}"
                on:click={() => OpenPopout("timers", c.name)}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="12"
                  height="12"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path d="M10 6H6a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4" />
                  <path d="M14 4h6v6" />
                  <path d="M20 4 12 12" />
                </svg>
              </button>
            </div>
          </div>
          {#each c.timers as t (t.id)}
            <div class="tbar">
              <div
                class="tbar-fill"
                style="width:{barFrac(t) * 100}%; background:{c.color}"
              ></div>
              <span class="tbar-name">{t.name}</span>
              <span class="tbar-time">{fmtRemain(t.ends_at_ms - now)}</span>
              <button
                class="tbar-trash"
                title="Dismiss this timer"
                aria-label="Dismiss timer"
                on:click={() => dismissTimer(t)}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="12"
                  height="12"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" />
                </svg>
              </button>
            </div>
          {/each}
        </div>
      {/each}
    {:else}
      <div class="search-row">
        <input
          class="in search-in"
          placeholder="Search names & regex…"
          bind:value={searchQ}
          on:keydown={(e) => e.key === "Enter" && stepMatch(1)}
        />
        <span class="match-count">
          {matchIds.length
            ? `${matchIdx + 1}/${matchIds.length}`
            : q
              ? "0 results"
              : ""}
        </span>
        <button
          class="btn"
          title="Previous match"
          disabled={!matchIds.length}
          on:click={() => stepMatch(-1)}>↑</button
        >
        <button
          class="btn"
          title="Next match"
          disabled={!matchIds.length}
          on:click={() => stepMatch(1)}>↓</button
        >
      </div>
      <div class="tree">
        {#each shownTree as g (g.id)}
          <TriggerNode
            node={g}
            expanded={q ? searchExpanded : expanded}
            {highlightId}
            query={q}
            {onToggle}
            {onMenu}
            {onToggleEnable}
          />
        {:else}
          <div class="hint">
            {q ? "No triggers match your search." : "No trigger groups."}
          </div>
        {/each}
      </div>
    {/if}
  </div>

  <!-- locked activity feed -->
  <div class="activity">
    <div class="act-title">Activity</div>
    <div class="act-list">
      {#each state.activity || [] as a}
        <div class="act-row">
          <span class="act-time">{fmtClock(a.at_ms)}</span>
          <button class="act-path" on:click={() => openInTree(a.trigger_id)}>
            {a.path.join(" > ")}
          </button>
        </div>
      {:else}
        <div class="act-empty">No triggers have fired yet.</div>
      {/each}
    </div>
  </div>
</div>

<!-- context menu -->
{#if menu}
  <div class="ctx" style="left:{menu.x}px; top:{menu.y}px">
    {#if menu.kind === "trigger"}
      <button class="ctx-item" on:click={menuEditTrigger}>Edit</button>
      <button class="ctx-item danger" on:click={menuDeleteTrigger}
        >Delete</button
      >
    {:else}
      <button class="ctx-item" on:click={menuRenameGroup}>Edit Name</button>
      <button class="ctx-item" on:click={menuNewTrigger}>New Trigger</button>
      <button class="ctx-item" on:click={menuNewGroup}>New Group</button>
      <button class="ctx-item danger" on:click={menuDeleteGroup}>Delete</button>
    {/if}
  </div>
{/if}

<!-- single-input prompt -->
{#if prompt}
  <div class="overlay" on:click|self={() => (prompt = null)}>
    <div class="modal">
      <div class="modal-title">{prompt.title}</div>
      <!-- svelte-ignore a11y-autofocus -->
      <input
        class="in"
        autofocus
        bind:value={prompt.value}
        on:keydown={(e) => e.key === "Enter" && promptOK()}
      />
      <div class="modal-actions">
        <button class="btn" on:click={promptOK}>OK</button>
        <button class="btn" on:click={() => (prompt = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- trigger edit/create form -->
{#if form}
  <div class="overlay" on:click|self={() => (form = null)}>
    <div class="modal form">
      <div class="modal-title">
        {formIsNew ? "New Trigger" : "Edit Trigger"}
      </div>

      <label class="f-label" for="tf-name">Name</label>
      <input id="tf-name" class="in" bind:value={form.name} />

      <label class="f-label" for="tf-text">Search text</label>
      <textarea
        id="tf-text"
        class="in mono"
        rows="2"
        bind:value={form.trigger_text}
      ></textarea>
      <label class="f-chk">
        <input type="checkbox" bind:checked={form.enable_regex} /> Regular expression
      </label>

      <div class="f-sep" />
      <label class="f-chk">
        <input type="checkbox" bind:checked={form.use_text} /> Show alert
      </label>
      {#if form.use_text}
        <input
          class="in"
          placeholder="Alert text (supports $&#123;1&#125; captures)"
          bind:value={form.display_text}
        />
      {/if}

      <div class="f-sep" />
      <label class="f-chk">
        <input type="checkbox" bind:checked={form.timer_enabled} /> Start countdown
        timer
      </label>
      {#if form.timer_enabled}
        <div class="f-grid">
          <label class="f-label" for="tf-tname">Timer name</label>
          <input
            id="tf-tname"
            class="in"
            placeholder="Defaults to trigger name"
            bind:value={form.timer_name}
          />
          <label class="f-label" for="tf-dur">Duration (seconds)</label>
          <input
            id="tf-dur"
            class="in num"
            type="number"
            min="1"
            bind:value={form.timer_seconds}
          />
          <label class="f-label" for="tf-behavior">If already running</label>
          <select
            id="tf-behavior"
            class="in"
            bind:value={form.timer_start_behavior}
          >
            <option value="StartNewTimer">Start another timer</option>
            <option value="RestartTimer">Restart this trigger's timer</option>
            <option value="IgnoreIfRunning">Ignore</option>
          </select>
        </div>
        <label
          class="f-chk"
          title="Reset any running timer with the exact same name instead of starting a new one"
        >
          <input
            type="checkbox"
            bind:checked={form.restart_based_on_timer_name}
          />
          Restart timer with matching name
        </label>
        <label class="f-chk">
          <input type="checkbox" bind:checked={form.use_timer_ended} /> Alert when
          timer finishes
        </label>
        {#if form.use_timer_ended}
          <input
            class="in"
            placeholder="Finished alert text"
            bind:value={form.timer_ended_text}
          />
        {/if}
      {/if}

      <div class="f-sep" />
      <label class="f-label" for="tf-cat">Category</label>
      <input id="tf-cat" class="in" bind:value={form.category} />

      {#if formErr}<div class="f-err">{formErr}</div>{/if}
      <div class="modal-actions">
        <button class="btn save" on:click={saveForm}>Save</button>
        <button class="btn" on:click={() => (form = null)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .trig-tab {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
    flex-shrink: 0;
  }
  .title {
    color: var(--accent);
    font-weight: 600;
    font-size: 13px;
  }
  .who {
    color: var(--text-muted);
    font-size: 11px;
  }
  .imp-ok {
    color: var(--success);
    font-size: 11px;
  }
  .imp-err {
    color: #e05c5c;
    font-size: 11px;
  }
  .ro-note {
    color: var(--text-muted);
    font-size: 10.5px;
    font-style: italic;
  }
  .bar-right {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 8px;
  }
  .btn:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  .btn.on {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 3px 9px;
    line-height: 1.2;
  }

  .alert {
    flex-shrink: 0;
    text-align: center;
    padding: 10px 14px;
    font-size: 17px;
    font-weight: 700;
    color: var(--accent);
    background: rgba(200, 169, 81, 0.1);
    border-bottom: 1px solid var(--accent-dim);
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.6);
  }

  .main {
    flex: 1;
    overflow-y: auto;
    padding: 10px 14px;
  }
  .hint {
    color: var(--text-muted);
    font-size: 12px;
    padding: 16px 4px;
    text-align: center;
  }

  /* countdown bars */
  .cat {
    margin-bottom: 12px;
  }
  .cat-head {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-secondary);
    margin-bottom: 5px;
  }
  .cat-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
  .cat-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .cat-actions {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 2px;
    opacity: 0;
    transition: opacity 0.12s;
  }
  .cat-head:hover .cat-actions {
    opacity: 1;
  }
  .cat-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 1px 3px;
    border-radius: 3px;
    display: inline-flex;
    align-items: center;
  }
  .cat-btn:hover {
    color: var(--accent);
    background: rgba(255, 255, 255, 0.06);
  }
  .cat-btn:first-child:hover {
    color: #e05c5c;
  }

  .tbar-trash {
    position: absolute;
    right: 5px;
    top: 50%;
    transform: translateY(-50%);
    background: none;
    border: none;
    color: #fff;
    cursor: pointer;
    padding: 1px;
    display: inline-flex;
    align-items: center;
    opacity: 0;
    transition: opacity 0.12s;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.85);
    filter: drop-shadow(0 1px 1px rgba(0, 0, 0, 0.85));
  }
  .tbar:hover .tbar-trash {
    opacity: 1;
  }
  .tbar:hover .tbar-time {
    right: 28px;
  }
  .tbar-trash:hover {
    color: #ff8a8a;
  }
  .tbar {
    position: relative;
    height: 22px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    overflow: hidden;
    margin-bottom: 4px;
  }
  .tbar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    opacity: 0.5;
  }
  .tbar-name,
  .tbar-time {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    font-size: 11.5px;
    font-weight: 600;
    color: #fff;
    text-shadow:
      0 1px 2px rgba(0, 0, 0, 0.85),
      0 0 3px rgba(0, 0, 0, 0.6);
    white-space: nowrap;
  }
  .tbar-name {
    left: 8px;
    max-width: 70%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tbar-time {
    right: 8px;
    font-family: var(--font-mono);
  }

  /* edit tree */
  .search-row {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 8px;
  }
  .search-in {
    flex: 1;
    min-width: 0;
  }
  .match-count {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .search-row .btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .tree {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  /* activity feed (locked bottom) */
  .activity {
    flex-shrink: 0;
    height: 150px;
    display: flex;
    flex-direction: column;
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
  }
  .act-title {
    flex-shrink: 0;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
    padding: 5px 12px 3px;
  }
  .act-list {
    flex: 1;
    overflow-y: auto;
    padding: 0 12px 6px;
  }
  .act-row {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-size: 11.5px;
    line-height: 1.7;
  }
  .act-time {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 10.5px;
    flex-shrink: 0;
  }
  .act-path {
    background: none;
    border: none;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11.5px;
    padding: 0;
    text-align: left;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .act-path:hover {
    color: var(--accent);
    text-decoration: underline;
  }
  .act-empty {
    color: var(--text-muted);
    font-size: 11.5px;
    padding: 6px 0;
  }

  /* context menu */
  .ctx {
    position: fixed;
    z-index: 60;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 4px;
    display: flex;
    flex-direction: column;
    min-width: 130px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
  }
  .ctx-item {
    background: none;
    border: none;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 5px 9px;
    border-radius: 4px;
    text-align: left;
  }
  .ctx-item:hover {
    background: rgba(255, 255, 255, 0.06);
  }
  .ctx-item.danger {
    color: #e05c5c;
  }

  /* modals */
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 50;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .modal {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 14px;
    width: 320px;
    display: flex;
    flex-direction: column;
    gap: 7px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal.form {
    width: 420px;
    max-height: 85%;
    overflow-y: auto;
  }
  .modal-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 4px;
  }
  .btn.save {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .in {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 5px 8px;
    outline: none;
    width: 100%;
  }
  .in:focus {
    border-color: var(--accent-dim);
  }
  .in.mono {
    font-family: var(--font-mono);
    font-size: 11px;
    resize: vertical;
  }
  .in.num {
    width: 110px;
  }
  .f-label {
    font-size: 11px;
    color: var(--text-secondary);
  }
  .f-chk {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .f-chk input {
    accent-color: var(--accent);
  }
  .f-grid {
    display: grid;
    grid-template-columns: 130px 1fr;
    gap: 6px 8px;
    align-items: center;
  }
  .f-sep {
    height: 1px;
    background: var(--border);
    margin: 3px 0;
  }
  .f-err {
    color: #e05c5c;
    font-size: 11.5px;
  }
</style>
