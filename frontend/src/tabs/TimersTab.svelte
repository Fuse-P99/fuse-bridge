<script>
  import { onMount, onDestroy } from 'svelte'
  import { GetTimers } from '../../wailsjs/go/main/App'

  let data = null
  let loading = true
  let timer

  async function load() {
    try { data = await GetTimers() } catch { data = null }
    loading = false
  }
  onMount(async () => { await load(); timer = setInterval(load, 60000) })
  onDestroy(() => clearInterval(timer))

  const LABEL = { popped: 'Popped', in_window: 'In Window', upcoming: 'Upcoming' }

  // Group mobs by status in a fixed priority order, preserving board order within.
  $: groups = (data && data.mobs)
    ? ['popped', 'in_window', 'upcoming']
        .map(k => ({ key: k, label: LABEL[k], mobs: data.mobs.filter(m => m.status === k) }))
        .filter(g => g.mobs.length)
    : []
</script>

<div class="timers">
  {#if loading}
    <div class="empty">Loading timers…</div>
  {:else if !data || !data.verified}
    <div class="empty">
      <div class="big">Timers unavailable</div>
      <div class="hint">You could not be verified as a Fuse member.</div>
    </div>
  {:else}
    <div class="board">
      {#if data.porter}
        <div class="porter"><span class="ptag">PORTER</span> {data.porter}</div>
      {/if}

      {#if !groups.length}
        <div class="empty">No timers reported</div>
      {/if}

      {#each groups as grp}
        <div class="group-title {grp.key}">{grp.label} <span class="count">({grp.mobs.length})</span></div>
        {#each grp.mobs as m}
          <div class="mob">
            <div class="mob-head">
              <span class="dot {m.status}"></span>
              <span class="mob-name">{m.name}</span>
            </div>
            {#if m.detail}<div class="mob-detail">{m.detail}</div>{/if}
            {#if m.trackers}<div class="mob-trackers">{m.trackers}</div>{/if}
          </div>
        {/each}
      {/each}
    </div>

    <div class="footer">
      {#if data.summary}<span>{data.summary}</span>{/if}
      {#if data.updated}<span class="upd">{data.updated}</span>{/if}
    </div>
  {/if}
</div>

<style>
  .timers { display:flex; flex-direction:column; height:100%; overflow:hidden; }
  .board { flex:1; overflow-y:auto; padding:10px 14px; }

  .porter {
    background:var(--bg-panel); border:1px solid var(--border); border-radius:6px;
    padding:8px 10px; margin-bottom:12px; font-size:12px; color:var(--text-secondary);
  }
  .ptag {
    color:var(--accent); font-weight:700; font-size:10px; letter-spacing:0.06em;
    margin-right:6px;
  }

  .group-title {
    font-size:11px; font-weight:700; text-transform:uppercase; letter-spacing:0.06em;
    margin:14px 0 6px; color:var(--text-muted);
  }
  .group-title.popped    { color:#ff7a7a; }
  .group-title.in_window { color:#3fb950; }
  .group-title.upcoming  { color:var(--text-muted); }
  .group-title .count { font-weight:400; }

  .mob { padding:5px 0 6px; border-bottom:1px solid var(--border); }
  .mob:last-child { border-bottom:none; }
  .mob-head { display:flex; align-items:center; gap:7px; }
  .mob-name { color:var(--text-primary); font-size:13px; font-weight:600; }
  .dot { width:8px; height:8px; border-radius:50%; flex-shrink:0; }
  .dot.popped    { background:#ff5555; }
  .dot.in_window { background:#3fb950; }
  .dot.upcoming  { background:var(--text-muted); }

  .mob-detail   { color:var(--text-secondary); font-size:12px; margin:1px 0 0 15px; }
  .mob-trackers { color:var(--text-muted); font-size:11px; font-style:italic; margin:1px 0 0 15px; }

  .footer {
    flex-shrink:0; display:flex; justify-content:space-between; gap:10px;
    padding:6px 14px; border-top:1px solid var(--border); background:var(--bg-secondary);
    color:var(--text-muted); font-size:11px;
  }
  .upd { white-space:nowrap; }

  .empty {
    display:flex; flex-direction:column; align-items:center; justify-content:center;
    height:100%; gap:6px; color:var(--text-muted); font-size:13px; text-align:center;
  }
  .empty .big { color:var(--text-secondary); font-size:15px; font-weight:600; }
  .empty .hint { font-size:12px; }
</style>
