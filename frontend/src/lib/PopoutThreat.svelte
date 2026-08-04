<script>
  // Threat Meter special overlay (officer-only). Three stacked sections:
  //   1. Damage parse — target, engaged clock, live DPS. Opens and closes
  //      purely on damage matching (any melee fight, raid or not).
  //   2. Radial threat gauge — the viewer's estimated hate vs the tank's on
  //      the same mob. Raid identification controls this section's
  //      visibility, never the parse. A tank viewing it compares against
  //      their closest rival instead and wants the needle pegged deep red.
  //   3. Hate-reducer counters — Concussion / Jolt / Evade success + fails.
  //
  // All math lives in threat.go (client) and threatMeter.go (server); this
  // file only decides how the numbers look. Poll + nudge like Randoms: the
  // fight clock and idle collapse ride the clock, not log lines.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    GetThreatMeter,
    GetRaidDPS,
  } from "../../bindings/FuseBridge/app.js";

  export let hasContent = false; // drives the "Hide When 0" title-bar mode

  let data = {
    officer: false,
    engaged: false,
    mob: "",
    elapsed_s: 0,
    dps: 0,
    own_threat: 0,
    own_damage: 0,
    spell_hate: 0,
    tools: {},
    raid_active: false,
    have_ref: false,
    is_tank: false,
    ref_name: "",
    ref_threat: 0,
    tank_source: "",
    tank_name: "",
    tank_procs_pm: 0,
    tank_procs_src: "",
    ratio: 0,
    zones: { green_max: 0.7, yellow_max: 0.9, cap: 1.5 },
    others: [],
  };
  let off, pollTimer;
  let polling = false;

  $: hasContent = data.officer && data.engaged;

  // The fight's top 5, from the same shared damage board the Raid DPS panel
  // reads. It works outside a raid too — the server falls back to the mob we
  // have personally damaged — so this section is useful in a group, which is
  // where this overlay spends most of its life. Empty mob = "whatever I'm
  // fighting"; the call is cached client-side, so the 1s poll here costs one
  // round trip every three seconds.
  let top = [];

  // End-of-fight review window. The numbers are at their most interesting the
  // moment the fight stops, which is exactly when they'd otherwise vanish —
  // the engaged flag drops on its idle timer and takes the panel with it. So
  // the last engaged state is held, frozen, for half a minute before the
  // overlay lets go of it. Frozen rather than left to poll: once disengaged the
  // server has nothing to report, and a live read would empty the panel while
  // it's still on screen.
  const REVIEW_MS = 30000;
  let endedAt = 0; // ms when the fight stopped; 0 while engaged

  $: reviewing = endedAt > 0;

  async function poll() {
    if (polling) return;
    polling = true;
    try {
      const d = await GetThreatMeter();
      if (d) {
        if (d.engaged) {
          // Engaged — including a fresh pull that interrupts a review.
          data = d;
          endedAt = 0;
        } else if (data.engaged) {
          if (!endedAt) endedAt = Date.now();
          if (Date.now() - endedAt >= REVIEW_MS) {
            data = d;
            endedAt = 0;
          }
        } else {
          data = d;
        }
      }
    } catch {
      /* keep the last good state */
    }
    // The top 5 freezes with everything else, so the review shows one
    // consistent picture of the fight rather than a live board going quiet.
    if (!endedAt) {
      try {
        const r = await GetRaidDPS("");
        top = (r && r.top) || [];
      } catch {
        /* keep the last list */
      }
    }
    polling = false;
  }

  onMount(async () => {
    await poll();
    off = Events.On("threat-changed", poll);
    pollTimer = setInterval(poll, 1000);
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (off) off();
  });

  function clock(s) {
    const m = Math.floor(s / 60);
    const r = s % 60;
    return `${m}:${String(r).padStart(2, "0")}`;
  }
  function fmtDps(v) {
    return v >= 100 ? Math.round(v).toString() : v.toFixed(1);
  }
  function fmtDelay(v) {
    return v > 0 ? v.toFixed(1) : "—";
  }

  // ── gauge geometry: a semicircle, ratio 0 at the left end, cap at the
  //    right, needle rotating clockwise ─────────────────────────────────────
  const CX = 80,
    CY = 80,
    R = 60;
  function polar(r, deg) {
    const a = (Math.PI * (180 - deg)) / 180;
    return [CX + r * Math.cos(a), CY - r * Math.sin(a)];
  }
  function arc(a0, a1) {
    const [x0, y0] = polar(R, a0);
    const [x1, y1] = polar(R, a1);
    return `M ${x0.toFixed(1)} ${y0.toFixed(1)} A ${R} ${R} 0 ${a1 - a0 > 180 ? 1 : 0} 1 ${x1.toFixed(1)} ${y1.toFixed(1)}`;
  }
  $: zones = data.zones || { green_max: 0.7, yellow_max: 0.9, cap: 1.5 };
  $: cap = zones.cap > 0 ? zones.cap : 1.5;
  $: greenDeg = Math.min(180, (zones.green_max / cap) * 180);
  $: yellowDeg = Math.min(180, (zones.yellow_max / cap) * 180);
  $: needleDeg = Math.max(0, Math.min(180, (data.ratio / cap) * 180));
  $: pct = Math.round((data.ratio || 0) * 100);

  $: tools = data.tools || {};
  $: toolChips = [
    { name: "Concussion", ok: tools.conc_ok || 0, fail: tools.conc_fail || 0 },
    { name: "Jolt", ok: tools.jolt_ok || 0, fail: tools.jolt_fail || 0 },
    { name: "Evade", ok: tools.evade_ok || 0, fail: tools.evade_fail || 0 },
  ].filter((t) => t.ok + t.fail > 0);
