<script>
  import { onMount, onDestroy } from 'svelte'
  import { IsLinked, StartLinking, PollLinking, Unlink, IsAdminMode } from '../../wailsjs/go/main/App'
  import { ClipboardSetText } from '../../wailsjs/runtime/runtime'
  import { linked } from './linkState.js'

  let admin   = false
  let code    = ''
  let phase   = 'idle' // idle | waiting | error
  let errMsg  = ''
  let copied  = false
  let pollTimer

  onMount(async () => {
    linked.set(await IsLinked())
    admin = await IsAdminMode()
  })
  onDestroy(() => clearInterval(pollTimer))

  async function start() {
    errMsg = ''
    copied = false
    try {
      code = await StartLinking()
      phase = 'waiting'
      clearInterval(pollTimer)
      pollTimer = setInterval(poll, 3000)
    } catch (e) {
      phase = 'error'
      errMsg = String(e)
    }
  }

  async function poll() {
    try {
      if (await PollLinking(code)) {
        clearInterval(pollTimer)
        linked.set(true)
        phase  = 'idle'
        code   = ''
      }
    } catch { /* transient — keep polling */ }
  }

  async function unlink() {
    clearInterval(pollTimer)
    try { await Unlink() } catch { /* clear locally regardless */ }
    linked.set(false)
    phase  = 'idle'
    code   = ''
  }

  async function copyCode() {
    try {
      await ClipboardSetText(code)
      copied = true
      setTimeout(() => { copied = false }, 1500)
    } catch { /* ignore clipboard failure */ }
  }
</script>

<div class="panel account">
  {#if $linked}
    <div class="linked-row">
      <span class="dot green"></span>
      <span class="linked-text">Account linked</span>
      {#if admin}
        <button class="btn danger" on:click={unlink} title="Revoke this client's token and re-run linking">
          Unlink / Reset
        </button>
      {/if}
    </div>
  {:else if phase === 'waiting'}
    <div class="section-label">Link your Fuse account</div>
    <div class="steps">
      <div>1. In Discord, run <span class="mono">/linkclient</span></div>
      <div>2. Paste this code into the <span class="mono">code</span> field:</div>
      <button class="cmd" on:click={copyCode} title="Click to copy">
        <span class="code">{code}</span>
        <span class="copy-hint">{copied ? 'Copied!' : 'Click to copy'}</span>
      </button>
      <div class="waiting"><span class="spinner"></span> Waiting for verification…</div>
    </div>
    <button class="btn" on:click={start}>Get a new code</button>
  {:else}
    <div class="section-label">Link your Fuse account</div>
    <p class="blurb">
      Verify you're a Fuse member to relay under your own identity. You'll run a
      quick Discord command — no shared password.
    </p>
    {#if phase === 'error'}<p class="err">{errMsg}</p>{/if}
    <button class="btn primary" on:click={start}>Link account</button>
  {/if}
</div>

<style>
  .account { background:var(--bg-panel); border:1px solid var(--border); border-radius:6px; padding:10px 14px; }

  .linked-row { display:flex; align-items:center; gap:8px; font-size:12px; }
  .linked-text { color:var(--success); font-weight:600; }
  .dot { width:8px; height:8px; border-radius:50%; }
  .dot.green { background:var(--success); box-shadow:0 0 4px var(--success); }

  .section-label {
    font-size:10px; font-weight:600; letter-spacing:0.08em; text-transform:uppercase;
    color:var(--text-muted); margin-bottom:6px;
  }
  .blurb { color:var(--text-secondary); font-size:12px; margin:0 0 10px; line-height:1.5; }
  .err   { color:var(--error); font-size:12px; margin:0 0 8px; }

  .steps { font-size:12px; color:var(--text-secondary); display:flex; flex-direction:column; gap:6px; margin-bottom:10px; }
  .mono { font-family:var(--font-mono); color:var(--text-primary); }
  .cmd {
    display:flex; align-items:center; justify-content:space-between; gap:10px;
    font-family:var(--font-mono); background:var(--bg-input); border:1px solid var(--border);
    border-radius:4px; padding:7px 10px; color:var(--text-primary); cursor:pointer;
    width:100%; text-align:left; transition:border-color 0.15s;
  }
  .cmd:hover { border-color:var(--accent); }
  .code { color:var(--accent); font-weight:700; letter-spacing:0.12em; font-size:14px; }
  .copy-hint { font-size:10px; color:var(--text-muted); white-space:nowrap; }
  .waiting { display:flex; align-items:center; gap:7px; color:var(--text-muted); font-style:italic; }

  .spinner {
    width:11px; height:11px; border:2px solid var(--border); border-top-color:var(--accent);
    border-radius:50%; display:inline-block; animation:spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform:rotate(360deg); } }

  .btn {
    background:var(--bg-panel); border:1px solid var(--border); border-radius:4px;
    color:var(--text-primary); cursor:pointer; font-size:11px; padding:4px 12px;
    transition:border-color 0.15s, color 0.15s;
  }
  .btn:hover    { border-color:var(--accent); color:var(--accent); }
  .btn.primary  { border-color:var(--accent); color:var(--accent); }
  .btn.danger:hover { border-color:var(--error); color:var(--error); }
</style>
