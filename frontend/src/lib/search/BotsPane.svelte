<script context="module">
  // Survives sub-tab switches so returning to Bots doesn't flash empty.
  let savedBots = [];
  let savedFilter = "";
  let savedZoneF = "";
  let savedClassF = "";
  let savedMobF = "";
</script>

<script>
  // Search → Bots: the guild's shared bot characters, served by the King
  // Ak'Anon Discord bot through the FuseBridge server. Full info including
  // credentials (via Details/Claim), claiming, and the batphone tie-in panel.
  // Access rules (NOBOTS, per-bot roles) are enforced by King Ak'Anon itself —
  // bots above this member's reach simply never appear here.
  import { onMount, onDestroy } from "svelte";
  import {
    GetGuildBots,
    GetGuildBotDetail,
    ClaimGuildBot,
    GetBatphoneBots,
    ParkGuildBot,
    GetGuildBotZones,
  } from "../../../bindings/FuseBridge/app.js";
  import { linked } from "../linkState.js";
  import { activeTab } from "../nav.js";
  import { classAbbr } from "../classAbbr.js";
  import AlertBots from "./AlertBots.svelte";
  import BotCredsModal from "./BotCredsModal.svelte";

  let bots = savedBots;
  let filter = savedFilter;
  let zoneF = savedZoneF;
  let classF = savedClassF;
  let mobF = savedMobF;
  let loading = savedBots.length === 0;
  let err = "";
  let note = "";
  let noteTimer;
  let bp = null;
  let now = Date.now();
  let pollTimer, tickTimer;
  let inflight = "";
  let sortKey = "";
  let sortDir = 1;

  // Inline park editing (zone / note — the API twin of /botpark).
  let editing = null; // { name, field: "zone"|"note", value }
  let editSaving = false;
  let zonesList = null; // KA's canonical zone list, fetched on first edit

  // Bot modal state, rendered by the shared BotCredsModal: Details opens the
  // full /botinfo-style view, a successful Claim opens the compact login view.
  let detail = null;
  let detailTitle = "";
  let detailFull = false;

  $: savedBots = bots;
  $: savedFilter = filter;
  $: savedZoneF = zoneF;
  $: savedClassF = classF;
  $: savedMobF = mobF;

  async function load() {
    if (!$linked) {
      loading = false;
      return;
    }
    try {
      bots = (await GetGuildBots()) || [];
      err = "";
    } catch (e) {
      err = String(e?.message || e || "load failed");
    }
    try {
      bp = await GetBatphoneBots();
    } catch {
      bp = null;
    }
    loading = false;
  }

  onMount(() => {
    load();
    pollTimer = setInterval(load, 15000);
    tickTimer = setInterval(() => (now = Date.now()), 1000);
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    clearInterval(tickTimer);
    clearTimeout(noteTimer);
  });

  let prevLinked = $linked;
  $: if ($linked !== prevLinked) {
    prevLinked = $linked;
    if ($linked) {
      loading = true;
      load();
    }
  }

  function flash(msg) {
    note = msg;
    clearTimeout(noteTimer);
    noteTimer = setTimeout(() => (note = ""), 5000);
  }

  function isClaimed(b) {
    return !!b.claimed_by && b.claim_expires_ms > now;
  }
  function claimLeft(b) {
    const s = Math.max(0, Math.floor((b.claim_expires_ms - now) / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }
  function ago(ms) {
    if (!ms) return "";
    const s = Math.max(0, Math.floor((now - ms) / 1000));
    if (s < 3600) return `${Math.max(1, Math.floor(s / 60))}m ago`;
    if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
    return `${Math.floor(s / 86400)}d ago`;
  }

  // Dropdown options come from the roster itself, so they only ever offer
  // values that exist.
  $: zoneOptions = [
    ...new Set(bots.map((b) => b.park_zone).filter(Boolean)),
  ].sort();
  $: classOptions = [
    ...new Set(bots.map((b) => b.class).filter(Boolean)),
  ].sort();
  $: mobOptions = [...new Set(bots.flatMap((b) => b.alerts || []))].sort();

  function sortBy(key) {
    if (sortKey === key) sortDir = -sortDir;
    else {
      sortKey = key;
      sortDir = 1;
    }
  }
  function sortVal(b, key) {
    switch (key) {
      case "status":
        return isClaimed(b) ? 1 : 0;
      case "name":
        return b.name || "";
      case "cls":
        return `${b.class || ""} ${String(b.level || 0).padStart(2, "0")}`;
      case "zone":
        return b.park_zone || "";
      case "note":
        return b.park_note || b.note || "";
      case "alerts":
        return (b.alerts || []).length;
      default:
        return "";
    }
  }

  $: shown = (() => {
    const q = filter.trim().toLowerCase();
    let out = bots.filter((b) => {
      if (zoneF && b.park_zone !== zoneF) return false;
      if (classF && b.class !== classF) return false;
      if (mobF && !(b.alerts || []).includes(mobF)) return false;
      if (!q) return true;
      return (
        b.name.toLowerCase().includes(q) ||
        (b.class || "").toLowerCase().includes(q) ||
        (b.park_zone || "").toLowerCase().includes(q) ||
        (b.alerts || []).some((m) => m.toLowerCase().includes(q))
      );
    });
    if (sortKey) {
      out = [...out].sort((a, b) => {
        const av = sortVal(a, sortKey),
          bv = sortVal(b, sortKey);
        let c;
        if (typeof av === "number") c = av - bv;
        else c = String(av).localeCompare(String(bv));
        if (c !== 0) return c * sortDir;
        return a.name.localeCompare(b.name);
      });
    }
    return out;
  })();

  async function startEdit(b, field) {
    if (editSaving) return;
    editing = {
      name: b.name,
      field,
      value: field === "zone" ? b.park_zone || "" : b.park_note || "",
    };
    if (zonesList === null) {
      try {
        zonesList = (await GetGuildBotZones()) || [];
      } catch {
        zonesList = [];
      }
    }
  }
  function cancelEdit() {
    editing = null;
  }
  async function saveEdit(b) {
    if (!editing || editSaving) return;
    const zone = editing.field === "zone" ? editing.value.trim() : b.park_zone;
    const parkNote =
      editing.field === "note" ? editing.value.trim() : b.park_note;
    editSaving = true;
    try {
      await ParkGuildBot(b.name, zone, parkNote);
      flash(`${b.name} park updated.`);
      editing = null;
      await load();
    } catch (e) {
      flash(String(e?.message || e));
    }
    editSaving = false;
  }
  function editKey(e, b) {
    if (e.key === "Enter") saveEdit(b);
    else if (e.key === "Escape") cancelEdit();
  }
  // Svelte action: focus the inline editor when it appears.
  function autofocus(node) {
    node.focus();
    node.select?.();
  }

  async function openDetails(b) {
    inflight = b.name;
    try {
      detail = await GetGuildBotDetail(b.name);
      detailTitle = "Bot Info";
      detailFull = true;
    } catch (e) {
      flash(String(e?.message || e));
    }
    inflight = "";
  }

  async function claim(b) {
    // No confirm step: one click claims and the login modal opens (same flow
    // as the batphone AlertBots panel). The Discord-announcement disclosure
    // lives on the button's hover title.
    if (inflight) return;
    inflight = b.name;
    try {
      detail = await ClaimGuildBot(b.name);
      detailTitle = "Claimed — Bot Login";
      detailFull = false;
      flash(`${b.name} claimed for 5 minutes.`);
      load();
    } catch (e) {
      flash(String(e?.message || e));
      load();
    }
    inflight = "";
  }
</script>

<div class="bots-pane">
  {#if !$linked}
    <div class="empty">
      <div class="big">Link your Discord account</div>
      <div class="hint">
        Guild bot information requires a verified Fuse membership.
      </div>
      <button class="link-btn" on:click={() => activeTab.set("general")}
        >Link your account on the General tab →</button
      >
    </div>
  {:else if loading}
    <div class="empty">Loading bots…</div>
  {:else if err}
    <div class="empty">
      <div class="big">Bots unavailable</div>
      <div class="hint">{err}</div>
    </div>
  {:else}
    <AlertBots {bp} on:changed={load} />

    <div class="controls">
      <input
        class="filter"
        placeholder="Filter by name, class, zone, or mob alert…"
        bind:value={filter}
      />
      <label class="flabel"
        >Zone
        <select class="fsel" bind:value={zoneF}>
          <option value="">All</option>
          {#each zoneOptions as z}
            <option value={z}>{z}</option>
          {/each}
        </select>
      </label>
      <label class="flabel"
        >Class
        <select class="fsel" bind:value={classF}>
          <option value="">All</option>
          {#each classOptions as c}
            <option value={c}>{c}</option>
          {/each}
        </select>
      </label>
      <label class="flabel"
        >Mob
        <select class="fsel" bind:value={mobF}>
          <option value="">All</option>
          {#each mobOptions as m}
            <option value={m}>{m}</option>
          {/each}
        </select>
      </label>
      <span class="count"
        >{shown.length} bot{shown.length === 1 ? "" : "s"}</span
      >
      {#if note}<span class="note">{note}</span>{/if}
    </div>

    <div class="table-wrap">
      {#if !shown.length}
        <div class="msg">No bots match.</div>
      {:else}
        <table>
          <thead>
            <tr>
              <th class="c-status sortable" on:click={() => sortBy("status")}
                >{#if sortKey === "status"}<span class="arrow"
                    >{sortDir === 1 ? "▲" : "▼"}</span
                  >{/if}</th
              >
              <th class="c-name sortable" on:click={() => sortBy("name")}
                >Name{#if sortKey === "name"}<span class="arrow"
                    >{sortDir === 1 ? "▲" : "▼"}</span
                  >{/if}</th
              >
              <th class="c-cls sortable" on:click={() => sortBy("cls")}
                >Cls/Lvl{#if sortKey === "cls"}<span class="arrow"
                    >{sortDir === 1 ? "▲" : "▼"}</span
                  >{/if}</th
              >
              <th class="c-zone sortable" on:click={() => sortBy("zone")}
                >Zone{#if sortKey === "zone"}<span class="arrow"
                    >{sortDir === 1 ? "▲" : "▼"}</span
                  >{/if}</th
              >
              <th class="c-note sortable" on:click={() => sortBy("note")}
                >Note{#if sortKey === "note"}<span class="arrow"
                    >{sortDir === 1 ? "▲" : "▼"}</span
                  >{/if}</th
              >
              <th class="c-alerts sortable" on:click={() => sortBy("alerts")}
                >Alerts{#if sortKey === "alerts"}<span class="arrow"
                    >{sortDir === 1 ? "▲" : "▼"}</span
                  >{/if}</th
              >
              <th class="c-act"></th>
            </tr>
          </thead>
          <tbody>
            {#each shown as b (b.name)}
              <tr class:claimedRow={isClaimed(b)}>
                <td class="c-status">
                  <span
                    class="dot"
                    class:red={isClaimed(b)}
                    title={isClaimed(b)
                      ? `Claimed by ${b.claimed_by} — ${claimLeft(b)} left`
                      : "Available"}
                  ></span>
                </td>
                <td class="c-name">{b.name}</td>
                <td class="c-cls">{classAbbr(b.class)} {b.level || "?"}</td>
                <td class="c-zone">
                  {#if editing && editing.name === b.name && editing.field === "zone"}
                    <input
                      class="edit-inp"
                      list="bot-zone-list"
                      bind:value={editing.value}
                      disabled={editSaving}
                      use:autofocus
                      on:keydown={(e) => editKey(e, b)}
                      on:blur={cancelEdit}
                    />
                  {:else}
                    <button
                      class="cellbtn"
                      title="Click to move this bot (same as /botpark)"
                      on:click={() => startEdit(b, "zone")}
                      >{b.park_zone || "—"}{#if b.park_time_ms}<span
                          class="sub"
                        >
                          {ago(b.park_time_ms)}</span
                        >{/if}<span class="pencil">✎</span></button
                    >
                  {/if}
                </td>
                <td class="c-note">
                  {#if editing && editing.name === b.name && editing.field === "note"}
                    <input
                      class="edit-inp"
                      bind:value={editing.value}
                      disabled={editSaving}
                      use:autofocus
                      on:keydown={(e) => editKey(e, b)}
                      on:blur={cancelEdit}
                    />
                  {:else}
                    <button
                      class="cellbtn trunc"
                      title={(b.park_note || b.note || "") +
                        "\nClick to edit the park note"}
                      on:click={() => startEdit(b, "note")}
                      >{b.park_note || b.note || "—"}<span class="pencil"
                        >✎</span
                      ></button
                    >
                  {/if}
                </td>
                <td class="c-alerts" title={(b.alerts || []).join(", ")}
                  >{(b.alerts || []).join(", ") || "—"}</td
                >
                <td class="c-act">
                  <button
                    class="act"
                    disabled={inflight === b.name}
                    on:click={() => openDetails(b)}>Details</button
                  >
                  {#if isClaimed(b)}
                    <span class="claimtag" title="Claimed by {b.claimed_by}"
                      >{claimLeft(b)}</span
                    >
                  {:else}
                    <button
                      class="act gold"
                      disabled={inflight === b.name}
                      title="Claims for 5 minutes — announced in the bots-data Discord channel under your name."
                      on:click={() => claim(b)}
                      >{inflight === b.name ? "Claiming…" : "Claim"}</button
                    >
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        <datalist id="bot-zone-list">
          {#each zonesList || [] as z}
            <option value={z}></option>
          {/each}
        </datalist>
      {/if}
    </div>
  {/if}
</div>

{#if detail}
  <BotCredsModal
    {detail}
    title={detailTitle}
    full={detailFull}
    onClose={() => (detail = null)}
  />
{/if}

<style>
  .bots-pane {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }
  .empty {
    padding: 40px 20px;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
  }
  .empty .big {
    font-size: 15px;
    font-weight: 600;
    color: var(--text-secondary);
    margin-bottom: 6px;
  }
  .empty .hint {
    margin-bottom: 12px;
  }
  .link-btn {
    background: none;
    border: 1px solid rgba(96, 165, 250, 0.55);
    color: #60a5fa;
    border-radius: 4px;
    padding: 4px 12px;
    font-size: 12px;
    cursor: pointer;
  }

  .controls {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 12px 8px;
  }
  .filter {
    background: var(--bg-input);
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 4px;
    padding: 5px 8px;
    font-size: 12px;
    width: 320px;
    max-width: 60%;
  }
  .filter:focus {
    outline: none;
    border-color: var(--border-hover);
  }
  .flabel {
    display: flex;
    align-items: center;
    gap: 5px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .fsel {
    background: var(--bg-input);
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 4px;
    padding: 4px 6px;
    font-size: 12px;
  }
  .count {
    color: var(--text-muted);
    font-size: 11px;
  }
  .note {
    color: var(--accent);
    font-size: 11px;
  }

  .table-wrap {
    flex: 1;
    min-height: 0;
    overflow: auto;
    margin: 0 12px 12px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg-secondary);
  }
  .msg {
    padding: 16px;
    color: var(--text-muted);
    font-size: 12px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th {
    text-align: left;
    padding: 6px 8px;
    color: var(--text-muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    background: var(--bg-secondary);
    z-index: 1;
  }
  th.sortable {
    cursor: pointer;
    user-select: none;
  }
  .arrow {
    margin-left: 2px;
    color: var(--accent);
  }
  td {
    padding: 5px 8px;
    border-bottom: 1px solid var(--border);
    color: var(--text-secondary);
    vertical-align: middle;
  }
  tr:last-child td {
    border-bottom: none;
  }
  .c-name {
    color: var(--text-primary);
    font-weight: 600;
  }
  .c-note,
  .c-alerts {
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Editable cells (zone + park note) render as quiet buttons; the pencil
     only shows on hover so the table reads as a table. */
  .cellbtn {
    background: none;
    border: none;
    color: inherit;
    font: inherit;
    padding: 0;
    cursor: pointer;
    text-align: left;
    max-width: 100%;
  }
  .cellbtn.trunc {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
  }
  .pencil {
    margin-left: 5px;
    color: var(--text-muted);
    font-size: 10px;
    opacity: 0;
  }
  tr:hover .pencil {
    opacity: 1;
  }
  .edit-inp {
    background: var(--bg-input);
    border: 1px solid var(--accent-dim, var(--border));
    color: var(--text-primary);
    border-radius: 3px;
    padding: 2px 5px;
    font-size: 12px;
    width: 100%;
    box-sizing: border-box;
  }
  .edit-inp:focus {
    outline: none;
    border-color: var(--accent);
  }
  .claimedRow td {
    opacity: 0.75;
  }
  .sub {
    color: var(--text-muted);
    font-size: 10px;
  }
  .dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--success, #6bbf6b);
  }
  .dot.red {
    background: var(--error, #e05c5c);
  }
  .act {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 2px 8px;
    font-size: 11px;
    cursor: pointer;
  }
  .act:hover:not(:disabled) {
    border-color: var(--border-hover);
    color: var(--text-primary);
  }
  .act:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .act.gold {
    color: var(--accent);
    border-color: var(--accent-dim, var(--border));
  }
  .claimtag {
    color: var(--error, #e05c5c);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    margin-left: 4px;
  }
</style>