</script>

<div class="th">
  {#if !data.officer}
    <div class="th-none">Not linked.</div>
  {:else if data.engaged}
    <div class="th-head">
      <span class="th-mob" title={data.mob}>{data.mob}</span>
      {#if reviewing}<span class="th-final" title="Fight over — final numbers"
          >FINAL</span
        >{/if}
      <span class="th-clock">{clock(data.elapsed_s)}</span>
    </div>

    <!-- A pure debuffer generates hate without ever dealing damage; showing
         them a permanent 0.0 DPS is noise, so the block only appears once
         they've actually done some. -->
    {#if data.own_damage > 0}
      <div class="th-dps">
        <span class="th-dps-num">{fmtDps(data.dps)}</span>
        <span class="th-dps-lbl">DPS</span>
      </div>
    {/if}

    <!-- Everyone on the mob, not just us. Raid or group; anyone the guild
         can't class shows without one rather than not at all. -->
    {#if top.length}
      <div class="th-top">
        {#each top as p, i (p.name)}
          <div class="th-top-row" class:me={p.name === data.own_name}>
            <span class="th-top-rank">{i + 1}</span>
            <span class="th-top-name" class:dead={p.dead} title={p.name}
              >{p.name}</span
            >
            <!-- SDPS, matching the order the rows are in: the list is ranked
                 on damage over the whole fight, so showing anything else lets
                 a higher row display a lower number. -->
            <span class="th-top-dps" title="Damage over the whole fight"
              >{fmtDps(p.sdps)}</span
            >
          </div>
        {/each}
      </div>
    {/if}

    <div class="th-threat">
      Threat: <span class="th-threat-num"
        >{(data.own_threat || 0).toLocaleString()}</span
      >
    </div>
    <!-- Debug block while the swing model is being calibrated: per-hand
         threat per swing, backed-out weapon DMG, and estimated base delay
         (measured cadence with haste divided out). Weapons say nothing about
         a caster's hate, so this is hidden for anyone who hasn't swung. -->
    {#if data.own_damage > 0 || !data.spell_hate}
      <div class="th-hands">
      <div class="th-hand">
        <div class="th-hand-t">Primary</div>
        <div class="th-hand-r">TPS <b>{data.tps_main || 0}</b></div>
        <div class="th-hand-r">Est. DMG <b>{data.est_dmg_main || 0}</b></div>
        <div class="th-hand-r">Est. Delay <b>{fmtDelay(data.est_delay_main)}</b></div>
      </div>
        {#if data.dual}
          <div class="th-hand">
            <div class="th-hand-t">Offhand</div>
            <div class="th-hand-r">TPS <b>{data.tps_off || 0}</b></div>
            <div class="th-hand-r">Est. DMG <b>{data.est_dmg_off || 0}</b></div>
            <div class="th-hand-r">
              Est. Delay <b>{fmtDelay(data.est_delay_off)}</b>
            </div>
          </div>
        {/if}
      </div>
      <div class="th-haste">
        Haste: {data.haste_pct || 0}%{data.swing_src
          ? " · " + data.swing_src
          : ""}
      </div>
    {/if}
    {#if data.spell_hate > 0}
      <div class="th-haste">
        Spell hate: {data.spell_hate.toLocaleString()}
      </div>
    {/if}

    {#if data.raid_active}
      <div class="th-gauge">
        <svg viewBox="0 0 160 92" class="th-svg">
          {#if data.have_ref}
            <path d={arc(0, greenDeg)} class="th-arc green" />
            {#if yellowDeg > greenDeg}
              <path d={arc(greenDeg, yellowDeg)} class="th-arc yellow" />
            {/if}
            {#if yellowDeg < 180}
              <path d={arc(yellowDeg, 180)} class="th-arc red" />
            {/if}
            <g class="th-needle" style="transform: rotate({needleDeg}deg)">
              <line x1={CX} y1={CY} x2={CX - R + 14} y2={CY} />
            </g>
            <circle cx={CX} cy={CY} r="4" class="th-hub" />
          {:else}
            <path d={arc(0, 180)} class="th-arc idle" />
          {/if}
        </svg>
        {#if data.have_ref}
          <div class="th-pct" class:hot={data.ratio >= zones.yellow_max}>
            {pct}%
          </div>
          <div class="th-ref" title={data.ref_name}>
            {#if data.is_tank}
              vs closest rival: {data.ref_name}
            {:else}
              vs {data.ref_name}{data.tank_source === "maintank" ? " (MT)" : ""}
            {/if}
          </div>
        {:else if data.tank_name}
          <div class="th-ref muted">
            {data.tank_name} (MT) isn't running FuseBridge — no threat to
            compare against
          </div>
        {:else}
          <div class="th-ref muted">
            No tank data — tank isn't running FuseBridge
          </div>
        {/if}
        {#if data.tank_procs_src && data.tank_procs_src !== "bridge"}
          <div class="th-ref muted">
            tank procs {data.tank_procs_pm.toFixed(1)}/min ({data.tank_procs_src ===
            "macro"
              ? "counted from their PROC macro"
              : "assumed — nobody can see them"})
          </div>
        {/if}
        {#if data.others && data.others.length}
          <div class="th-others">
            {#each data.others as o (o.name)}
              <div class="th-other">
                <span class="th-oname">{o.name}</span>
                <span class="th-oval">{o.threat}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    {#if toolChips.length}
      <div class="th-tools">
        {#each toolChips as t (t.name)}
          <span class="th-chip">
            {t.name}
            <span class="th-ok">{t.ok}✓</span>
            {#if t.fail}<span class="th-bad">{t.fail}✗</span>{/if}
          </span>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .th {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 6px 8px;
    overflow-y: auto;
    /* Readable over the game even on a transparent background. */
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
  .th-none {
    font-size: 12px;
    color: var(--text-muted);
    padding: 4px 0;
  }
  .th-head {
    display: flex;
    align-items: baseline;
    gap: 6px;
    min-width: 0;
  }
  .th-mob {
    min-width: 0;
    flex: 1 1 auto;
    font-size: 13px;
    font-weight: 700;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .th-clock {
    flex-shrink: 0;
    font-size: 12px;
    color: var(--text-secondary);
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }
  .th-final {
    font-size: 8px;
    font-weight: 700;
    letter-spacing: 0.08em;
    color: #d4af37;
    border: 1px solid rgba(212, 175, 55, 0.5);
    border-radius: 3px;
    padding: 0 3px;
    flex: none;
  }
  .th-top {
    display: flex;
    flex-direction: column;
    gap: 1px;
    margin: 3px 0 1px;
  }
  .th-top-row {
    display: flex;
    align-items: baseline;
    gap: 5px;
    font-size: 11px;
    line-height: 1.35;
    min-width: 0;
  }
  .th-top-row.me .th-top-name,
  .th-top-row.me .th-top-dps {
    color: var(--accent, #d4af37);
    font-weight: 700;
  }
  .th-top-rank {
    color: #7f7f7f;
    font-size: 9px;
    width: 0.9em;
    text-align: right;
    flex: none;
  }
  .th-top-name {
    color: #d8d8d8;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .th-top-name.dead {
    color: #7a7a7a;
    text-decoration: line-through;
  }
  .th-top-dps {
    margin-left: auto;
    color: #e8e8e8;
    font-variant-numeric: tabular-nums;
    flex: none;
  }
  .th-dps {
    display: flex;
    align-items: baseline;
    gap: 5px;
  }
  .th-dps-num {
    font-size: 24px;
    font-weight: 700;
    color: var(--accent);
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    line-height: 1.1;
  }
  .th-dps-lbl {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.08em;
    color: var(--text-muted);
  }
  .th-threat {
    font-size: 12.5px;
    color: var(--text-secondary);
  }
  .th-threat-num {
    font-weight: 700;
    color: var(--text-primary);
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }
  .th-hands {
    display: flex;
    gap: 6px;
  }
  .th-hand {
    flex: 1 1 0;
    min-width: 0;
    padding: 3px 6px;
    border-radius: 6px;
    background: rgba(255, 255, 255, 0.05);
    font-size: 11px;
    color: var(--text-muted);
  }
  .th-hand-t {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-secondary);
    margin-bottom: 1px;
  }
  .th-hand-r {
    display: flex;
    justify-content: space-between;
    gap: 4px;
    white-space: nowrap;
  }
  .th-hand-r b {
    font-weight: 600;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    color: var(--text-primary);
  }
  .th-haste {
    font-size: 11px;
    color: var(--text-muted);
  }

  /* ── gauge ─────────────────────────────────────────────────────────── */
  .th-gauge {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 1px;
  }
  .th-svg {
    width: 150px;
    max-width: 100%;
    display: block;
  }
  .th-arc {
    fill: none;
    stroke-width: 12;
    stroke-linecap: butt;
  }
  .th-arc.green {
    stroke: #66bb6a;
  }
  .th-arc.yellow {
    stroke: #ffca28;
  }
  .th-arc.red {
    stroke: #ef5350;
  }
  .th-arc.idle {
    stroke: rgba(255, 255, 255, 0.18);
  }
  .th-needle {
    transform-origin: 80px 80px;
    transition: transform 0.6s cubic-bezier(0.4, 0, 0.2, 1);
  }
  .th-needle line {
    stroke: var(--text-primary, #fff);
    stroke-width: 3;
    stroke-linecap: round;
  }
  .th-hub {
    fill: var(--text-primary, #fff);
  }
  .th-pct {
    margin-top: -14px;
    font-size: 15px;
    font-weight: 700;
    color: var(--text-primary);
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }
  .th-pct.hot {
    color: #ef5350;
  }
  .th-ref {
    max-width: 100%;
    font-size: 11px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .th-ref.muted {
    color: var(--text-muted);
    white-space: normal;
    text-align: center;
  }
  .th-others {
    align-self: stretch;
    display: flex;
    flex-direction: column;
    margin-top: 2px;
  }
  .th-other {
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: 11px;
    color: var(--text-muted);
    min-width: 0;
  }
  .th-oname {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .th-oval {
    margin-left: auto;
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }

  /* ── reducer counters ──────────────────────────────────────────────── */
  .th-tools {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }
  .th-chip {
    display: inline-flex;
    align-items: baseline;
    gap: 4px;
    padding: 1px 6px;
    border-radius: 8px;
    background: rgba(255, 255, 255, 0.07);
    font-size: 11px;
    color: var(--text-secondary);
  }
  .th-ok {
    color: #66bb6a;
    font-weight: 600;
  }
  .th-bad {
    color: #ef5350;
    font-weight: 600;
  }
</style>
