<script>
  // Boat trip recorder — calibration for boats the schedule can't see.
  //
  // A route with no usable dock announcement (the Bloated Belly, TD ↔ Overthere)
  // can only be measured by riding it. This records the observable moments —
  // zone transitions, dock shouts, and a marker phrase the rider types at each
  // dock — with EQ's own log timestamps, and turns the repeats into a loop time.
  //
  // Lives under the Boats section because that's what it measures: the numbers
  // it produces are the constants those boat lines are drawn from.
  import { onMount, onDestroy } from "svelte";
  import { Clipboard } from "@wailsio/runtime";
  import {
    StartBoatTrack,
    StopBoatTrack,
    ClearBoatTrack,
    GetBoatTrack,
  } from "../../bindings/FuseBridge/app.js";

  let bt = { recording: false, marker: "", events: [], truncated: false };
  // Same phrase players use to report this boat (boatManualReports in
  // worldtimers.go), so recording a calibration run and updating the live timer
  // are one action rather than two. Widen it to "boat arrived" to catch both
  // ends — events group by their full text, so the two become separate series.
  let btMarker = "Boat arrived at OT";
  let btCopied = "";
  let btInterval;

  async function btLoad() {
    try {
      bt = (await GetBoatTrack()) || bt;
      if (bt.marker) btMarker = bt.marker;
    } catch {
      /* leave the last snapshot up */
    }
  }
  async function btStart() {
    await StartBoatTrack(btMarker);
    await btLoad();
  }
  async function btStop() {
    await StopBoatTrack();
    await btLoad();
  }
  async function btClear() {
    await ClearBoatTrack();
    btCopied = "";
    await btLoad();
  }

  const btClock = (ms) =>
    ms ? new Date(ms).toLocaleTimeString([], { hour12: false }) : "—";
  // "announce" is the dock shout — the instant every schedule offset in the
  // boat table is measured from, so it's worth telling apart from an arrival.
  const btKind = (k) =>
    k === "zone" ? "ZONE" : k === "announce" ? "SHOUT" : "MARK";

  // Gap from the previous event of the SAME kind and text — one docking to the
  // next docking, one Overthere arrival to the next. Comparing adjacent rows of
  // different kinds would measure legs, not the loop.
  function btGap(events, i) {
    const e = events[i];
    for (let j = i - 1; j >= 0; j--) {
      if (events[j].kind === e.kind && events[j].text === e.text) {
        return e.at_ms - events[j].at_ms;
      }
    }
    return null;
  }
  const btDur = (ms) => {
    const s = Math.round(ms / 1000);
    return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s (${s}s)`;
  };

  // Round-trip verification. Every event that recurred is a lap marker, so the
  // laps between the FIRST and LAST sighting of it measure the loop.
  //
  // The average deliberately comes from (last - first) / laps rather than from
  // averaging adjacent gaps: EQ log timestamps are whole seconds, so a single
  // gap is only ±1s — which, over the dozens of loops a day a boat holds, is
  // minutes of drift. Spanning N laps divides that error by N. That is the
  // entire reason to sit on the boat rather than time one crossing.
  function btSeries(events) {
    const groups = new Map();
    for (const e of events || []) {
      if (!e.at_ms) continue;
      const k = e.kind + " " + e.text;
      if (!groups.has(k)) groups.set(k, { kind: e.kind, text: e.text, t: [] });
      groups.get(k).t.push(e.at_ms);
    }
    const out = [];
    for (const g of groups.values()) {
      if (g.t.length < 2) continue;
      const laps = g.t.length - 1;
      let lo = Infinity;
      let hi = -Infinity;
      for (let i = 1; i < g.t.length; i++) {
        const d = g.t[i] - g.t[i - 1];
        if (d < lo) lo = d;
        if (d > hi) hi = d;
      }
      out.push({
        kind: g.kind,
        text: g.text,
        laps,
        avgS: (g.t[g.t.length - 1] - g.t[0]) / laps / 1000,
        loS: lo / 1000,
        hiS: hi / 1000,
        // Resolution-limited uncertainty on the average, in seconds.
        precS: 1 / laps,
      });
    }
    return out.sort((a, b) => b.laps - a.laps);
  }
  $: btStats = btSeries(bt.events);

  // A lap that disagrees with the others by more than a couple of seconds isn't
  // measurement noise — it's the boat having been reset, which invalidates
  // spanning across it.
  const btSpread = (s) => s.hiS - s.loS;

  // Tab-separated so it pastes straight into a spreadsheet.
  function btText() {
    const rows = ["kind\tdetail\tlog_time\tlog_ms\tread_ms\tgap_s"];
    (bt.events || []).forEach((e, i) => {
      const g = btGap(bt.events, i);
      rows.push(
        [
          e.kind,
          e.text,
          btClock(e.at_ms),
          e.at_ms,
          e.seen_ms,
          g == null ? "" : Math.round(g / 1000),
        ].join("\t"),
      );
    });
    return rows.join("\n");
  }
  async function btCopy() {
    const text = btText();
    try {
      await Clipboard.SetText(text);
      btCopied = `Copied ${(bt.events || []).length} event(s)`;
    } catch {
      try {
        await navigator.clipboard.writeText(text);
        btCopied = `Copied ${(bt.events || []).length} event(s)`;
      } catch {
        btCopied = "Could not reach the clipboard";
      }
    }
    setTimeout(() => (btCopied = ""), 4000);
  }

  onMount(() => {
    btLoad();
    // A docking should show up while you're still standing on the boat looking
    // at it. Cheap — the recorder is local, no server round trip.
    btInterval = setInterval(btLoad, 2000);
  });
  onDestroy(() => clearInterval(btInterval));
</script>

<div class="bt">
  <div class="bt-head">
    <span class="bt-name">Trip Recorder</span>
    <span class="bt-hint"
      >Ride the boat. Zone changes and dock shouts are captured automatically;
      hit a hotkey containing the marker phrase at each dock.</span
    >
  </div>
  <div class="bt-ctl">
    <label class="bt-lbl" for="bt-marker">Marker</label>
    <input
      id="bt-marker"
      class="bt-in"
      bind:value={btMarker}
      disabled={bt.recording}
      placeholder="Boat arrived at OT"
    />
    {#if bt.recording}
      <button class="bt-btn bt-stop" on:click={btStop}>Stop</button>
    {:else}
      <button class="bt-btn bt-go" on:click={btStart}>Record</button>
    {/if}
    <button class="bt-btn" on:click={btCopy} disabled={!(bt.events || []).length}
      >Copy</button
    >
    <button
      class="bt-btn"
      on:click={btClear}
      disabled={!(bt.events || []).length}>Clear</button
    >
    {#if bt.recording}<span class="bt-live">● recording</span>{/if}
    {#if btCopied}<span class="bt-copied">{btCopied}</span>{/if}
  </div>
  {#if bt.truncated}
    <div class="bt-warn">
      Oldest events dropped — the recording hit its retention cap.
    </div>
  {/if}
  {#if btStats.length}
    <div class="bt-stats">
      {#each btStats as s}
        <div class="bt-stat">
          <span class="bt-kind bt-{s.kind}">{btKind(s.kind)}</span>
          <span class="bt-stat-t">{s.text}</span>
          <span class="bt-stat-n">{s.laps} lap{s.laps === 1 ? "" : "s"}</span>
          <span class="bt-stat-avg"
            >{s.avgS.toFixed(2)}s ±{s.precS.toFixed(2)}</span
          >
          <span
            class="bt-stat-range"
            class:bt-stat-bad={btSpread(s) > 2}
            title={btSpread(s) > 2
              ? "Laps disagree by more than 2s — the boat was probably reset partway through, so the average across this span is not trustworthy."
              : "Shortest and longest single lap observed."}
            >{s.loS.toFixed(0)}–{s.hiS.toFixed(0)}s</span
          >
        </div>
      {/each}
    </div>
  {/if}
  <div class="bt-log">
    {#if !(bt.events || []).length}
      <div class="bt-empty">
        {bt.recording ? "Waiting for a zone change or marker…" : "Not recording"}
      </div>
    {:else}
      {#each [...bt.events].reverse() as e, ri}
        {@const i = bt.events.length - 1 - ri}
        {@const gap = btGap(bt.events, i)}
        <div class="bt-row">
          <span class="bt-kind bt-{e.kind}">{btKind(e.kind)}</span>
          <span class="bt-time">{btClock(e.at_ms)}</span>
          <span class="bt-text">{e.text}</span>
          <span class="bt-gap">{gap == null ? "" : `+${btDur(gap)}`}</span>
        </div>
      {/each}
    {/if}
  </div>
</div>

<style>
  .bt {
    margin-top: 10px;
    padding-top: 8px;
    border-top: 1px dashed var(--border);
  }
  .bt-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    flex-wrap: wrap;
  }
  .bt-name {
    color: var(--text-secondary);
    font-size: 10.5px;
    font-weight: 700;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }
  .bt-hint {
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .bt-ctl {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 7px 0;
    flex-wrap: wrap;
  }
  .bt-lbl {
    color: var(--text-secondary);
    font-size: 10px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .bt-in {
    background: var(--bg-input, rgba(0, 0, 0, 0.25));
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 11px;
    padding: 3px 7px;
    min-width: 150px;
    flex: 1 1 150px;
  }
  .bt-in:disabled {
    opacity: 0.55;
  }
  .bt-btn {
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 10.5px;
    padding: 3px 9px;
  }
  .bt-btn:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.06);
    color: var(--text-primary);
  }
  .bt-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .bt-go {
    border-color: #2f7d4f;
    color: #6ee7a0;
  }
  .bt-stop {
    border-color: #8a3b3b;
    color: #ff8a8a;
  }
  /* Steady, not blinking: this sits on screen for a whole boat ride. */
  .bt-live {
    color: #6ee7a0;
    font-size: 10.5px;
    font-weight: 600;
  }
  .bt-copied,
  .bt-empty {
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .bt-warn {
    color: #e3a008;
    font-size: 10.5px;
    margin-bottom: 5px;
  }
  /* Round-trip summary — the answer, above the raw events it came from. */
  .bt-stats {
    display: flex;
    flex-direction: column;
    gap: 3px;
    margin-bottom: 7px;
    padding: 5px 7px;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: rgba(110, 231, 160, 0.05);
  }
  .bt-stat,
  .bt-row {
    display: flex;
    align-items: baseline;
    gap: 7px;
    font-size: 10.5px;
  }
  .bt-row {
    padding: 2px 0;
  }
  .bt-kind {
    flex: 0 0 auto;
    border-radius: 3px;
    font-size: 9.5px;
    font-weight: 700;
    letter-spacing: 0.04em;
    padding: 0 5px;
  }
  .bt-zone {
    background: #21227f;
    color: #cfd3ff;
  }
  .bt-mark {
    background: #7a5a12;
    color: #ffe6a8;
  }
  .bt-announce {
    background: #13502f;
    color: #b6f2d0;
  }
  .bt-stat-t,
  .bt-text {
    flex: 1 1 auto;
    color: var(--text-primary);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .bt-stat-n {
    flex: 0 0 auto;
    color: var(--text-muted);
  }
  .bt-stat-avg {
    flex: 0 0 auto;
    color: #6ee7a0;
    font-family: var(--font-mono, monospace);
    font-weight: 700;
  }
  .bt-stat-range,
  .bt-time {
    flex: 0 0 auto;
    color: var(--text-secondary);
    font-family: var(--font-mono, monospace);
  }
  .bt-stat-bad {
    color: #e3a008;
    font-weight: 700;
  }
  .bt-log {
    max-height: 170px;
    overflow-y: auto;
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 5px 7px;
  }
  /* The measurement itself — the number this whole panel exists to produce. */
  .bt-gap {
    flex: 0 0 auto;
    color: #6ee7a0;
    font-family: var(--font-mono, monospace);
    font-weight: 600;
  }
</style>
