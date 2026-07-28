<script>
  // The share inbox: an in-place panel (anchored above the footer) listing
  // pending incoming shares. Every item shows the sender and a full
  // server-built preview; nothing applies until Accept. Decline deletes.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    GetShareInbox,
    AcceptTriggerShare,
    ResolveShare,
  } from "../../bindings/FuseBridge/app.js";

  export let onClose;

  let items = [];
  let busy = {}; // id -> true while resolving
  let notes = {}; // id -> transient error text
  let offInbox;

  async function load() {
    try {
      items = (await GetShareInbox()) || [];
    } catch {
      items = [];
    }
  }
  onMount(() => {
    load();
    offInbox = Events.On("share-inbox", load);
  });
  onDestroy(() => offInbox && offInbox());

  function meta(it) {
    try {
      return JSON.parse(it.meta || "{}");
    } catch {
      return {};
    }
  }
  function fmtWhen(ms) {
    if (!ms) return "";
    return new Date(ms).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
    });
  }
  function fmtTimer(secs) {
    const m = Math.floor(secs / 60);
    const s = secs % 60;
    return m ? `${m}m ${s}s` : `${s}s`;
  }

  // Merge accepted markers into the map's saved-marker store (the same
  // localStorage key MapTab reads), skipping exact duplicates, then poke any
  // open map view to reload.
  function mergeMarkers(zone, markers) {
    const KEY = "fuse.mapPOIs";
    let all = {};
    try {
      all = JSON.parse(localStorage.getItem(KEY) || "{}");
    } catch {
      all = {};
    }
    const zk = (zone || "").toLowerCase();
    const list = all[zk] || [];
    for (const m of markers || []) {
      const exists = list.some(
        (p) => p.name === m.name && p.x === m.x && p.y === m.y && p.z === m.z,
      );
      if (!exists) list.push({ name: m.name, x: m.x, y: m.y, z: m.z || 0 });
    }
    all[zk] = list;
    localStorage.setItem(KEY, JSON.stringify(all));
    window.dispatchEvent(
      new CustomEvent("fuse-pois-changed", { detail: { zone: zk } }),
    );
  }

  async function accept(it) {
    if (busy[it.id]) return;
    busy = { ...busy, [it.id]: true };
    notes = { ...notes, [it.id]: "" };
    try {
      if (it.kind === "trigger") {
        await AcceptTriggerShare(it.id);
      } else if (it.kind === "markers") {
        const p = JSON.parse(it.payload || "{}");
        mergeMarkers(p.zone, p.markers);
        await ResolveShare(it.id, true);
      } else {
        await ResolveShare(it.id, false);
      }
      items = items.filter((x) => x.id !== it.id);
    } catch (e) {
      notes = { ...notes, [it.id]: String(e) };
    }
    busy = { ...busy, [it.id]: false };
  }

  async function decline(it) {
    if (busy[it.id]) return;
    busy = { ...busy, [it.id]: true };
    try {
      await ResolveShare(it.id, false);
      items = items.filter((x) => x.id !== it.id);
    } catch (e) {
      notes = { ...notes, [it.id]: String(e) };
    }
    busy = { ...busy, [it.id]: false };
  }
</script>

