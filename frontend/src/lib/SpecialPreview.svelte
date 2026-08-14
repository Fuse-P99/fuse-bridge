<script>
  // Static sample of what a Special Overlay shows, slid open under its card in
  // Manage Overlays. Deliberately a hand-built illustration rather than the
  // live component: the real overlays poll live raid/fight/voice state and
  // would render empty exactly when someone is browsing here (out of a raid,
  // out of a fight), which is when the preview matters most. Kept visually in
  // step with each overlay's own CSS vocabulary — colors, bar anatomy, row
  // shapes — so what slides down is an honest miniature.
  export let kind;
</script>

<div class="pv">
  {#if kind === "map"}
    <div class="pv-map">
      <div class="pv-dot" title="You"></div>
      <div class="pv-flag">⚑</div>
      <span class="pv-maplbl">Lake of Ill Omen</span>
    </div>
  {:else if kind === "raidassign"}
    <div class="pv-row">
      <span class="pv-dim">MT</span><span class="pv-hi">Grommet</span>
      <span class="pv-proc">⚡3</span>
    </div>
    <div class="pv-row">
      <span class="pv-dim">Ramp</span><span class="pv-hi">Kregor</span>
    </div>
    <div class="pv-bar">
      <div class="pv-fill teal" style="width:62%"></div>
      <span class="pv-barlbl">DEFENSIVE</span><span class="pv-bartime">1:52</span>
    </div>
    <div class="pv-row pv-small">
      <span class="pv-dim">Tanks:</span> Grommet → Kregor → Dainen
    </div>
  {:else if kind === "raiddebuffs"}
    <div class="pv-row">
      <span class="pv-hi">Lord Nagafen</span>
      <span class="pv-proc">Sieve ×3</span>
    </div>
    <div class="pv-bar">
      <div class="pv-fill violet" style="width:45%"></div>
      <span class="pv-barlbl">Malosini</span><span class="pv-bartime">3:12</span>
    </div>
    <div class="pv-bar">
      <div class="pv-fill teal" style="width:78%"></div>
      <span class="pv-barlbl">Tashania</span><span class="pv-bartime">5:40</span>
    </div>
  {:else if kind === "raidclerics"}
    <div class="pv-row pv-small">
      <span class="pv-dim">Fluffers:</span> Healyou, Patchup
    </div>
    <div class="pv-row">
      <span class="pv-num">111</span><span class="pv-hi">Alba</span>
      <span class="pv-delta">+3.2s</span>
      <span class="pv-dim">→ Grommet</span>
    </div>
    <div class="pv-bar">
      <div class="pv-fill gold" style="width:70%"></div>
      <span class="pv-barlbl">222 Bly — casting</span><span class="pv-bartime"
        >7s</span
      >
    </div>
    <div class="pv-row">
      <span class="pv-num">333</span><span class="pv-hi">Cural</span>
      <span class="pv-delta">+3.5s</span>
      <span class="pv-dim">→ Grommet</span>
    </div>
  {:else if kind === "othertimers"}
    <div class="pv-bar">
      <div class="pv-fill gold" style="width:55%"></div>
      <span class="pv-barlbl">Ocean 2 - Wave 4</span><span class="pv-bartime"
        >1:23</span
      >
    </div>
    <div class="pv-bar">
      <div class="pv-fill gold" style="width:85%"></div>
      <span class="pv-barlbl">Narandi</span><span class="pv-bartime">25:10</span>
    </div>
    <div class="pv-bar">
      <div class="pv-fill teal" style="width:30%"></div>
      <span class="pv-barlbl">Ancient Breath Cooldown</span><span
        class="pv-bartime">0:42</span
      >
    </div>
  {:else if kind === "voicespeakers"}
    <div class="pv-row pv-small">
      <span>🔊</span><span class="pv-dim">VOICE</span>
      <span class="pv-dim">(12)</span>
    </div>
    <div class="pv-row">
      <span class="pv-av">D</span><span class="pv-hi">Deuce</span>
    </div>
    <div class="pv-row">
      <span class="pv-av">K</span><span class="pv-hi">Kregor</span>
    </div>
  {:else if kind === "randoms"}
    <div class="pv-row pv-small">
      <span class="pv-dim">/random 0–333 — Singing Steel Breastplate</span>
    </div>
    <div class="pv-row">
      <span class="pv-num">287</span><span class="pv-hi">Bly</span>
    </div>
    <div class="pv-row">
      <span class="pv-num">102</span><span class="pv-hi">Cural</span>
    </div>
  {:else if kind === "threat"}
    <div class="pv-row">
      <span class="pv-big">142.7</span><span class="pv-dim">DPS</span>
      <span class="pv-dim" style="margin-left:auto">2:41</span>
    </div>
    <div class="pv-stack">
      <div style="width:52%;background:#3987e5"></div>
      <div style="width:23%;background:#d55181"></div>
      <div style="width:15%;background:#e66767"></div>
      <div style="width:10%;background:#98978f"></div>
    </div>
    <div class="pv-row pv-small">
      <span class="pv-dim">Slash 52% · Kick 23% · Spell 15% · DS 10%</span>
    </div>
    <div class="pv-row pv-small">
      <span class="pv-dim">Threat vs Grommet:</span>
      <span class="pv-ok">64%</span>
    </div>
  {:else if kind === "raiddps"}
    <div class="pv-row">
      <span class="pv-dim">Raid DPS</span><span class="pv-big">4,821</span>
    </div>
    <div class="pv-row">
      <span class="pv-num">1</span><span class="pv-hi">Vex + Pet</span>
      <span class="pv-bartime" style="margin-left:auto">612</span>
    </div>
    <div class="pv-row">
      <span class="pv-num">2</span><span class="pv-hi">Bly</span>
      <span class="pv-bartime" style="margin-left:auto">548</span>
    </div>
    <div class="pv-row pv-small">
      <span class="pv-dim">Rogue 31% · Monk 24% · Wizard 18%</span>
    </div>
  {:else}
    <div class="pv-row pv-small"><span class="pv-dim">No preview</span></div>
  {/if}
  <div class="pv-note">Example data — pop out to see it live.</div>
</div>

<style>
  .pv {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 8px 10px;
    border-top: 1px solid var(--border);
    background: rgba(0, 0, 0, 0.25);
    font-size: 12px;
  }
  .pv-row {
    display: flex;
    align-items: baseline;
    gap: 6px;
    min-width: 0;
  }
  .pv-map {
    position: relative;
    height: 64px;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: linear-gradient(
      rgba(255, 255, 255, 0.05),
      rgba(255, 255, 255, 0.02)
    );
  }
  .pv-dot {
    position: absolute;
    left: 40%;
    top: 45%;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #e3a008;
    box-shadow: 0 0 6px #e3a008;
  }
  .pv-flag {
    position: absolute;
    left: 65%;
    top: 18%;
    color: #6ecbff;
    font-size: 12px;
  }
  .pv-maplbl {
    position: absolute;
    right: 6px;
    bottom: 3px;
    font-size: 9.5px;
    color: var(--text-muted);
  }
  .pv-small {
    font-size: 10.5px;
  }
  .pv-dim {
    color: var(--text-muted);
  }
  .pv-hi {
    color: var(--text-primary);
    font-weight: 600;
  }
  .pv-big {
    font-size: 17px;
    font-weight: 700;
    color: var(--text-primary);
    font-variant-numeric: tabular-nums;
  }
  .pv-proc {
    color: #e3a008;
    font-weight: 700;
    font-size: 11px;
  }
  .pv-num {
    background: #4a4a4a;
    color: #e6e6e6;
    border-radius: 3px;
    padding: 0 4px;
    font-size: 10px;
    font-weight: 700;
    font-family: var(--font-mono);
  }
  .pv-delta {
    font-size: 10px;
    font-family: var(--font-mono);
    color: #fff;
  }
  .pv-av {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: var(--bg-panel);
    border: 1px solid var(--success);
    font-size: 9px;
    font-weight: 700;
    color: var(--text-secondary);
  }
  .pv-ok {
    color: #7fb069;
    font-weight: 700;
  }
  /* Same bar anatomy as the overlays: name left, time right, fill behind. */
  .pv-bar {
    position: relative;
    height: 16px;
    border-radius: 4px;
    overflow: hidden;
    background: rgba(255, 255, 255, 0.05);
  }
  .pv-fill {
    position: absolute;
    inset: 0 auto 0 0;
  }
  .pv-fill.teal {
    background: rgba(79, 179, 169, 0.55);
  }
  .pv-fill.gold {
    background: rgba(227, 160, 8, 0.45);
  }
  .pv-fill.violet {
    background: rgba(165, 143, 214, 0.5);
  }
  .pv-barlbl,
  .pv-bartime {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    font-size: 10.5px;
    font-weight: 600;
    color: var(--text-primary);
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.7);
    white-space: nowrap;
  }
  .pv-barlbl {
    left: 6px;
  }
  .pv-bartime {
    right: 6px;
    font-family: var(--font-mono);
  }
  /* The threat preview's 100% composition bar, 2px surface gaps like the
     real one. */
  .pv-stack {
    display: flex;
    gap: 2px;
    height: 8px;
    border-radius: 4px;
    overflow: hidden;
  }
  .pv-note {
    margin-top: 2px;
    font-size: 9.5px;
    font-style: italic;
    color: var(--text-muted);
  }
</style>
