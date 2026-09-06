<script>
  // The bot login modal — shared by the Bots sub-tab (Details/Claim) and the
  // batphone AlertBots panel, so the two stay identical by construction.
  import { onMount, onDestroy } from "svelte";
  import { Clipboard } from "@wailsio/runtime";

  export let detail; // GuildBotDetail payload (credentials included)
  export let title = "Bot Login";
  export let onClose;
  // full: the Details view — mirrors the Discord /botinfo single-bot embed
  // section for section (Bot Details, Login Information, Last Park
  // Information, Mob Alerts). Off (default) is the compact login card shown
  // after a claim and from the batphone AlertBots panel.
  export let full = false;

  let showPw = false;
  let copied = "";
  let copyErr = "";
  let now = Date.now();
  let tick;

  onMount(() => (tick = setInterval(() => (now = Date.now()), 1000)));
  onDestroy(() => clearInterval(tick));

  function claimLeft() {
    const s = Math.max(0, Math.floor((detail.claim_expires_ms - now) / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }

  function fmtTime(ms) {
    return new Date(ms).toLocaleString([], {
      dateStyle: "medium",
      timeStyle: "short",
    });
  }
  function ago(ms) {
    const s = Math.max(0, Math.floor((now - ms) / 1000));
    if (s < 3600) return `${Math.max(1, Math.floor(s / 60))}m ago`;
    if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
    return `${Math.floor(s / 86400)}d ago`;
  }

  async function copy(text, tag) {
    copyErr = "";
    try {
      await Clipboard.SetText(text);
      copied = tag;
    } catch {
      try {
        await navigator.clipboard.writeText(text);
        copied = tag;
      } catch {
        copyErr = "Could not access the clipboard.";
      }
    }
  }
</script>

<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="overlay" on:click|self={onClose}>
  <div class="modal">
    <div class="modal-title">{title}</div>
    <div class="d-head">
      <span class="d-name">{detail.name}</span>
      {#if !full}
        <span class="d-meta"
          >{detail.class} {detail.level || "?"} · {detail.park_zone ||
            "unparked"}</span
        >
      {/if}
    </div>
    {#if full}
      <div class="sec">Bot Details</div>
      <div class="d-line">
        {detail.level || "?"} / {detail.race || "?"}
        {detail.class}
      </div>
      <div class="d-line">Note: {detail.note || "None"}</div>
      <div class="sec">Login Information</div>
    {:else if detail.park_note}
      <div class="d-park">{detail.park_note}</div>
    {/if}

    <div class="cred">
      <span class="cred-label">Account</span>
      <span class="cred-val">{detail.eq_username}</span>
      <button class="act" on:click={() => copy(detail.eq_username, "u")}
        >{copied === "u" ? "✓ Copied" : "Copy"}</button
      >
    </div>
    <div class="cred">
      <span class="cred-label">Password</span>
      <span class="cred-val">{showPw ? detail.eq_password : "••••••••"}</span>
      <button class="act" on:click={() => (showPw = !showPw)}
        >{showPw ? "Hide" : "Show"}</button
      >
      <button class="act" on:click={() => copy(detail.eq_password, "p")}
        >{copied === "p" ? "✓ Copied" : "Copy"}</button
      >
    </div>

    {#if full}
      <div class="sec">Last Park Information</div>
      {#if detail.park_zone}
        <div class="d-line">Zone: {detail.park_zone}</div>
        {#if detail.park_time_ms}
          <div class="d-line">
            Time: {fmtTime(detail.park_time_ms)} ({ago(detail.park_time_ms)})
          </div>
        {/if}
        <div class="d-line">Park Note: {detail.park_note || "None"}</div>
      {:else}
        <div class="d-line">Zone: None</div>
      {/if}
      {#if detail.alerts && detail.alerts.length}
        <div class="sec">Mob Alerts</div>
        <div class="d-line">Mobs: {detail.alerts.join(", ")}</div>
      {/if}
    {/if}

    {#if copyErr}
      <div class="d-err">{copyErr}</div>
    {/if}
    {#if detail.claimed_by && detail.claim_expires_ms > now}
      <div class="d-claim">
        Claimed by {detail.claimed_by} — {claimLeft()} remaining
      </div>
    {/if}

    <div class="modal-actions">
      <button class="btn" on:click={onClose}>Close</button>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 120;
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
    width: 440px;
    max-width: 94vw;
    display: flex;
    flex-direction: column;
    gap: 8px;
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.6);
  }
  .modal-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--accent);
  }
  .d-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
  }
  .d-name {
    font-size: 15px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .d-meta {
    font-size: 11px;
    color: var(--text-muted);
  }
  .d-park {
    font-size: 11px;
    color: var(--text-secondary);
  }
  /* Section headers for the full /botinfo-style view. */
  .sec {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    margin-top: 4px;
  }
  .d-line {
    font-size: 12px;
    color: var(--text-primary);
    line-height: 1.4;
  }
  .cred {
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 6px 8px;
  }
  .cred-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    width: 64px;
    flex-shrink: 0;
  }
  .cred-val {
    font-family: var(--font-mono);
    font-size: 13px;
    color: var(--text-primary);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .d-err {
    font-size: 11px;
    color: var(--error, #e05c5c);
  }
  .d-claim {
    font-size: 11px;
    color: var(--error, #e05c5c);
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
  .act:hover {
    border-color: var(--border-hover);
    color: var(--text-primary);
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
  }
  .btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 4px;
    padding: 4px 14px;
    font-size: 12px;
    cursor: pointer;
  }
</style>
