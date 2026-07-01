<script>
  import { onMount, onDestroy } from 'svelte'
  import { GetClients, GetClientActivity } from '../../wailsjs/go/main/App'

  let clients  = []
  let activity = []
  let error    = ''
  let interval

  function since(dateStr) {
    const s = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000)
    if (s < 60)   return 'just now'
    const m = Math.floor(s / 60)
    if (m < 60)   return `${m} min ago`
    const h = Math.floor(m / 60)
    if (h < 24)   return `${h} hr ago`
    return `${Math.floor(h / 24)} days ago`
  }

  async function load() {
    try {
      clients = await GetClients() || []
      activity = await GetClientActivity() || []
      error = ''
    } catch (e) { error = String(e) }
  }

  onMount(async () => { await load(); interval = setInterval(load, 10000) })
  onDestroy(() => clearInterval(interval))
</script>

<div class="clients">
  {#if error}
    <div class="msg error">{error}</div>
  {:else}
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
          </tr>
        </thead>
        <tbody>
          {#each clients as c}
            <tr class:connected={c.status === 'active' || c.status === 'connected'}>
              <td class="c-status">
                <span
                  class="dot {c.status}"
                  title={c.status === 'active' ? 'Relaying log data' : c.status === 'connected' ? 'Connected (no recent log data)' : 'Offline'}
                ></span>
              </td>
              <td class="c-name">{c.name}</td>
              <td class="c-toon">
                {c.toon || '—'}{#if c.guild}<span class="guild">&lt;{c.guild}&gt;</span>{/if}
              </td>
              <td class="c-zone">{c.last_zone || '—'}</td>
              <td class="c-ver mono">{c.version}</td>
              <td class="c-seen">{since(c.last_seen)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}

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
  {/if}
</div>

<style>
  .clients { padding:16px; height:100%; overflow:auto; display:flex; flex-direction:column; }

  .msg {
    color:var(--text-muted); font-size:12px;
    text-align:center; margin-top:60px;
  }
  .msg.error { color:var(--error); }

  table { width:100%; border-collapse:collapse; font-size:12px; }

  thead th {
    background:var(--bg-panel); border-bottom:1px solid var(--border);
    color:var(--text-secondary); font-size:11px; font-weight:600;
    letter-spacing:0.04em; padding:8px 12px; text-align:left; text-transform:uppercase;
  }

  tbody tr { border-bottom:1px solid var(--border); }
  tbody tr:hover { background:rgba(255,255,255,0.03); }

  tbody td { color:var(--text-secondary); padding:8px 12px; }
  tbody tr.connected td { color:var(--text-primary); }

  .c-status { width:54px; text-align:center; }
  .c-ver    { font-family:var(--font-mono); width:80px; }
  .c-seen   { width:100px; }
  .c-zone   { color:var(--text-secondary); }
  .mono     { font-family:var(--font-mono); }
  .guild    { color:var(--text-muted); margin-left:5px; font-size:11px; }

  .dot {
    display:inline-block; width:8px; height:8px; border-radius:50%;
    background:var(--text-muted);
  }
  .dot.active    { background:var(--success); box-shadow:0 0 5px var(--success); }
  .dot.connected { background:#e3a008; box-shadow:0 0 5px #e3a008; }
  .dot.offline   { background:var(--text-muted); }

  .section-label {
    font-size:10px; font-weight:600; letter-spacing:0.08em; text-transform:uppercase;
    color:var(--text-muted); margin:18px 0 5px;
  }
  .log {
    flex:1; min-height:120px; overflow-y:auto;
    background:var(--bg-panel); border:1px solid var(--border); border-radius:4px;
    padding:7px 10px; font-family:var(--font-mono); font-size:11px;
    color:var(--text-secondary); line-height:1.55;
  }
  .log-line { white-space:pre-wrap; word-break:break-word; }
  .log-empty { color:var(--text-muted); font-style:italic; }
</style>
