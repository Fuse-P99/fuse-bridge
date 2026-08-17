<script>
  import { onMount, onDestroy } from "svelte";
  import {
    GetClients,
    GetClientActivity,
    SetClientMuted,
    GetOtherGuildChat,
    GetCrossGuildToons,
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

  // The version bar's four gradations, newest → oldest, derived from the
  // server's authoritative current build (latest_version on /clients):
  //   current  — the current build or newer
  //   a.b.x    — behind within the current minor
  //   a.b-1.x  — the previous minor
  //   older    — everything earlier, and unknown versions
  // Fallback for a server that predates latest_version: the highest version
  // any client reports stands in for current.
  $: stats = computeStats(clients, latestVersion);
  function computeStats(list, latest) {
    const active = list.filter((c) => c.status === "active").length;
    const connected = list.filter((c) => c.status === "connected").length;
    let cur = String(latest || "").trim();
    if (!cur) {
      for (const c of list) {
        const v = String(c.version || "").trim();
        if (v && (!cur || cmpVer(v, cur) > 0)) cur = v;
      }
    }
    const floor = verParts(cur);
    const hasFloor = floor.length >= 2;
    const sameMinor = hasFloor ? `${floor[0]}.${floor[1]}.x` : "minor";
    const prevMinor = hasFloor ? `${floor[0]}.${floor[1] - 1}.x` : "prev";
    const buckets = [
      {
        label: "current",
        hint: `Up to date — ${cur || "?"} or newer`,
        count: 0,
        color: VER_COLORS[0],
      },
      {
        label: sameMinor,
        hint: `Behind within the ${sameMinor} minor — older than ${cur}`,
        count: 0,
        color: VER_COLORS[1],
      },
      {
        label: prevMinor,
        hint: `On the previous minor (${prevMinor})`,
        count: 0,
        color: VER_COLORS[2],
      },
      {
        label: "older",
        hint: "Anything earlier, and unknown versions",
        count: 0,
        color: VER_COLORS[3],
      },
    ];
    for (const c of list) {
      const p = verParts(c.version);
      let b = 3;
      if (hasFloor && p.length) {
        if (cmpVer(c.version, cur) >= 0) b = 0;
        else if (p[0] === floor[0] && p[1] === floor[1]) b = 1;
        else if (p[0] === floor[0] && p[1] === floor[1] - 1) b = 2;
      }
      buckets[b].count++;
    }
    return {
      active,
      connected,
      total: list.length,
      versions: buckets.filter((v) => v.count > 0),
    };
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

  let latestVersion = ""; // the server's authoritative current build

  // Other-guild chat feeds — competition chatter overheard by claimed alts
  // parked in non-Fuse guilds. Officer-level like the rest of this tab; the
  // server enforces that itself (non-officers get an empty list back), so the
  // panels simply render whenever data comes back.
  let guildFeeds = [];
  // Cross-guild report: characters on members' installs whose latest /who
  // sighting puts them in a non-Fuse guild. Same officer gating as the feeds.
  let crossGuild = [];
  // Bottom panel tab: "activity" (default) | "crossguild" | "feed:<guild>".
  let bottomTab = "activity";
  $: curFeed = guildFeeds.find((f) => "feed:" + f.guild === bottomTab) || null;
  // A selected tab whose data vanished (feed aged out, report emptied on a
  // refresh) falls back to Activity rather than showing a blank panel.
  $: if (
    (bottomTab === "crossguild" && !crossGuild.length) ||
    (bottomTab.startsWith("feed:") &&
      !guildFeeds.some((f) => "feed:" + f.guild === bottomTab))
  ) {
    bottomTab = "activity";
  }

  function feedTime(ms) {
    return new Date(ms).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    });
  }
  // "Soandso tells the guild, 'msg'" → "Soandso: msg" for a denser feed.
  function fmtFeedLine(line) {
    const m = line.match(/^(\w+) tells the guild, '(.*)'\s*$/);
    return m ? `${m[1]}: ${m[2]}` : line;
  }

  async function load() {
    try {
      const cl = (await GetClients()) || {};
      clients = cl.clients || [];
      latestVersion = cl.latest_version || "";
      activity = (await GetClientActivity()) || [];
      guildFeeds = (await GetOtherGuildChat().catch(() => [])) || [];
      crossGuild = (await GetCrossGuildToons().catch(() => [])) || [];
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
              title="{v.hint} — {v.count} of {stats.total} ({Math.round(
                (v.count / stats.total) * 100,
              )}%)"
            ></div>
          {/each}
        </div>
        <div class="verchips">
          {#each stats.versions as v (v.label)}
            <span
              class="verchip"
              title="{v.hint} — {v.count} of {stats.total} ({Math.round(
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

    <!-- Bottom panel pinned under the table, tabbed: Activity (default),
         the cross-guild character report, and one tab per overheard guild's
         chat feed. Intel tabs only exist when their data does. -->
    <div class="bottom-panels">
      <div class="activity">
        <div class="ogc-tabs">
          <button
            class="ogc-tab"
            class:active={bottomTab === "activity"}
            on:click={() => (bottomTab = "activity")}>Activity</button
          >
          {#if crossGuild.length}
            <button
              class="ogc-tab"
              class:active={bottomTab === "crossguild"}
              on:click={() => (bottomTab = "crossguild")}
              title="Characters found on members' installs whose latest /who sighting places them in a non-Fuse guild"
              >Cross-Guild Characters ({crossGuild.length})</button
            >
          {/if}
          {#each guildFeeds as f (f.guild)}
            <button
              class="ogc-tab"
              class:active={bottomTab === "feed:" + f.guild}
              on:click={() => (bottomTab = "feed:" + f.guild)}
              >{f.guild} ({f.lines.length})</button
            >
          {/each}
        </div>
        {#if bottomTab === "activity"}
          <div class="log">
            {#if !activity.length}
              <div class="log-empty">No recent activity</div>
            {:else}
              {#each [...activity].reverse() as line}
                <div class="log-line">{line}</div>
              {/each}
            {/if}
          </div>
        {:else if bottomTab === "crossguild"}
          <div class="log">
            {#each crossGuild as cg, i (cg.client + cg.toon)}
              {#if i === 0 || crossGuild[i - 1].client !== cg.client}
                <div class="log-line cg-client">{cg.client}</div>
              {/if}
              <div class="log-line cg-row">
                {cg.toon}
                <span class="cg-guild">&lt;{cg.guild}&gt;</span>
                <span
                  class="ogc-time"
                  title="last /who sighting in that guild"
                  >{since(new Date(cg.guild_seen_ms).toISOString())}</span
                >
              </div>
            {/each}
          </div>
        {:else if curFeed}
          <div class="log">
            {#each [...curFeed.lines].reverse() as l (l.at_ms + l.line)}
              <div class="log-line">
                <span class="ogc-time">{feedTime(l.at_ms)}</span>{fmtFeedLine(
                  l.line,
                )}
              </div>
            {/each}
          </div>
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

  .bottom-panels {
    display: flex;
    gap: 14px;
    flex-shrink: 0;
    margin-top: 14px;
  }
  .activity {
    flex: 1;
    min-width: 0;
  }
  .ogc-tabs {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-bottom: 5px;
  }
  .ogc-tab {
    background: none;
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 10px;
    padding: 1px 8px;
  }
  .ogc-tab.active {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .ogc-time {
    color: var(--text-muted);
    margin-right: 6px;
  }
  .cg-client {
    color: var(--text-primary);
    font-weight: 700;
  }
  .cg-row {
    padding-left: 14px;
  }
  .cg-guild {
    color: #e3a008;
    margin: 0 5px;
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
