<script>
  import { onMount, onDestroy } from "svelte";
  import {
    GetClients,
    GetClientActivity,
    SetClientMuted,
  } from "../../bindings/FuseBridge/app.js";

  let clients = [];
  let activity = [];
  let error = "";
  let interval;

  // Version-bar ramp: newest → oldest as one gold hue, light → dark
  // (sequential — version recency is ordinal; validated against --bg-panel).
  const VER_COLORS = ["#ffe08a", "#c8a951", "#95783a", "#6b592d"];

  function verParts(v) {
    return String(v || "")
      .split(/[^0-9]+/)
      .filter(Boolean)
      .map(Number);
  }
  function cmpVer(a, b) {
    const pa = verParts(a),
      pb = verParts(b);
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
      const d = (pa[i] || 0) - (pb[i] || 0);
      if (d) return d;
    }
    return 0;
  }

  $: stats = computeStats(clients);
  function computeStats(list) {
    const active = list.filter((c) => c.status === "active").length;
    const connected = list.filter((c) => c.status === "connected").length;
    const byVer = new Map();
    for (const c of list) {
      const v = String(c.version || "").trim() || "unknown";
      byVer.set(v, (byVer.get(v) || 0) + 1);
    }
    const sorted = [...byVer.entries()].sort((a, b) => cmpVer(b[0], a[0]));
    const versions = sorted
      .slice(0, 3)
      .map(([label, count], i) => ({ label, count, color: VER_COLORS[i] }));
    const rest = sorted.slice(3);
    if (rest.length) {
      versions.push({
        label: "older",
        count: rest.reduce((s, [, n]) => s + n, 0),
        color: VER_COLORS[3],
      });
    }
    return { active, connected, total: list.length, versions };
  }

  function since(dateStr) {
    const s = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
    if (s < 60) return "just now";
    const m = Math.floor(s / 60);
    if (m < 60) return `${m} min ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h} hr ago`;
    return `${Math.floor(h / 24)} days ago`;
  }

  async function load() {
    try {
      clients = (await GetClients()) || [];
      activity = (await GetClientActivity()) || [];
      error = "";
    } catch (e) {
      error = String(e);
    }
  }

  async function toggleMute(c) {
    try {
      await SetClientMuted(c.id, !c.muted);
      clients = clients.map((x) =>
        x.id === c.id ? { ...x, muted: !c.muted } : x,
      );
    } catch (e) {
      error = String(e);
    }
  }

  onMount(async () => {
    await load();
    interval = setInterval(load, 10000);
  });
  onDestroy(() => clearInterval(interval));
</script>

