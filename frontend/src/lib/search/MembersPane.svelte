<script context="module">
  // Survives sub-tab switches.
  let savedQuery = "";
  let savedInfo = null;
</script>

<script>
  // Search → Members: the /who Discord command's card, in-app. Autocomplete
  // over Discord handles, display names, AND toon names (toons shown as
  // "Xyzzy — Owner"); the lookup resolves through the same fallback chain
  // (handle → display name → toon owner).
  import { onDestroy } from "svelte";
  import {
    SearchMembers,
    GetMemberInfo,
  } from "../../../bindings/FuseBridge/app.js";
  import { linked } from "../linkState.js";
  import { activeTab } from "../nav.js";
  import { classAbbr } from "../classAbbr.js";

  let query = savedQuery;
  let info = savedInfo;
  let sugs = [];
  let sugOpen = false;
  let loading = false;
  let err = "";
  let debounceTimer;
  let seq = 0;

  $: savedQuery = query;
  $: savedInfo = info;

  function onInput() {
    clearTimeout(debounceTimer);
    const q = query.trim();
    if (q.length < 2) {
      sugs = [];
      sugOpen = false;
      return;
    }
    debounceTimer = setTimeout(async () => {
      const mySeq = ++seq;
      try {
        const got = (await SearchMembers(q)) || [];
        if (mySeq === seq) {
          sugs = got;
          sugOpen = got.length > 0;
        }
      } catch {
        if (mySeq === seq) {
          sugs = [];
          sugOpen = false;
        }
      }
    }, 300);
  }

  function onKey(e) {
    if (e.key === "Enter") {
      sugOpen = false;
      lookup(query.trim());
    } else if (e.key === "Escape") {
      sugOpen = false;
    }
  }

  function pick(name) {
    query = name;
    sugOpen = false;
    lookup(name);
  }

  async function lookup(name) {
    if (!name) return;
    clearTimeout(debounceTimer);
    loading = true;
    err = "";
    try {
      info = await GetMemberInfo(name);
    } catch (e) {
      err = String(e?.message || e);
      info = null;
    }
    loading = false;
  }

  // Discord shows these as relative timestamps (<t:…:R>) — mirror that.
  function rel(ms) {
    if (!ms) return "";
    const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
    if (s < 3600) return `${Math.max(1, Math.floor(s / 60))} minutes ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} hours ago`;
    if (s < 60 * 86400) return `${Math.floor(s / 86400)} days ago`;
    if (s < 730 * 86400) return `${Math.floor(s / (30 * 86400))} months ago`;
    return `${Math.floor(s / (365 * 86400))} years ago`;
  }
  function fmtDate(ms) {
    if (!ms) return "";
    return new Date(ms).toISOString().slice(0, 10);
  }

  onDestroy(() => clearTimeout(debounceTimer));
</script>

