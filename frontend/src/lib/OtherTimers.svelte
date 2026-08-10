<script>
  // "Raid Specific Timers" — raid/event countdown bars sourced from the local
  // trigger engine, filtered to the shared-package folder that owns them: the
  // Ring War wave chain, Sirran on Sky, and the single-target mobs' AE
  // cooldowns (Ice Breath, Dragon Roar, Ancient Breath, …).
  //
  // Used two ways:
  //   - inside RaidCardView (card passed in): filters by the card's event_key
  //     for event raids, or its target mob for single-target raids.
  //   - as the special overlay (no card): polls GetTimers for the live raid and
  //     resolves the same key.
  //
  // The overlay's internal kind is still "othertimers" — it keys every
  // character's saved geometry, so renaming it would reset their layouts.
  // Hidden entirely (hasAny=false) when nothing matches — an empty timer
  // panel is dead space on the card and a translucent nothing as an overlay.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { GetTriggerState, GetTimers } from "../../bindings/FuseBridge/app.js";

  export let card = null;
  export let showLabel = true;
  export let hasAny = false;

  // Folder filters, keyed by event key for event raids and by lowercased mob
  // name for single-target raids. Each entry is a list of alternatives; an
  // alternative is a chain of lowercase substrings that must appear, IN ORDER,
  // across the timer's folder path. ("5 - Ring War" matches ["ring war"].)
  //
  // Every mob chain is anchored on "raid spells" — the shared package's
  // "05 - Raiding > 02 - Raid Spells/AoEs" tree. That anchor is what keeps the
  // duplicates out: several of these AEs also have a "Faux" countdown under
  // 02 - Common and a variant under 07 - Situational, and matching on the mob
  // name alone would pull those in too. Only folder names are matched, never
  // the trigger's own name, so a Faux timer titled "(Lord Yelinak)" can't
  // sneak through on its title.
  // Each alternative is {chain, name}: the folder chain locates the trigger's
  // home, and `name` picks the ONE timer wanted out of it. Both are required —
  // a mob's folder holds every AE it casts, and only the named ones belong
  // here. `name` matches the running timer's TimerName, which is what the bar
  // is labelled with.
  //
  // The chain is still load-bearing even with a name test, because TimerNames
  // repeat across the package: "Ice Breath Cooldown" is used by both Yelinak's
  // real trigger and the Faux countdown under 02 - Common. Anchoring on
  // "raid spells" is what keeps the Faux and 07 - Situational copies out.
  const FOLDER_FILTERS = {
    // ── event raids ──
    // Ring War keeps its whole folder: the wave chain is the raid, and the
    // redundant per-wave bars are collapsed into the derived rows below.
    ringwar: [{ chain: ["ring war"] }],
    // Sirran spawns off an island boss death and stays up 15 minutes; his
    // timer lives under Classic Dragons rather than with the Velious content.
    sky: [{ chain: ["raid spells", "plane of sky"], name: /^sirran$/i }],

    // ── single-target raids (keyed on card.target) ──
    "lord yelinak": [
      { chain: ["raid spells", "lord yelinak"], name: /^ice breath cooldown$/i },
    ],
    trakanon: [
      { chain: ["raid spells", "trakanon"], name: /^trakanon banish cooldown$/i },
    ],
    klandicar: [
      { chain: ["raid spells", "klandicar"], name: /^dragon fear cooldown$/i },
    ],
    sontalak: [
      { chain: ["raid spells", "sontalak"], name: /^lava breath cooldown$/i },
    ],
    // No Dragon Roar trigger exists for Wuoshi in the package yet (his folder
    // has only Ceticious Cloud). Matches nothing until one is added.
    wuoshi: [
      { chain: ["raid spells", "wuoshi"], name: /^dragon (roar|fear) cooldown$/i },
    ],
    zlandicar: [
      { chain: ["raid spells", "zlandicar"], name: /^stun breath cooldown$/i },
    ],
    aaryonar: [
      {
        chain: ["raid spells", "aaryonar"],
        name: /^cloud of disempowerment cooldown$/i,
      },
    ],
    // "Bellowing Winds" is TimerType=NoTimer in the package, so it draws no
    // bar. Matches nothing until it's given a duration.
    "lady nevederia": [
      { chain: ["raid spells", "lady nevederia"], name: /bellowing winds/i },
    ],
    "lord vyemm": [
      { chain: ["raid spells", "lord vyemm"], name: /^scream of chaos cooldown$/i },
    ],
    "vulak`aerr": [
      { chain: ["raid spells", "vulak"], name: /^ancient breath cooldown$/i },
    ],
    zlexak: [
      { chain: ["raid spells", "zlexak"], name: /^diseased cloud cooldown$/i },
    ],
    // Tunare and the Dain have no per-mob folder — their timers sit directly
    // in the zone folder, which makes the name test the only thing separating
    // them from the rest of that zone's triggers.
    tunare: [
      { chain: ["raid spells", "plane of growth"], name: /^protector of growth spawn$/i },
    ],
    "dain frostreaver iv": [
      { chain: ["raid spells", "icewell keep"], name: /^dain banish cooldown$/i },
    ],
  };

  // Mob names drift in punctuation between the DB and the trigger tree
  // (Vulak`Aerr / Vulak'Aerr / Vulak Aerr), so normalise before looking up.
  function filterKeyFor(c) {
    if (!c) return "";
    if (c.event_key) return c.event_key;
    const t = (c.target || "").toLowerCase().trim();
    if (!t) return "";
    if (FOLDER_FILTERS[t]) return t;
    const loose = t.replace(/[`'’]/g, "");
    for (const k of Object.keys(FOLDER_FILTERS)) {
      if (k.replace(/[`'’]/g, "") === loose) return k;
    }
    return "";
  }

  // Ring War spawn schedule, transcribed from the guild's shared trigger set
  // ("05 - Raiding > 02 - Raid Spells/AoEs > 3 - Velious > 5 - Ring War").
  // All offsets are seconds from THAT ocean's kickoff shout, which the server
  // timestamps and ships as card.ocean_starts.
  //
  //   waves        — the ocean's seven wave spawns (210s apart after the first)
  //   nextOceanW1  — wave 1 of the FOLLOWING ocean (the "Wave 7 Break" timer,
  //                  named "2 - 1"/"3 - 1" in the trigger set)
  //   narandi      — Narandi's spawn
  //
  // The three narandi values are independent paths to the same instant and
  // agree within 10s, so re-anchoring on each new shout tightens the estimate
  // rather than jumping it.
  const RW_SCHEDULE = {
    1: {
      waves: [210, 420, 630, 840, 1050, 1260, 1470],
      nextOceanW1: 1982,
      narandi: 5526,
    },
    2: {
      waves: [270, 480, 690, 900, 1110, 1320, 1530],
      nextOceanW1: 2050,
      narandi: 3815,
    },
    3: {
      waves: [272, 482, 692, 902, 1112, 1322, 1532],
      nextOceanW1: 0, // ocean 3 is the last — Narandi follows
      narandi: 2045,
    },
  };

  // Trigger timers that just restate the schedule above ("1 - 5", "Narandi",
  // "4 - Narandi"). The derived rows say the same thing in one line each, so
  // these are dropped to keep the panel readable; every other Ring War timer
  // (Turn in Timer, Time until CHARGE!, …) still shows.
  const RW_REDUNDANT = /^(?:\d+\s*-\s*\d+|(?:\d+\s*-\s*)?narandi.*)$/i;

  let timers = [];
  let now = Date.now();
  let liveEventKey = ""; // popout mode: resolved filter key from GetTimers()
  let liveOceanStarts = null;
  let liveAEs = []; // shared raid-AE anchors from the server (ae_timers)
  let pollTimer, dataTimer, animReq, offTriggers;
  let polling = false,
    pollAgain = false;

  // Targets with a server-side shared AE anchor (raidAE.go): one client in
  // the AE's radius anchors the bar for the whole raid. Card mode only polls
  // GetTimers for these — every other card has nothing to fetch.
  const AE_KEYS = new Set(["vulak`aerr", "dain frostreaver iv", "klandicar"]);

  $: filterKey = card ? filterKeyFor(card) : liveEventKey;
  $: chains = FOLDER_FILTERS[filterKey] || [];
  $: oceanStarts = card ? card.ocean_starts : liveOceanStarts;

  // The newest ocean to have shouted anchors everything: each shout is a fresh
  // measurement of where we are, so later ones supersede earlier estimates.
  $: anchor = (() => {
    if (filterKey !== "ringwar" || !oceanStarts) return null;
    for (let n = 3; n >= 1; n--) {
      const at = Number(oceanStarts[n] || 0);
      if (at > 0) return { ocean: n, at, sched: RW_SCHEDULE[n] };
    }
    return null;
  })();

  // The next thing to spawn: the current ocean's next wave, or — once its
  // seventh has come — wave 1 of the ocean after it. In ocean 3 the waves run
  // out and Narandi is next, which the dedicated row below already covers.
  $: nextSpawn = (() => {
    if (!anchor) return null;
    const { ocean, at, sched } = anchor;
    let prev = at;
    for (let i = 0; i < sched.waves.length; i++) {
      const t = at + sched.waves[i] * 1000;
      if (t > now) {
        return {
          label: `Ocean ${ocean} - Wave ${i + 1}`,
          at: t,
          from: prev,
        };
      }
      prev = t;
    }
    if (sched.nextOceanW1 > 0) {
      const t = at + sched.nextOceanW1 * 1000;
      if (t > now)
        return { label: `Ocean ${ocean + 1} - Wave 1`, at: t, from: prev };
    }
    return null;
  })();

  // Narandi's spawn — shown from the first shout onward, and kept on screen
  // after it lands so the card doesn't go quiet at the moment he arrives.
  $: narandi = anchor
    ? { label: "Narandi", at: anchor.at + anchor.sched.narandi * 1000 }
    : null;

  $: derived = [
    ...(nextSpawn
      ? [
          {
            key: "next",
            label: nextSpawn.label,
            at: nextSpawn.at,
            from: nextSpawn.from,
          },
        ]
      : []),
    ...(narandi
      ? [
          {
            key: "narandi",
            label: narandi.label,
            at: narandi.at,
            from: anchor.at,
            done: narandi.at <= now,
          },
        ]
      : []),
  ];

  function pathMatches(path, chain) {
    const parts = (path || []).map((p) => (p || "").toLowerCase());
    let i = 0;
    for (const want of chain) {
      for (; i < parts.length; i++) {
        if (parts[i].includes(want)) break;
      }
      if (i >= parts.length) return false;
      i++;
    }
    return true;
  }

  // A timer belongs here only if it sits in the right folder AND is one of the
  // timers named for this raid (an alternative with no `name` takes the whole
  // folder — Ring War).
  function specMatches(t, spec) {
    if (!pathMatches(t.path, spec.chain)) return false;
    return !spec.name || spec.name.test((t.name || "").trim());
  }

  // Shared AE anchors for THIS target, shaped like bars. The anchor is the
  // server's first sighting of the cast (any client in range), so everyone's
  // bar starts and restarts together.
  const aeNorm = (s) => (s || "").toLowerCase().replace(/[`'’]/g, "");
  $: aeRows = (liveAEs || [])
    .filter((a) => filterKey && aeNorm(a.mob) === aeNorm(filterKey))
    .map((a) => ({
      key: "ae:" + a.label,
      label: a.label,
      at: a.started_ms + a.dur_ms,
      from: a.started_ms,
    }))
    .filter((a) => a.at > now);
  // While a shared row is live, the locally-fired bar of the same name is
  // redundant (the personally-hit player would see both) — the shared one
  // wins so the whole raid reads one identical bar.
  $: aeLabels = new Set(aeRows.map((a) => a.label.toLowerCase()));

  $: active = chains.length
    ? timers
        .filter(
          (t) =>
            t.ends_at_ms > now &&
            chains.some((c) => specMatches(t, c)) &&
            !(filterKey === "ringwar" && RW_REDUNDANT.test((t.name || "").trim())) &&
            !aeLabels.has((t.name || "").trim().toLowerCase()),
        )
        .sort((a, b) => a.ends_at_ms - b.ends_at_ms)
    : [];
  $: hasAny = active.length > 0 || derived.length > 0 || aeRows.length > 0;

  function fmtRemain(ms) {
    const s = Math.max(0, Math.ceil(ms / 1000));
    const m = Math.floor(s / 60);
    return `${String(m).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
  }
  function barFrac(t) {
    const total = t.ends_at_ms - t.started_at_ms;
    if (total <= 0) return 0;
    return Math.max(0, Math.min(1, (t.ends_at_ms - now) / total));
  }
  // Derived rows drain across the gap since the PREVIOUS milestone (the last
  // wave, or the ocean shout), so a wave bar refills each cycle instead of
  // creeping down one long slope.
  function derivedFrac(d) {
    const total = d.at - d.from;
    if (total <= 0) return 0;
    return Math.max(0, Math.min(1, (d.at - now) / total));
  }

  async function poll() {
    if (polling) {
      pollAgain = true;
      return;
    }
    polling = true;
    try {
      const s = await GetTriggerState();
      timers = s.timers || [];
    } catch {
      /* keep last */
    }
    polling = false;
    if (pollAgain) {
      pollAgain = false;
      poll();
    }
  }

  // Resolve the live raid context. In popout mode this picks the folder
  // filter key; in card mode the parent owns the card and this only harvests
  // the shared AE anchors — and only for the three targets that have any.
  async function pollContext() {
    if (card && !AE_KEYS.has(filterKey)) return;
    try {
      const d = await GetTimers();
      liveAEs = (d && d.ae_timers) || [];
      if (card) return;
      // Same precedence as the raid overlays (PopoutRaidSection): a live mob
      // raid first — an interrupt's AE timers belong to that fight — then the
      // event raid.
      let active = null;
      for (const m of (d && d.mobs) || []) {
        if (m.is_raid && m.raid && m.raid.status !== "complete") {
          active = m.raid;
          break;
        }
      }
      const c = active || (d && d.event_raid) || null;
      liveEventKey = filterKeyFor(c);
      liveOceanStarts = (d && d.event_raid && d.event_raid.ocean_starts) || null;
    } catch {
      /* keep last */
    }
  }

  function animLoop() {
    now = Date.now();
    animReq = requestAnimationFrame(animLoop);
  }

  onMount(async () => {
    await poll();
    await pollContext();
    offTriggers = Events.On("triggers-changed", poll);
    pollTimer = setInterval(poll, 1000);
    // Card mode ticks too, for the shared AE anchors — pollContext itself
    // skips the fetch unless this card's target has any.
    dataTimer = setInterval(pollContext, 5000);
    animLoop();
  });
  onDestroy(() => {
    clearInterval(pollTimer);
    if (dataTimer) clearInterval(dataTimer);
    if (offTriggers) offTriggers();
    if (animReq) cancelAnimationFrame(animReq);
  });
</script>

<div class="rc-col">
  {#if showLabel && hasAny}<div class="rc-label">Raid Specific Timers</div>{/if}
  <!-- Derived Ring War rows first: what spawns next, and Narandi. These are
       always present once an ocean has shouted, so the raid always has an
       answer to "what's coming and when" without reading seven bars. -->
  {#each derived as d (d.key)}
    <div class="obar" class:sched={true} class:done={d.done}>
      <div
        class="obar-fill"
        style="width:{derivedFrac(d) * 100}%"
      ></div>
      <span class="obar-name">{d.label}</span>
      <span class="obar-time"
        >{d.done ? "UP" : fmtRemain(d.at - now)}</span
      >
    </div>
  {/each}
  <!-- Shared AE anchors: same bar anatomy as the engine timers — to the raid
       these ARE the AE cooldowns, whoever's log happened to anchor them. -->
  {#each aeRows as a (a.key)}
    <div class="obar">
      <div class="obar-fill" style="width:{derivedFrac(a) * 100}%"></div>
      <span class="obar-name">{a.label}</span>
      <span class="obar-time">{fmtRemain(a.at - now)}</span>
    </div>
  {/each}
  {#each active as t (t.id)}
    <div class="obar">
      <div class="obar-fill" style="width:{barFrac(t) * 100}%"></div>
      <span class="obar-name">{t.name}</span>
      <span class="obar-time">{fmtRemain(t.ends_at_ms - now)}</span>
    </div>
  {/each}
</div>

<style>
  .rc-col {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }
  .rc-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e3a008;
    margin-bottom: 3px;
  }
  /* Same bar anatomy as the trigger timer overlays (PopoutTimers), fixed
     height and accent color — these bars live inside the raid card, not a
     styleable overlay category. */
  .obar {
    position: relative;
    height: 20px;
    border-radius: 4px;
    overflow: hidden;
    background: rgba(255, 255, 255, 0.05);
  }
  .obar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    background: rgba(79, 179, 169, 0.55);
  }
  /* Schedule rows are the headline — gold, to separate them from the
     trigger-engine bars below and match the event card's accent. */
  .obar.sched {
    background: rgba(227, 160, 8, 0.12);
  }
  .obar.sched .obar-fill {
    background: rgba(227, 160, 8, 0.45);
  }
  .obar.sched .obar-name {
    color: #f5d67b;
  }
  /* Narandi already up: no bar left to drain, just the standing "UP" state. */
  .obar.done .obar-fill {
    width: 100% !important;
    background: rgba(227, 160, 8, 0.28);
  }
  .obar.done .obar-time {
    color: #f5d67b;
    font-weight: 700;
  }
  .obar-name,
  .obar-time {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.7);
    white-space: nowrap;
  }
  .obar-name {
    left: 8px;
    max-width: 68%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .obar-time {
    right: 8px;
    font-variant-numeric: tabular-nums;
    font-family: var(--font-mono);
  }
</style>