<div class="clients">
  {#if error}
    <div class="msg error">{error}</div>
  {:else}
    <!-- One-line fleet dashboard -->
    {#if clients.length}
      <div class="dash">
        <div class="stat" title="Relaying log data in the last 30 min">
          <span class="dot active"></span>
          <span class="stat-val">{stats.active}</span>
          <span class="stat-label">active</span>
        </div>
        <div class="stat" title="Connected but no recent log data">
          <span class="dot connected"></span>
          <span class="stat-val">{stats.connected}</span>
          <span class="stat-label">connected</span>
        </div>
        <div class="stat" title="All registered installs">
          <span class="stat-val">{stats.total}</span>
          <span class="stat-label">installs</span>
        </div>
        <div class="dash-sep"></div>
        <div class="verbar" role="img" aria-label="Version distribution">
          {#each stats.versions as v (v.label)}
            <div
              class="verseg"
              style="flex-grow:{v.count}; background:{v.color}"
              title="{v.label} — {v.count} of {stats.total} ({Math.round(
                (v.count / stats.total) * 100,
              )}%)"
            ></div>
          {/each}
        </div>
        <div class="verchips">
          {#each stats.versions as v (v.label)}
            <span
              class="verchip"
              title="{v.count} of {stats.total} ({Math.round(
                (v.count / stats.total) * 100,
              )}%)"
            >
              <span class="versw" style="background:{v.color}"></span>
              <span class="mono">{v.label}</span>
              <span class="verct">{v.count}</span>
            </span>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Scrollable client list -->
    <div class="table-wrap">
      {#if !clients.length}
        <div class="msg">No clients registered</div>
      {:else}
        <table>
          <thead>
            <tr>
              <th class="c-status">Status</th>
              <th class="c-name">Name</th>
              <th class="c-toon">Toon</th>
              <th class="c-zone">Last Zone</th>
              <th class="c-ver">Version</th>
              <th class="c-seen">Last Seen</th>
              <th class="c-mute"></th>
            </tr>
          </thead>
          <tbody>
            {#each clients as c (c.id)}
              <tr
                class:connected={c.status === "active" ||
                  c.status === "connected"}
                class:mutedRow={c.muted}
              >
                <td class="c-status">
                  <span
                    class="dot {c.status}"
                    title={c.status === "active"
                      ? "Relaying log data"
                      : c.status === "connected"
                        ? "Connected (no recent log data)"
                        : "Offline"}
                  ></span>
                </td>
                <td class="c-name"
                  >{c.name}{#if c.muted}<span class="muted-tag">MUTED</span
                    >{/if}</td
                >
                <td class="c-toon">
                  {c.toon || "—"}{#if c.guild}<span class="guild"
                      >&lt;{c.guild}&gt;</span
                    >{/if}
                </td>
                <td class="c-zone">{c.last_zone || "—"}</td>
                <td class="c-ver mono">{c.version}</td>
                <td class="c-seen">{since(c.last_seen)}</td>
                <td class="c-mute">
                  <button
                    class="mute-btn"
                    class:muted={c.muted}
                    title={c.muted
                      ? "Resume accepting data from this client"
                      : "Ignore all data from this client"}
                    on:click={() => toggleMute(c)}
                    >{c.muted ? "Unmute" : "Mute"}</button
                  >
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>

    <!-- Activity panel pinned to the bottom -->
    <div class="activity">
      <div class="section-label">Activity</div>
      <div class="log">
        {#if !activity.length}
          <div class="log-empty">No recent activity</div>
        {:else}
          {#each [...activity].reverse() as line}
            <div class="log-line">{line}</div>
          {/each}
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .clients {
    display: flex;
    flex-direction: column;
    height: 100%;
    padding: 16px;
    overflow: hidden;
  }

  .msg {
    color: var(--text-muted);
    font-size: 12px;
    text-align: center;
    margin-top: 60px;
  }
  .msg.error {
    color: var(--error);
  }

  /* One-line fleet dashboard */
  .dash {
    display: flex;
    align-items: center;
    flex-shrink: 0;
    gap: 18px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 8px 14px;
    margin-bottom: 12px;
    font-size: 11px;
  }
  .stat {
    display: flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;
  }
  .stat-val {
    color: var(--text-primary);
    font-size: 15px;
    font-weight: 700;
  }
  .stat-label {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }
  .dash-sep {
    width: 1px;
    align-self: stretch;
    background: var(--border);
  }
  .verbar {
    display: flex;
    gap: 2px;
    flex: 1;
    min-width: 90px;
    max-width: 240px;
    height: 8px;
    border-radius: 4px;
    overflow: hidden;
  }
  .verseg {
    flex-basis: 0;
    min-width: 3px;
  }
  .verchips {
    display: flex;
    align-items: center;
    gap: 12px;
    white-space: nowrap;
  }
  .verchip {
    display: flex;
    align-items: center;
    gap: 5px;
    color: var(--text-secondary);
  }
  .versw {
    width: 7px;
    height: 7px;
    border-radius: 2px;
    flex-shrink: 0;
  }
  .verct {
    color: var(--text-primary);
    font-weight: 700;
  }

  /* Client list scrolls; activity stays put */
  .table-wrap {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }

  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--bg-panel);
    border-bottom: 1px solid var(--border);
    color: var(--text-secondary);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    padding: 8px 12px;
    text-align: left;
    text-transform: uppercase;
  }

  tbody tr {
    border-bottom: 1px solid var(--border);
  }
  tbody tr:hover {
    background: rgba(255, 255, 255, 0.03);
  }

  tbody td {
    color: var(--text-secondary);
    padding: 8px 12px;
  }
  tbody tr.connected td {
    color: var(--text-primary);
  }

  .c-status {
    width: 54px;
    text-align: center;
  }
  .c-ver {
    font-family: var(--font-mono);
    width: 80px;
  }
  .c-seen {
    width: 100px;
  }
  .c-zone {
    color: var(--text-secondary);
  }
  .mono {
    font-family: var(--font-mono);
  }
  .guild {
    color: var(--text-muted);
    margin-left: 5px;
    font-size: 11px;
  }

  .c-mute {
    width: 74px;
    text-align: right;
  }
  .mute-btn {
    background: none;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 10px;
    transition:
      color 0.15s,
      border-color 0.15s;
  }
  .mute-btn:hover {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }
  .mute-btn.muted {
    color: #e3a008;
    border-color: #e3a008;
  }
  .muted-tag {
    color: #ef4444;
    font-size: 9px;
    font-weight: 800;
    letter-spacing: 0.08em;
    margin-left: 6px;
    vertical-align: middle;
  }
  tr.mutedRow td {
    opacity: 0.55;
  }
  tr.mutedRow td.c-mute {
    opacity: 1;
  }

  .dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--text-muted);
  }
  .dot.active {
    background: var(--success);
    box-shadow: 0 0 5px var(--success);
  }
  .dot.connected {
    background: #e3a008;
    box-shadow: 0 0 5px #e3a008;
  }
  .dot.offline {
    background: var(--text-muted);
  }

  .activity {
    flex-shrink: 0;
    margin-top: 14px;
  }
  .section-label {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-muted);
    margin-bottom: 5px;
  }
  .log {
    height: 170px;
    overflow-y: auto;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 7px 10px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    line-height: 1.55;
  }
  .log-line {
    white-space: pre-wrap;
    word-break: break-word;
  }
  .log-empty {
    color: var(--text-muted);
    font-style: italic;
  }
</style>
