<script>
  import { onMount, onDestroy } from 'svelte'
  import { GetClients } from '../../wailsjs/go/main/App'

  let clients  = []
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
    try { clients = await GetClients() || []; error = '' }
    catch (e) { error = String(e) }
  }

  onMount(async () => { await load(); interval = setInterval(load, 15000) })
  onDestroy(() => clearInterval(interval))
</script>

<div class="clients">
  {#if error}
    <div class="msg error">{error}</div>
  {:else if !clients.length}
    <div class="msg">No clients registered</div>
  {:else}
    <table>
      <thead>
        <tr>
          <th class="c-status">Status</th>
          <th class="c-name">Name</th>
          <th class="c-ver">Version</th>
          <th class="c-seen">Last Seen</th>
        </tr>
      </thead>
      <tbody>
        {#each clients as c}
          <tr class:connected={c.connected}>
            <td class="c-status">
              <span class="dot" class:on={c.connected}></span>
            </td>
            <td class="c-name">{c.name}</td>
            <td class="c-ver mono">{c.version}</td>
            <td class="c-seen">{since(c.last_seen)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .clients { padding:16px; height:100%; overflow:auto; }

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
  .c-seen   { width:110px; }
  .mono     { font-family:var(--font-mono); }

  .dot {
    display:inline-block; width:8px; height:8px; border-radius:50%;
    background:var(--text-muted);
  }
  .dot.on { background:var(--success); box-shadow:0 0 5px var(--success); }
</style>