<div class="members-pane">
  {#if !$linked}
    <div class="empty">
      <div class="big">Link your Discord account</div>
      <div class="hint">Member lookup requires a verified Fuse membership.</div>
      <button class="link-btn" on:click={() => activeTab.set("general")}
        >Link your account on the General tab →</button
      >
    </div>
  {:else}
    <div class="controls">
      <div class="acwrap">
        <input
          class="inp"
          placeholder="Discord display name or handle… (2+ characters)"
          bind:value={query}
          on:input={onInput}
          on:keydown={onKey}
          on:blur={() => setTimeout(() => (sugOpen = false), 150)}
        />
        {#if sugOpen}
          <div class="sugs">
            {#each sugs as s (s.kind + ":" + s.value + ":" + s.label)}
              <button class="sug" on:mousedown={() => pick(s.value)}>
                <span class="sug-label">{s.label}</span>
                <span class="sug-kind">{s.kind}</span>
              </button>
            {/each}
          </div>
        {/if}
      </div>
      <button
        class="go"
        disabled={loading}
        on:click={() => lookup(query.trim())}
        >{loading ? "Looking up…" : "Look up"}</button
      >
      {#if err}<span class="errline">{err}</span>{/if}
    </div>

    <div class="results">
      {#if !info}
        <div class="msg">
          Look up any member — status, DKP, attendance, recent purchases, and
          their characters. The same card as /who in Discord.
        </div>
      {:else}
        <div class="card">
          <div class="card-head">
            {#if info.avatar_url}
              <img class="avatar" src={info.avatar_url} alt="" />
            {/if}
            <div>
              <div class="dname">{info.display_name || info.handle}</div>
              <div class="handle">{info.handle}</div>
            </div>
          </div>

          <div class="sec">Member Information</div>
          <div class="kv">
            <span class="k">Status</span><span class="v"
              >{info.rostered ? "Rostered Member" : "Not on Roster"}</span
            >
          </div>
          {#if info.rostered && info.date_rostered_ms}
            <div class="kv">
              <span class="k">Date Rostered</span><span
                class="v"
                title={fmtDate(info.date_rostered_ms)}
                >{rel(info.date_rostered_ms)}</span
              >
            </div>
          {/if}
          <div class="kv">
            <span class="k">DKP</span><span class="v">{info.dkp}</span>
          </div>
          <div class="kv">
            <span class="k">30 Day RA</span><span class="v"
              >{Math.round(info.ra_30)}%</span
            >
          </div>
          <div class="kv">
            <span class="k">Lifetime RA</span><span class="v"
              >{Math.round(info.ra_life)}%</span
            >
          </div>
          {#if info.guild_last_seen_ms}
            <div class="kv">
              <span class="k">Last Seen (/gu)</span><span
                class="v"
                title={fmtDate(info.guild_last_seen_ms)}
                >{rel(info.guild_last_seen_ms)}</span
              >
            </div>
          {/if}

          <div class="sec">Discord Information</div>
          <div class="kv">
            <span class="k">Handle</span><span class="v">{info.handle}</span>
          </div>
          {#if info.display_name}
            <div class="kv">
              <span class="k">Display Name</span><span class="v"
                >{info.display_name}</span
              >
            </div>
          {/if}
          {#if info.discord_last_seen_ms}
            <div class="kv">
              <span class="k">Last Seen (Discord)</span><span
                class="v"
                title={fmtDate(info.discord_last_seen_ms)}
                >{rel(info.discord_last_seen_ms)}</span
              >
            </div>
          {/if}

          {#if info.purchases.length}
            <div class="sec">Recent Purchases</div>
            {#each info.purchases as p}
              <div class="purchase">
                <span class="pname">{p.item}</span>
                <span class="pdkp">{p.dkp} DKP</span>
                <span class="pdate">{fmtDate(p.date_ms)}</span>
              </div>
            {/each}
          {/if}

          <div class="sec">Toon Information</div>
          {#if !info.toons.length}
            <div class="dim">No characters on record.</div>
          {:else}
            <table class="toons">
              <thead>
                <tr>
                  <th>Name (Class)</th>
                  <th class="c">Rostered</th>
                  <th class="c">Garanel</th>
                </tr>
              </thead>
              <tbody>
                {#each info.toons as t (t.name)}
                  <tr>
                    <td
                      ><strong>{t.name}</strong>
                      <span class="tclass"
                        >({t.class ? classAbbr(t.class) : "?"})</span
                      ></td
                    >
                    <td class="c">{t.rostered ? "✅" : "🚫"}</td>
                    <td class="c">{t.garanel ? "✅" : "🚫"}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .members-pane {
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
    gap: 8px;
    padding: 0 12px 8px;
  }
  .acwrap {
    position: relative;
  }
  .inp {
    background: var(--bg-input);
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 4px;
    padding: 5px 8px;
    font-size: 12px;
    width: 300px;
  }
  .inp:focus {
    outline: none;
    border-color: var(--border-hover);
  }
  .sugs {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 50;
    background: var(--bg-panel);
    border: 1px solid var(--border-hover);
    border-radius: 4px;
    margin-top: 2px;
    max-height: 220px;
    overflow: auto;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.5);
  }
  .sug {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    background: none;
    border: none;
    color: var(--text-primary);
    text-align: left;
    padding: 5px 8px;
    font-size: 12px;
    cursor: pointer;
  }
  .sug:hover {
    background: var(--bg-secondary);
    color: var(--accent);
  }
  .sug-label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Tags each row as a member or a toon match (a toon opens its owner's card). */
  .sug-kind {
    flex-shrink: 0;
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
  }
  .go {
    background: var(--bg-panel);
    border: 1px solid var(--accent-dim, var(--border));
    color: var(--accent);
    border-radius: 4px;
    padding: 5px 14px;
    font-size: 12px;
    cursor: pointer;
  }
  .go:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .errline {
    color: var(--error, #e05c5c);
    font-size: 11px;
  }
  .results {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 0 12px 12px;
  }
  .msg {
    padding: 16px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 14px;
    max-width: 520px;
  }
  .card-head {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 4px;
  }
  .avatar {
    width: 48px;
    height: 48px;
    border-radius: 50%;
    border: 1px solid var(--border);
  }
  .dname {
    font-size: 16px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .handle {
    font-size: 11px;
    color: var(--text-muted);
  }
  .sec {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--accent);
    font-weight: 700;
    margin: 12px 0 5px;
    border-bottom: 1px solid var(--border);
    padding-bottom: 3px;
  }
  .kv {
    display: flex;
    gap: 10px;
    font-size: 12px;
    padding: 1px 0;
  }
  .k {
    color: var(--text-muted);
    width: 130px;
    flex-shrink: 0;
  }
  .v {
    color: var(--text-primary);
  }
  .purchase {
    display: flex;
    gap: 10px;
    font-size: 12px;
    padding: 1px 0;
  }
  .pname {
    color: var(--text-primary);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .pdkp {
    color: var(--accent);
    font-variant-numeric: tabular-nums;
  }
  .pdate {
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }
  .dim {
    color: var(--text-muted);
    font-size: 12px;
  }
  .toons {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .toons th {
    text-align: left;
    padding: 4px 8px;
    color: var(--text-muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border);
  }
  .toons td {
    padding: 3px 8px;
    border-bottom: 1px solid var(--border);
    color: var(--text-secondary);
  }
  .toons tr:last-child td {
    border-bottom: none;
  }
  .toons strong {
    color: var(--text-primary);
  }
  .tclass {
    color: var(--text-muted);
  }
  th.c,
  td.c {
    text-align: center;
  }
</style>
