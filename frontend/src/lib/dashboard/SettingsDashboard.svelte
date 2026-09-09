<script>
  // Officer settings dashboard — the aggregate of every install's daily
  // settings snapshot (server: clientSettings.go), opened by clicking the
  // one-line dashboard strip on the Clients tab. Every number is available
  // exactly on hover, and every percentage says what it is a share OF.
  //
  // SETTINGS TELEMETRY (CLAUDE.md): a new Settings bool appears here on its
  // own under "Other settings" (raw tag) until lib/settingsMeta.js labels it.
  // Anything that isn't a Settings bool needs a row or chart added below.
  import PercentBars from "./PercentBars.svelte";
  import Donut from "./Donut.svelte";
  import Histogram from "./Histogram.svelte";
  import RankBars from "./RankBars.svelte";
  import StackedBar from "./StackedBar.svelte";
  import KpiStrip from "./KpiStrip.svelte";
  import { SETTINGS_SECTIONS, MAP_OPTIONS } from "../settingsMeta.js";
  import { catColor } from "../catColor.js";
  import { pct, fmtPct } from "./chartTheme.js";

  export let summary = null;
  export let loading = false;
  export let error = "";

  let sortByPct = false;
  let openSection = -1;

  $: R = (summary && summary.reporting && summary.reporting.installs) || 0;
  $: L = (summary && summary.reporting && summary.reporting.linked) || 0;
  $: empty = !!summary && R === 0;

  const onN = (obj, key) => (obj && obj[key]) || { on: 0, n: 0 };
  const num = (v) => (typeof v === "number" ? v : 0);
  const lastSeg = (k) => String(k || "").split("/").pop();
  const hist = (obj, order) =>
    order.map((l) => ({ label: l, count: num(obj && obj[l]) }));

  // ── General tab clusters ──────────────────────────────────────────────────
  // A section or item marked `linked` reads the linked-only aggregate and
  // says so in the hover. "Other settings" also picks up any reported key no
  // cluster labels yet (a brand-new toggle shows up with its raw tag).
  function clusterItems(section, s, sort) {
    const src = (s && s.settings) || {};
    const lsrc = (s && s.linked_settings) || {};
    const items = section.items.map((it) => {
      const linked = !!(it.linked || section.linked);
      const e = onN(linked ? lsrc : src, it.key);
      return {
        key: it.key,
        label: it.label,
        on: e.on,
        n: e.n,
        tip: it.tip,
        note: linked
          ? "Linked installs only — this setting lives on the server and unlinked clients have none."
          : "",
      };
    });
    if (section.id === "other") {
      const known = new Set(
        SETTINGS_SECTIONS.flatMap((x) => x.items.map((i) => i.key)),
      );
      for (const k of Object.keys(src).sort()) {
        if (known.has(k)) continue;
        items.push({
          key: k,
          label: k,
          on: src[k].on,
          n: src[k].n,
          tip: "Reported by clients but not labeled yet — add it to lib/settingsMeta.js.",
        });
      }
    }
    return sort
      ? [...items].sort((a, b) => pct(b.on, b.n) - pct(a.on, a.n))
      : items;
  }
  $: clusters = summary
    ? SETTINGS_SECTIONS.map((sec) => ({
        ...sec,
        rows: clusterItems(sec, summary, sortByPct),
      }))
    : [];

  // ── preferences (non-bool General/other settings) ─────────────────────────
  $: prefs = (summary && summary.prefs) || {};
  $: titleSlices = [
    ["always", "Always shown"],
    ["locked", "Hidden while locked"],
    ["zero", "Only while active"],
  ].map(([k, l]) => ({
    label: l,
    count: num(prefs.overlay_titles && prefs.overlay_titles[k]),
  }));
  $: volumeBuckets = hist(prefs.volume, ["0", "1-49", "50-99", "100"]);
  $: archive = prefs.archive || {};

  // ── Map ───────────────────────────────────────────────────────────────────
  $: map = (summary && summary.map) || {};
  $: mapRows = MAP_OPTIONS.map((o) => {
    const e = onN(map.bools, o.key);
    const fromPrefs = o.key !== "stay_unlocked" && o.key !== "overlay_open";
    return {
      key: o.key,
      label: o.label,
      on: e.on,
      n: e.n,
      tip: o.tip,
      note: fromPrefs
        ? `Map preferences come from installs that have opened the Map tab or map overlay (${num(map.n_prefs)} of ${R}).`
        : "",
    };
  });
  const RADIUS_ORDER = ["Off", "Bard", "Druid", "Ranger", "Leash", "Custom"];
  $: radiusSlices = (() => {
    const r = map.radius || {};
    const keys = [
      ...RADIUS_ORDER.filter((k) => r[k]),
      ...Object.keys(r)
        .filter((k) => !RADIUS_ORDER.includes(k))
        .sort(),
    ];
    return keys.map((k) => ({ label: k, count: num(r[k]) }));
  })();
  $: opacityBuckets = hist(map.opacity, ["≤0.5", "0.6", "0.7", "0.8", "0.9–1"]);
  $: cr = map.custom_radius || {};

  // ── Overlays ──────────────────────────────────────────────────────────────
  const OVERLAY_TIPS = {
    pulseaudio: "Audio Cue: play one sound the moment the cleric before you starts casting.",
    timing: "Show “+3.4s” after each cleric in the CH chain.",
    flash: "Pulse animations: tank proc counters, sieve counts, the CH cast-start flash.",
    breakdown: "A bar under the DPS number splitting the fight's damage by type.",
    smarthide: "Only show the overlay while YOU are in the raid voice channel.",
    speedo: "Racing Mode: the speedometer readout.",
    focusmode: "Racing Mode: focus mode.",
    fit: "Shrink the overlay's height to its content.",
    aot: "Keep the overlay above the game window.",
  };
  $: ov = (summary && summary.overlays) || {};
  $: ovOpen = (ov.open || []).map((o) => ({ label: o.label, value: o.installs }));
  $: ovGroups = (ov.settings || []).map((g) => ({
    ...g,
    rows: (g.items || []).map((it) => ({
      key: it.key,
      label: it.label,
      on: it.on,
      n: it.n,
      tip: OVERLAY_TIPS[it.key] || "",
      note: `Of the ${g.n} installs that have configured the ${g.label} overlay.`,
    })),
  }));
  $: cats = (ov.categories && {
    timers: ov.categories.timers || {},
    alerts: ov.categories.alerts || {},
  }) || { timers: {}, alerts: {} };
  $: timerCatRows = [
    {
      key: "auto_pause",
      label: "Auto-pause when out of world",
      on: num(cats.timers.auto_pause),
      tip: "Freeze the overlay's timers when the character leaves the world instead of discarding them.",
    },
    {
      key: "carry_timers",
      label: "Keep running across characters",
      on: num(cats.timers.carry_timers),
      tip: "Timers count down on real-world time across swaps, logouts, and restarts (mob respawn windows).",
    },
    {
      key: "flash_ended_early",
      label: "Flash when ended early",
      on: num(cats.timers.flash_ended_early),
      tip: "An End Early condition flashes the bar three times and fades it instead of removing it instantly.",
    },
  ].map((r) => ({
    ...r,
    n: num(cats.timers.n),
    note: `Counted over ${num(cats.timers.n)} configured timer overlays across all installs.`,
  }));
  $: alertCatRows = [
    {
      key: "alert_stopwatch",
      label: "Stopwatch on alerts",
      on: num(cats.alerts.stopwatch),
      tip: "A ticking m:ss “shown for” clock on each alert.",
    },
    {
      key: "flash_ended_early",
      label: "Flash when ended early",
      on: num(cats.alerts.flash_ended_early),
      tip: "An End Early condition flashes the alert and fades it instead of removing it instantly.",
    },
  ].map((r) => ({
    ...r,
    n: num(cats.alerts.n),
    note: `Counted over ${num(cats.alerts.n)} configured alert overlays across all installs.`,
  }));
  $: alertSecondsBuckets = hist(cats.alerts.seconds_hist, [
    "default",
    "≤10",
    "11-30",
    "31-60",
    "60+",
  ]).map((b) => ({ ...b, label: b.label === "default" ? "default" : b.label + " s" }));

  // ── Fuse triggers ─────────────────────────────────────────────────────────
  $: fuse = (summary && summary.fuse) || {};
  $: fuseSections = (fuse.sections || []).map((s) => ({
    key: s.name,
    label: s.name,
    on: s.enabled_pairs,
    n: s.total_pairs,
    tip: `${s.installs_any} of ${num(fuse.installs_with_package)} installs with the package have something enabled here.`,
    note: "A pair is one character × one trigger; the bar is the share of pairs enabled. Click to open the groups inside.",
  }));
  $: openGroups =
    openSection >= 0 && fuse.sections && fuse.sections[openSection]
      ? (fuse.sections[openSection].groups || []).map((g) => ({
          key: g.name,
          label: g.name,
          on: g.enabled_pairs,
          n: g.total_pairs,
          tip: `Inside ${fuse.sections[openSection].name}.`,
          note: "Share of character × trigger pairs enabled.",
        }))
      : [];
  $: versionLines = Object.entries(fuse.versions || {})
    .sort((a, b) => Number(b[0]) - Number(a[0]))
    .map(([v, n]) => `|v${v}|${n}`)
    .join("\n");
  const rank = (arr) =>
    (arr || []).map((r) => ({ label: lastSeg(r.key), value: r.installs, sub: r.key }));
  const rankLabels = (arr) =>
    (arr || []).map((r) => ({ label: r.label, value: r.installs }));

  // ── Customizations / personal / reminders / magelos ───────────────────────
  $: custom = (summary && summary.custom) || {};
  $: overrideBuckets = hist(custom.override_hist, ["0", "1-3", "4-10", "11+"]);
  $: personal = (summary && summary.personal) || {};
  $: personalBuckets = hist(personal.hist, ["0", "1-5", "6-20", "21+"]);
  $: personalCats = Object.entries(personal.categories || {})
    .sort((a, b) => b[1] - a[1])
    .map(([label, count]) => ({
      label,
      count,
      color: label === "(unassigned)" ? "#4d5270" : catColor(label),
    }));
  $: rem = (summary && summary.reminders) || {};
  $: remKindSlices = [
    ["event", "Zone events"],
    ["boat", "Boats"],
    ["quake", "Earthquakes"],
  ].map(([k, l]) => ({ label: l, count: num(rem.by_kind && rem.by_kind[k]) }));
  $: leadBuckets = hist(rem.lead_hist, ["≤5", "10", "15", "30", "30+"]).map(
    (b) => ({ ...b, label: b.label + " min" }),
  );
  $: mag = (summary && summary.magelos) || {};
  $: mageloBuckets = hist(mag.hist, ["0", "1", "2-3", "4+"]);