<div class="ib-panel">
  <div class="ib-head">
    <span class="ib-title">Shared with you</span>
    <button class="ib-x" title="Close" on:click={() => onClose && onClose()}
      >✕</button
    >
  </div>

  {#if !items.length}
    <div class="ib-empty">
      Nothing pending. When another player shares a trigger or map markers with
      you, it shows up here.
    </div>
  {:else}
    <div class="ib-list">
      {#each items as it (it.id)}
        {@const m = meta(it)}
        <div class="ib-item">
          <div class="ib-from">
            <span class="ib-name">{it.from_name || "(unnamed)"}</span>
            <span class="ib-addr">#{it.from_addr}</span>
            {#if it.from_linked}<span class="ib-badge linked">linked</span
              >{:else}<span class="ib-badge">unverified</span>{/if}
            <span class="ib-when">{fmtWhen(it.created_ms)}</span>
          </div>

          {#if it.kind === "trigger"}
            <div class="ib-kind">Trigger</div>
            <div class="ib-body">
              <div class="ib-line ib-strong">{m.name || "(unnamed)"}</div>
              {#if m.category}<div class="ib-line">
                  Category: {m.category}
                </div>{/if}
              {#if m.trigger_text}<div class="ib-line">
                  Matches: <span class="ib-mono">{m.trigger_text}</span>
                </div>{/if}
              {#if m.timer_seconds}<div class="ib-line">
                  Timer: {fmtTimer(m.timer_seconds)}
                </div>{/if}
              {#if m.display_text}<div class="ib-line">
                  Alert: {m.display_text}
                </div>{/if}
              {#if m.tts_text}<div class="ib-line">
                  Speaks: {m.tts_text}
                </div>{/if}
              {#if m.media_file}<div class="ib-line ib-dim">
                  Plays {m.media_file} — sound file not included; it only plays
                  if you already have it.
                </div>{/if}
              <div class="ib-line ib-dim">
                Accept adds it under Personal &gt; Shared.
              </div>
            </div>
          {:else if it.kind === "markers"}
            <div class="ib-kind">Map markers</div>
            <div class="ib-body">
              <div class="ib-line ib-strong">
                {m.count || 0} marker{(m.count || 0) === 1 ? "" : "s"} in {m.zone ||
                  "?"}
              </div>
              {#if m.names && m.names.length}
                <div class="ib-line">
                  {m.names.join(", ")}{#if (m.count || 0) > m.names.length}…{/if}
                </div>
              {/if}
              <div class="ib-line ib-dim">
                Accept saves them to your map for that zone.
              </div>
            </div>
          {:else if it.kind === "note"}
            <!-- Server-generated notice. Nothing to install; either button
                 just clears it. -->
            <div class="ib-kind">Note</div>
            <div class="ib-body">
              <div class="ib-line ib-strong">{m.text || it.payload}</div>
            </div>
          {/if}

          {#if notes[it.id]}<div class="ib-err">{notes[it.id]}</div>{/if}
          <div class="ib-btns">
            {#if it.kind === "note"}
              <!-- A note carries nothing to install, so Accept/Decline would be
                   a meaningless choice — one Dismiss button instead. -->
              <button
                class="ib-btn ib-accept"
                disabled={busy[it.id]}
                on:click={() => decline(it)}>Dismiss</button
              >
            {:else}
              <button
                class="ib-btn ib-decline"
                disabled={busy[it.id]}
                on:click={() => decline(it)}>Decline</button
              >
              <button
                class="ib-btn ib-accept"
                disabled={busy[it.id]}
                on:click={() => accept(it)}>Accept</button
              >
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .ib-panel {
    position: absolute;
    right: 8px;
    bottom: 28px;
    width: 340px;
    max-width: 92vw;
    max-height: 60vh;
    display: flex;
    flex-direction: column;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 8px 28px rgba(0, 0, 0, 0.5);
    z-index: 150;
    overflow: hidden;
  }
  .ib-head {
    display: flex;
    align-items: center;
    padding: 9px 12px;
    border-bottom: 1px solid var(--border);
  }
  .ib-title {
    font-size: 13px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .ib-x {
    margin-left: auto;
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 13px;
    padding: 0 2px;
  }
  .ib-x:hover {
    color: var(--text-primary);
  }
  .ib-empty {
    padding: 16px 14px;
    font-size: 12px;
    color: var(--text-muted);
    line-height: 1.5;
  }
  .ib-list {
    overflow-y: auto;
    display: flex;
    flex-direction: column;
  }
  .ib-item {
    padding: 10px 12px;
    border-bottom: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .ib-item:last-child {
    border-bottom: none;
  }
  .ib-from {
    display: flex;
    align-items: baseline;
    gap: 5px;
    flex-wrap: wrap;
  }
  .ib-name {
    font-weight: 700;
    font-size: 12.5px;
    color: var(--text-primary);
  }
  .ib-addr {
    font-size: 11px;
    color: var(--text-muted);
  }
  .ib-badge {
    font-size: 9.5px;
    font-weight: 700;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-muted);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
  }
  .ib-badge.linked {
    color: var(--success);
    border-color: var(--success);
  }
  .ib-when {
    margin-left: auto;
    font-size: 10.5px;
    color: var(--text-muted);
  }
  .ib-kind {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e3a008;
  }
  .ib-body {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .ib-line {
    font-size: 12px;
    color: var(--text-secondary);
    word-break: break-word;
  }
  .ib-strong {
    color: var(--text-primary);
    font-weight: 600;
  }
  .ib-dim {
    color: var(--text-muted);
    font-size: 11px;
  }
  .ib-mono {
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .ib-err {
    font-size: 11.5px;
    color: #ff6b6b;
  }
  .ib-btns {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 2px;
  }
  .ib-btn {
    background: none;
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 3px 12px;
    font-size: 12px;
    cursor: pointer;
    color: var(--text-primary);
  }
  .ib-btn:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .ib-accept {
    border-color: var(--success);
    color: var(--success);
  }
  .ib-accept:hover:not(:disabled) {
    background: rgba(52, 168, 83, 0.12);
  }
  .ib-decline:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.06);
  }
</style>