</script>

{#if loading && !summary}
  <div class="empty working">
    <span class="spinner"></span>
    <span>Loading settings dashboard…</span>
  </div>
{:else if error && !summary}
  <div class="msg error">{error}</div>
{:else if empty}
  <div class="msg">
    No settings snapshots yet — clients report once a day during off hours.
  </div>
{:else if summary}
  <div class="sd-grid">
    <!-- 1. General tab + other settings -->
    <section class="sd-panel sd-wide">
      <div class="sd-head">
        <span class="section-title">Settings</span>
        <span class="sd-denom"
          >share of {R} reporting installs · linked-only rows of {L}</span
        >
        <button
          class="sd-pill"
          class:on={sortByPct}
          on:click={() => (sortByPct = !sortByPct)}
          title="Order each cluster by share enabled instead of General-tab order"
          >sort by %</button
        >
      </div>
      <div class="sd-clusters">
        {#each clusters as c (c.id)}
          <div class="sd-cluster">
            <div class="sd-sub">{c.title}</div>
            <PercentBars items={c.rows} />
          </div>
        {/each}
      </div>
      <div class="sd-row3">
        <div>
          <div class="sd-sub">Overlay title bars</div>
          <Donut
            slices={titleSlices}
            total={R}
            subject="installs"
            centerLabel="installs"
            height={150}
          />
        </div>
        <div>
          <div class="sd-sub">Trigger volume</div>
          <Histogram
            buckets={volumeBuckets}
            total={R}
            subject="installs"
            height={140}
          />
        </div>
        <div>
          <div class="sd-sub">Log archiving</div>
          <div class="sd-text">
            {num(archive.n_on)} of {R} installs archive their logs.
            {#if num(archive.n_on)}
              Median size threshold: {archive.size_median_mb
                ? archive.size_median_mb + " MB"
                : "default"}; {num(archive.delete_never)} keep archives forever{#if archive.delete_days_median},
                the rest delete after a median {archive.delete_days_median} days{/if}.
            {/if}
          </div>
        </div>
      </div>
    </section>

    <!-- 2. Map -->
    <section class="sd-panel">
      <div class="sd-head">
        <span class="section-title">Map</span>
        <span class="sd-denom">{num(map.n_prefs)} of {R} report map prefs</span>
      </div>
      <PercentBars items={mapRows} />
      <div class="sd-row2">
        <div>
          <div class="sd-sub">Radius ring</div>
          <Donut
            slices={radiusSlices}
            total={num(map.n_prefs)}
            subject="installs"
            centerLabel="installs"
            height={160}
          />
        </div>
        <div>
          <div class="sd-sub">Overlay opacity</div>
          <Histogram
            buckets={opacityBuckets}
            total={num(map.n_prefs)}
            subject="installs"
            height={140}
          />
          <div class="sd-note">
            median {map.opacity_median ?? "—"}{#if cr.n}
              · custom radius: median {cr.median} ({cr.min}–{cr.max}, {cr.n} installs){/if}
          </div>
        </div>
      </div>
    </section>

    <!-- 3. Overlays -->
    <section class="sd-panel">
      <div class="sd-head">
        <span class="section-title">Overlays</span>
        <span class="sd-denom">open state of {R} installs</span>
      </div>
      <div class="sd-sub">Overlays in use</div>
      <RankBars
        rows={ovOpen}
        total={R}
        subject="installs"
        empty="No overlays reported open"
      />
      {#each ovGroups as g (g.kind)}
        <div class="sd-sub">
          {g.label}
          <span class="sd-sub-n">{g.n} configured</span>
        </div>
        <PercentBars items={g.rows} />
      {/each}
      {#if num(cats.timers.n)}
        <div class="sd-sub">
          Timer overlays <span class="sd-sub-n">{num(cats.timers.n)} configured</span>
        </div>
        <PercentBars items={timerCatRows} subject="timer overlays" />
      {/if}
      {#if num(cats.alerts.n)}
        <div class="sd-sub">
          Alert overlays <span class="sd-sub-n">{num(cats.alerts.n)} configured</span>
        </div>
        <PercentBars items={alertCatRows} subject="alert overlays" />
        <div class="sd-sub">Time shown per alert</div>
        <Histogram
          buckets={alertSecondsBuckets}
          total={num(cats.alerts.n)}
          subject="alert overlays"
          height={130}
        />
      {/if}
    </section>

    <!-- 4. Fuse triggers -->
    <section class="sd-panel sd-wide">
      <div class="sd-head">
        <span class="section-title">Fuse triggers</span>
        <span class="sd-denom"
          >{num(fuse.installs_with_package)} of {R} installs carry the package</span
        >
      </div>
      <KpiStrip
        items={[
          {
            val: num(fuse.installs_with_package),
            label: "with Fuse package",
            tip: `**Package versions in the field**\n${versionLines || "|none|0"}\n|Unpublished officer edits|${num(fuse.dirty)}`,
          },
          {
            val: fuse.avg_enabled_per_char ?? 0,
            label: "enabled per character",
            tip: "**Average enabled triggers per character**\nΣ enabled character × trigger pairs ÷ Σ characters, over installs with at least one evaluated character.",
          },
          {
            val: num(fuse.chars_total),
            label: "characters covered",
            tip: "**Characters evaluated**\nEvery configured or current character across reporting installs. Classes only — names never leave the client.",
          },
        ]}
      />
      <div class="sd-row2">
        <div>
          <div class="sd-sub">Most used areas</div>
          <PercentBars
            items={fuseSections}
            subject="pairs"
            onSelect={(i) => (openSection = openSection === i ? -1 : i)}
          />
          {#if openGroups.length}
            <div class="sd-sub sd-indent">
              {fuse.sections[openSection].name}
              <button class="sd-pill" on:click={() => (openSection = -1)}
                >close</button
              >
            </div>
            <div class="sd-indent">
              <PercentBars items={openGroups} subject="pairs" />
            </div>
          {/if}
        </div>
        <div>
          <div class="sd-sub">Most enabled triggers</div>
          <RankBars
            rows={rank(fuse.top_enabled)}
            total={num(fuse.installs_with_package)}
            subject="installs"
          />
          <div class="sd-sub">Most disabled triggers</div>
          <RankBars
            rows={rank(fuse.top_disabled)}
            total={num(fuse.installs_with_package)}
            subject="installs"
          />
        </div>
      </div>
    </section>

    <!-- 5. Customizations -->
    <section class="sd-panel">
      <div class="sd-head">
        <span class="section-title">Customizations</span>
        <span class="sd-denom">share of {R} installs</span>
      </div>
      <KpiStrip
        items={[
          {
            val: fmtPct(num(custom.installs_any_mute), R),
            label: "mute something",
            tip: `**Installs with at least one muted trigger**\n|Installs|${num(custom.installs_any_mute)} of ${R}`,
          },
          {
            val: fmtPct(num(custom.installs_any_clip), R),
            label: "block clipboard",
            tip: `**Installs with at least one clipboard-blocked trigger**\n|Installs|${num(custom.installs_any_clip)} of ${R}`,
          },
          {
            val: fmtPct(num(custom.installs_any_override), R),
            label: "customize a trigger",
            tip: `**Installs with at least one local customization** (sound, text, TTS, color, overlay)\n|Installs|${num(custom.installs_any_override)} of ${R}`,
          },
        ]}
      />
      <div class="sd-sub">Most customized</div>
      <RankBars rows={rank(custom.top_override)} total={R} subject="installs" />
      <div class="sd-sub">Most muted</div>
      <RankBars rows={rank(custom.top_muted)} total={R} subject="installs" />
      <div class="sd-sub">Most clipboard-blocked</div>
      <RankBars rows={rank(custom.top_clip)} total={R} subject="installs" />
      <div class="sd-sub">Customizations per install</div>
      <Histogram
        buckets={overrideBuckets}
        total={R}
        subject="installs"
        height={130}
      />
    </section>

    <!-- 6. Personal timers -->
    <section class="sd-panel">
      <div class="sd-head">
        <span class="section-title">Personal timers</span>
        <span class="sd-denom">{num(personal.total)} across {R} installs</span>
      </div>
      <KpiStrip
        items={[
          {
            val: personal.avg ?? 0,
            label: "per install",
            tip: `**Average personal triggers per install**\n|Total|${num(personal.total)}\n|Installs|${R}`,
          },
          {
            val: fmtPct(num(personal.installs_shared), R),
            label: "have a Shared group",
            tip: `**Accepted a trigger share from another member**\n|Installs|${num(personal.installs_shared)} of ${R}`,
          },
          {
            val: fmtPct(num(personal.installs_gina), R),
            label: "imported GINA",
            tip: `**Imported at least one GINA group**\n|Installs|${num(personal.installs_gina)} of ${R}`,
          },
        ]}
      />
      <div class="sd-sub">Personal triggers per install</div>
      <Histogram
        buckets={personalBuckets}
        total={R}
        subject="installs"
        height={130}
      />
      <div class="sd-row2">
        <div>
          <div class="sd-sub">Timers vs alerts</div>
          <Donut
            slices={[
              { label: "Timers", count: num(personal.timers) },
              { label: "Alerts", count: num(personal.alerts) },
            ]}
            subject="triggers"
            centerLabel="triggers"
            height={140}
          />
        </div>
        <div>
          <div class="sd-sub">Overlay assignment</div>
          <StackedBar segments={personalCats} subject="triggers" />
        </div>
      </div>
    </section>

    <!-- 7. Reminders -->
    <section class="sd-panel">
      <div class="sd-head">
        <span class="section-title">Reminders</span>
        <span class="sd-denom">Server Timers board bells</span>
      </div>
      <KpiStrip
        items={[
          {
            val: fmtPct(num(rem.installs_any), R),
            label: "set a reminder",
            tip: `**Installs with at least one reminder**\n|Installs|${num(rem.installs_any)} of ${R}`,
          },
          {
            val: num(rem.total),
            label: "reminders set",
            tip: `**Reminders across all installs**\n|Sound|${num(rem.sound)}\n|Speech|${num(rem.speak)}\n|Both|${num(rem.both)}\n|Repeating|${num(rem.repeat)}`,
          },
        ]}
      />
      <div class="sd-row2">
        <div>
          <div class="sd-sub">By kind</div>
          <Donut
            slices={remKindSlices}
            subject="reminders"
            centerLabel="reminders"
            height={150}
          />
        </div>
        <div>
          <div class="sd-sub">Lead time</div>
          <Histogram
            buckets={leadBuckets}
            total={num(rem.total)}
            subject="reminders"
            height={130}
          />
          <div class="sd-note">earthquake reminders have no lead time</div>
        </div>
      </div>
      <div class="sd-sub">Most-set reminders</div>
      <RankBars
        rows={rankLabels(rem.top)}
        total={R}
        subject="installs"
        empty="No reminders set anywhere yet"
      />
    </section>

    <!-- 8. Custom magelos -->
    <section class="sd-panel">
      <div class="sd-head">
        <span class="section-title">Custom magelos</span>
        <span class="sd-denom">{num(mag.n)} linked installs reporting</span>
      </div>
      <KpiStrip
        items={[
          {
            val: fmtPct(num(mag.linked_installs_any), num(mag.n)),
            label: "built one",
            tip: `**Linked installs with at least one custom magelo**\n|Installs|${num(mag.linked_installs_any)} of ${num(mag.n)}\nUnlinked installs can't save magelos, so they're not counted.`,
          },
          {
            val: num(mag.total),
            label: "custom magelos",
            tip: "**Named magelos across reporting linked installs** — the auto “current” snapshot is not counted.",
          },
        ]}
      />
      <div class="sd-sub">Custom magelos per linked install</div>
      <Histogram
        buckets={mageloBuckets}
        total={num(mag.n)}
        subject="linked installs"
        height={130}
      />
    </section>
  </div>
{/if}

<style>
  .sd-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(440px, 1fr));
    gap: 12px;
    padding-bottom: 12px;
  }
  .sd-panel {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 10px 12px 8px;
    min-width: 0;
  }
  .sd-wide {
    grid-column: 1 / -1;
  }
  .sd-head {
    display: flex;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 4px;
  }
  .section-title {
    color: var(--accent);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .sd-denom {
    color: var(--text-muted);
    font-size: 10.5px;
    margin-left: auto;
    white-space: nowrap;
  }
  .sd-pill {
    background: none;
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 10px;
    padding: 1px 8px;
  }
  .sd-pill:hover {
    color: var(--text-primary);
  }
  .sd-pill.on {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .sd-clusters {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
    gap: 4px 20px;
  }
  .sd-cluster {
    min-width: 0;
  }
  .sd-sub {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.07em;
    text-transform: uppercase;
    color: var(--text-secondary);
    margin: 8px 0 2px;
  }
  .sd-sub-n {
    font-weight: 400;
    letter-spacing: 0;
    text-transform: none;
    color: var(--text-muted);
  }
  .sd-row2 {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 4px 18px;
  }
  .sd-row3 {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 4px 18px;
    margin-top: 6px;
  }
  .sd-indent {
    padding-left: 14px;
  }
  .sd-text {
    color: var(--text-secondary);
    font-size: 11.5px;
    line-height: 1.5;
    padding: 6px 0;
  }
  .sd-note {
    color: var(--text-muted);
    font-size: 10.5px;
    font-style: italic;
    margin-top: -4px;
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
  .empty {
    color: var(--text-muted);
    font-size: 12px;
    text-align: center;
    padding: 28px 14px;
  }
  /* The "loading" state: gold spinner above the label (LogsTab's recipe). */
  .empty.working {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
    margin-top: 40px;
  }
  .empty.working .spinner {
    width: 28px;
    height: 28px;
    border: 3px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: sd-spin 0.8s linear infinite;
  }
  @keyframes sd-spin {
    to {
      transform: rotate(360deg);
    }
  }

  /* ApexCharts custom tooltips (chartTheme.js tipHTML) in the app's own
     tooltip look. The library wraps them in .apexcharts-tooltip, whose dark
     theme chrome is stripped so only the .sd-tip card shows. */
  :global(.sd-grid .apexcharts-tooltip) {
    background: transparent !important;
    border: 0 !important;
    box-shadow: none !important;
    overflow: visible !important;
  }
  :global(.sd-tip) {
    max-width: 340px;
    background: #0d1930;
    border: 1px solid var(--accent, #c8a951);
    border-radius: 6px;
    color: var(--text-primary, #e6e6e6);
    padding: 6px 9px;
    font-size: 12px;
    line-height: 1.45;
    box-shadow: 0 6px 16px rgba(0, 0, 0, 0.5);
    white-space: normal;
  }
  :global(.sd-tip-t) {
    color: var(--accent, #c8a951);
    font-weight: 700;
  }
  :global(.sd-tip-grid) {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 1px 10px;
    margin-top: 3px;
  }
  :global(.sd-tip-l) {
    color: var(--text-muted, #8a93a5);
    font-weight: 600;
  }
  :global(.sd-tip-n) {
    margin-top: 4px;
    color: var(--text-secondary, #b0b6c8);
    font-size: 11.5px;
    white-space: pre-line;
  }
  :global(.sd-grid .apexcharts-legend-text) {
    color: var(--text-secondary) !important;
  }
</style>
