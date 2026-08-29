<script context="module">
  // Collapsed/expanded raid-card state, at module scope because the tab
  // component unmounts on every tab switch — a card collapsed before leaving
  // the tab stays collapsed on return, for the whole app session. Keyed the
  // same way the toggles are (mob name / event key), so the admin sample card
  // ("Lord Nagafen (sample)") is covered like any other.
  let savedOpenRaid = {};
  // The one optimistic signup action in flight or awaiting the board (Gynok
  // allows a single tracker per member). Module scope for the same reason:
  // Gynok's board takes up to a minute to reflect a signup, and losing the
  // "signing up…" note to a tab switch would read as the click not taking.
  // {mob, role, kind:"start"|"stop", state:"inflight"|"sent"|"error", msg, at,
  //  noteHidden} — noteHidden fades the status note after a few seconds while
  // the optimistic state itself lives on for reconciliation.
  let savedTrackPending = null;
  // An officer-stopped tracker being hidden until the board reflects it:
  // {mob, name (lowercased), at}
  let savedStoppedOther = null;
</script>

<script>
  import { onMount, onDestroy } from "svelte";
  import {
    GetTimers,
    IsAdminMode,
    IsOfficer,
    GetMobHPs,
    GetLocalRaidTimers,
    StartTracking,
    StopTracking,
    StopTrackerFor,
    GetTrackGuides,
    GetTrackGuideContent,
    SaveTrackGuide,
  } from "../../bindings/FuseBridge/app.js";
  import { fade } from "svelte/transition";
  import { linked } from "../lib/linkState.js";
  import { activeTab } from "../lib/nav.js";
  import { scale } from "../lib/scale.js";
  import { confirmDialog } from "../lib/confirm.js";
  import RaidCardView from "../lib/RaidCardView.svelte";
  import LearnMenu from "../lib/LearnMenu.svelte";
  import DiscordMessage from "../lib/DiscordMessage.svelte";

  let data = null;
  let loading = true;
  let admin = false;
  let officer = false;
  let mobHPs = {}; // lower mob name → live HP percent
  let timer;
  let hpTimer;
  let pollMs = 60000; // active raids poll every 5s, otherwise 60s

  let openRaid = savedOpenRaid; // mob name → expanded (default true)
  let completedOpen = {}; // completed raid index → open

  // ── role signup (track / coth / fte via Gynok) ───────────────────────────
  const ROLES = ["track", "coth", "fte"];
  let pending = savedTrackPending;
  let stoppedOther = savedStoppedOther;
  let guides = {}; // lower(mob) → role → guide post url
  let trackerCtx = null; // officer right-click: {x, y, mob, name}
  let guideModal = null; // {mob, role, url, err, saving}

  // Status notes fade on their own after a few seconds — the optimistic
  // pending STATE stays (the board still needs it to reconcile), only the
  // note hides.
  const NOTE_FADE_MS = 5000;
  let noteTimer;
  function scheduleNoteFade(p) {
    clearTimeout(noteTimer);
    if (!p || p.state === "inflight" || p.noteHidden) return;
    const left = Math.max(0, NOTE_FADE_MS - (Date.now() - p.at));
    noteTimer = setTimeout(() => {
      if (pending === p) setPending({ ...p, noteHidden: true });
    }, left);
  }
  function setPending(p) {
    pending = savedTrackPending = p;
    scheduleNoteFade(p);
  }
  function setStoppedOther(v) {
    stoppedOther = savedStoppedOther = v;
  }
  $: inflight = !!(pending && pending.state === "inflight");
  // A stop that has been sent (or is in flight): the UI stops showing the
  // member as signed up immediately instead of waiting out the board lag.
  $: stopPending = !!(
    pending &&
    pending.kind === "stop" &&
    pending.state !== "error"
  );
  function isYou(m) {
    return !!(m.trackers && m.trackers.some((t) => t.is_you));
  }
  // isYou as the UI should SHOW it: a sent stop hides your signup at once.
  function isYouShown(m, sp) {
    return !sp && isYou(m);
  }
  // The tracker list as the UI should show it: your own entry disappears the
  // moment your stop is sent, and an officer-stopped tracker disappears the
  // moment Gynok confirms — both ahead of the board refresh.
  function displayTrackers(m, p, stopped) {
    let ts = m.trackers || [];
    if (p && p.kind === "stop" && p.state !== "error") {
      ts = ts.filter((t) => !t.is_you);
    }
    if (stopped && stopped.mob === m.name) {
      ts = ts.filter((t) => (t.name || "").toLowerCase() !== stopped.name);
    }
    return ts;
  }
  function myRole(m) {
    const t = (m.trackers || []).find((x) => x.is_you);
    return t ? t.role || "track" : "";
  }
  // Which mob the linked member is signed up on, per the real board only —
  // the admin sample rows never count, and a sent stop clears it at once.
  $: yourMob =
    !stopPending && data && data.mobs
      ? (data.mobs.find(isYou) || {}).name
      : undefined;
  // Takes pending as an argument so template expressions using it re-evaluate
  // the moment pending changes (Svelte can't see dependencies inside a
  // function body — a bare pendingStartOn(m) would lag until the next poll).
  function pendingStartOn(p, m) {
    return !!(
      p &&
      p.kind === "start" &&
      p.mob === m.name &&
      p.state !== "error"
    );
  }
  function errText(e) {
    return String(e && e.message ? e.message : e).replace(/^Error:\s*/, "");
  }

  // Zone-grouped zones (ToV/ST/VP/Fear, server-tagged via zone_tag): the
  // track role always signs up the whole zone so Gynok shows coverage across
  // every mob in it; fte asks mob-or-zone; coth stays mob-specific.
  let fteChoice = null; // {m} — the mob-or-zone question for fte

  function signUp(m, role) {
    if (inflight) return;
    if (role === "track" && m.zone_tag) {
      doSignUp(m, role, m.zone_tag);
      return;
    }
    if (role === "fte" && m.zone_tag) {
      fteChoice = { m };
      return;
    }
    doSignUp(m, role, m.name);
  }

  async function doSignUp(m, role, target) {
    fteChoice = null;
    if (inflight) return;
    const zone = target !== m.name ? target : "";
    if (m.sample) {
      setPending({
        mob: m.name,
        role,
        zone,
        kind: "start",
        state: "sent",
        msg: "(sample row — nothing was sent)",
        at: Date.now(),
      });
      return;
    }
    const switching = !!(yourMob && yourMob !== m.name);
    if (switching) {
      const ok = await confirmDialog({
        title: "Switch tracker",
        message: `You're signed up on ${yourMob}. Switch to ${
          zone ? `the ${zone} zone` : m.name
        } (${role})?`,
        detail:
          "Gynok allows one active tracker — your current signup will be stopped first.",
        confirmLabel: "Switch",
      });
      if (!ok) return;
    }
    setPending({
      mob: m.name,
      role,
      zone,
      kind: "start",
      state: "inflight",
      msg: "",
      at: Date.now(),
    });
    try {
      const msg = await StartTracking(target, role, switching);
      setPending({
        mob: m.name,
        role,
        zone,
        kind: "start",
        state: "sent",
        msg,
        at: Date.now(),
      });
    } catch (e) {
      setPending({
        mob: m.name,
        role,
        zone,
        kind: "start",
        state: "error",
        msg: errText(e),
        at: Date.now(),
      });
    }
    load();
  }

  async function stopSelf(m) {
    if (inflight) return;
    if (m.sample) {
      setPending({
        mob: m.name,
        role: "",
        kind: "stop",
        state: "sent",
        msg: "(sample row — nothing was sent)",
        at: Date.now(),
      });
      return;
    }
    setPending({
      mob: m.name,
      role: "",
      kind: "stop",
      state: "inflight",
      msg: "",
      at: Date.now(),
    });
    try {
      const msg = await StopTracking();
      setPending({ mob: m.name, role: "", kind: "stop", state: "sent", msg, at: Date.now() });
    } catch (e) {
      setPending({
        mob: m.name,
        role: "",
        kind: "stop",
        state: "error",
        msg: errText(e),
        at: Date.now(),
      });
    }
    load();
  }

  // Officer: right-click a tracker's name → stop their signup.
  function trackerMenu(e, m, t) {
    if (!officer || !t.name || m.sample) return;
    e.preventDefault();
    // .shell scales its coordinate space with CSS zoom — divide the viewport
    // coords so the fixed-position menu lands at the cursor.
    trackerCtx = {
      x: e.clientX / $scale,
      y: e.clientY / $scale,
      mob: m.name,
      name: t.name,
    };
  }
  async function stopOther() {
    const c = trackerCtx;
    trackerCtx = null;
    if (!c) return;
    const ok = await confirmDialog({
      title: "Stop tracker",
      message: `Stop ${c.name}'s tracker on ${c.mob}?`,
      detail: "Gynok will sign them off and let them know.",
      confirmLabel: "Stop",
      danger: true,
    });
    if (!ok) return;
    try {
      await StopTrackerFor(c.mob, c.name);
      // Hide their row right away rather than waiting out the board refresh.
      setStoppedOther({
        mob: c.mob,
        name: (c.name || "").toLowerCase(),
        at: Date.now(),
      });
    } catch (e) {
      setPending({
        mob: c.mob,
        role: "",
        kind: "stop",
        state: "error",
        msg: errText(e),
        at: Date.now(),
      });
    }
    load();
  }
  const closeMenus = () => {
    trackerCtx = null;
  };

  // ── role guide links (Learn menu) ────────────────────────────────────────
  async function loadGuides() {
    try {
      const list = (await GetTrackGuides()) || [];
      const map = {};
      for (const g of list) {
        const k = (g.mob || "").toLowerCase();
        (map[k] = map[k] || {})[(g.role || "").toLowerCase()] = g.url;
      }
      guides = map;
    } catch {
      guides = {};
    }
  }
  async function offerAddGuide(mob, role) {
    const ok = await confirmDialog({
      title: "No guide yet",
      message: `No guide post is saved for ${role} on ${mob}.`,
      detail: "Would you like to add one?",
      confirmLabel: "Add guide",
    });
    if (!ok) return;
    guideModal = { mob, role, url: "", existing: false, err: "", saving: false };
  }
  function editGuide(mob, role, url) {
    guideModal = { mob, role, url, existing: true, err: "", saving: false };
  }

  // In-app guide viewer: the post's content is fetched by the server's bot
  // and rendered here — no browser round-trip.
  let guideView = null; // {key, role, label, loading, content, err}
  async function openGuide(key, role, label) {
    guideView = { key, role, label, loading: true, content: null, err: "" };
    try {
      const c = await GetTrackGuideContent(key, role);
      if (!guideView || guideView.key !== key || guideView.role !== role) return;
      if (c && c.ok) {
        guideView = { ...guideView, loading: false, content: c };
      } else {
        guideView = {
          ...guideView,
          loading: false,
          err: (c && c.error) || "Couldn't load the guide post",
        };
      }
    } catch (e) {
      if (!guideView || guideView.key !== key || guideView.role !== role) return;
      guideView = { ...guideView, loading: false, err: errText(e) };
    }
  }

  // A full MESSAGE link is required (right-click the post → Copy Message
  // Link) — the in-app viewer needs the message id to fetch content.
  const GUIDE_URL_RE =
    /^https:\/\/(?:(?:ptb|canary)\.)?discord(?:app)?\.com\/channels\/\d+\/\d+\/\d+$/;
  async function saveGuide() {
    const gmod = guideModal;
    if (!gmod || gmod.saving) return;
    if (/\(sample\)$/i.test(gmod.mob)) {
      guideModal = { ...gmod, err: "Sample row — saving is disabled" };
      return;
    }
    const url = (gmod.url || "").trim();
    if (!GUIDE_URL_RE.test(url)) {
      guideModal = {
        ...gmod,
        err: "That doesn't look like a Discord post link (https://discord.com/channels/…)",
      };
      return;
    }
    guideModal = { ...gmod, url, saving: true, err: "" };
    try {
      await SaveTrackGuide(gmod.mob, gmod.role, url);
      guideModal = null;
      await loadGuides();
    } catch (e) {
      guideModal = { ...gmod, url, saving: false, err: errText(e) };
    }
  }

  function toggleRaid(name) {
    openRaid = { ...openRaid, [name]: !(openRaid[name] ?? true) };
    savedOpenRaid = openRaid;
  }
  function toggleCompleted(i) {
    completedOpen = { ...completedOpen, [i]: !completedOpen[i] };
  }

  // Live HP lookups take the map as an arg so Svelte tracks it as a dependency.
  function hpFor(hps, name) {
    return name ? hps[name.toLowerCase()] : undefined;
  }
  function raidLiveHP(hps, m) {
    if (m.sample) return null;
    let h = hpFor(hps, m.raid && m.raid.target);
    if (h === undefined) h = hpFor(hps, m.name);
    return h === undefined ? -1 : h;
  }

  // Poll faster while a real raid is active so assignments/HP stay fresh.
  function reschedule() {
    const active = data && data.mobs && data.mobs.some((m) => m.is_raid);
    const want = active ? 5000 : 60000;
    if (want !== pollMs) {
      pollMs = want;
      clearInterval(timer);
      timer = setInterval(load, pollMs);
    }
  }

  async function load() {
    try {
      data = await GetTimers();
    } catch {
      data = null;
    }
    loading = false;
    // Let go of the optimistic signup note once the board reflects it (or it
    // ages out — Gynok's board edits and the 60s scrape can lag a signup by a
    // couple of minutes).
    if (pending) {
      const aged = Date.now() - pending.at > 180000;
      const mobs = (data && data.mobs) || [];
      const started =
        pending.kind === "start" &&
        mobs.some((m) => m.name === pending.mob && isYou(m));
      const stopped = pending.kind === "stop" && !mobs.some(isYou);
      if (aged || started || stopped) setPending(null);
    }
    if (stoppedOther) {
      const mobs = (data && data.mobs) || [];
      const still = mobs.some(
        (m) =>
          m.name === stoppedOther.mob &&
          (m.trackers || []).some(
            (t) => (t.name || "").toLowerCase() === stoppedOther.name,
          ),
      );
      if (!still || Date.now() - stoppedOther.at > 180000) {
        setStoppedOther(null);
      }
    }
    reschedule();
  }
  async function pollHP() {
    try {
      mobHPs = (await GetMobHPs()) || {};
    } catch {
      mobHPs = {};
    }
  }
  onMount(async () => {
    window.addEventListener("click", closeMenus);
    scheduleNoteFade(pending); // a note restored from a tab switch still fades
    admin = await IsAdminMode();
    IsOfficer().then((v) => (officer = !!v));
    loadGuides();
    await load();
    timer = setInterval(load, pollMs);
    hpTimer = setInterval(pollHP, 2000);
    // Sample raid-card previews disabled for now — re-enable by restoring
    // this block (and the sample injections in computePopped/computeInWindow).
    // if (admin) {
    //   // Put the tailed character in slot 555 so the clerics view's own-slot
    //   // treatment (gold row, "you're next" ring off 444's cast) previews on
    //   // the sample card. Without a tailed log the placeholder stays.
    //   try {
    //     const toon = ((await GetLocalRaidTimers()) || {}).toon;
    //     if (toon) {
    //       const mine = sampleCard.ch_chain.find((s) => s.label === "555");
    //       if (mine) mine.cleric = toon;
    //     }
    //   } catch {
    //     /* placeholder cleric is fine */
    //   }
    //   fireSampleChain();
    //   chainTimer = setInterval(fireSampleChain, 2500);
    // }
  });
  onDestroy(() => {
    window.removeEventListener("click", closeMenus);
    clearTimeout(noteTimer);
    clearInterval(timer);
    clearInterval(hpTimer);
    clearInterval(chainTimer);
  });

  let prevLinked;
  $: if ($linked !== prevLinked) {
    prevLinked = $linked;
    if ($linked) load();
  }

  function dotClass(m) {
    if (m.status === "in_window" && !(m.trackers && m.trackers.length))
      return "untracked";
    return m.status;
  }
  function trackerLabel(t) {
    let s = t.name || "Unknown";
    if (t.role) s += ` (${t.role})`;
    if (t.ago) s += ` · ${t.ago}`;
    return s;
  }

  // Fully-populated fictional card so admins can tweak the layout without a live raid.
  const sampleGroups = [
    {
      class: "CLR",
      members: [
        { name: "Healbot", level: 60, discord: "Bob" },
        { name: "Mendy", level: 59, discord: "Alice" },
      ],
    },
    {
      class: "WAR",
      members: [
        { name: "Tanky", level: 60, discord: "Carl" },
        { name: "Ironhide", level: 60, discord: "Dave" },
      ],
    },
    {
      class: "SHD",
      members: [{ name: "Grimtank", level: 58, discord: "Eve" }],
    },
    {
      class: "PAL",
      members: [{ name: "Lightbringer", level: 60, discord: "Frank" }],
    },
    {
      class: "MAG",
      members: [{ name: "Pewpew", level: 60, discord: "Grace" }],
    },
    {
      class: "BRD",
      members: [{ name: "Songbird", level: 60, discord: "Heidi" }],
    },
    { class: "DRU", members: [] },
    { class: "ENC", members: [{ name: "Mezzer", level: 59, discord: "Ivan" }] },
    {
      class: "MNK",
      members: [{ name: "Puncher", level: 60, discord: "Judy" }],
    },
    { class: "NEC", members: [] },
    { class: "RNG", members: [] },
    {
      class: "ROG",
      members: [{ name: "Backstab", level: 60, discord: "Ken" }],
    },
    {
      class: "SHM",
      members: [{ name: "Slower", level: 60, discord: "Laura" }],
    },
    { class: "WIZ", members: [{ name: "Nukey", level: 60, discord: "Mike" }] },
  ];
  const sampleCard = {
    target: "Lord Nagafen",
    status: "active",
    target_hp: 62,
    active_main_tank: "Tanky",
    active_ramp_tank: "Bruiser",
    sieve: 4,
    // Two adds up → the Debuffs column splits and Current Tanks shows the split.
    current_targets: [
      {
        name: "a fire giant",
        debuffs: [
          { name: "Malo", value: "Debuffa" },
          { name: "Slow", value: "Slowpoke" },
        ],
        sieve: 3,
      },
      {
        name: "King Tranix",
        debuffs: [{ name: "Tash", value: "Addler" }],
        sieve: 8,
      },
    ],
    current_tanks: ["Tanky", "Spanky"],
    tank_procs: { tanky: 12, spanky: 7, bruiser: 3 },
    main_tank_list: "Tanky, Steelskin, Ironhide",
    rampage_tank_list: "Bruiser, Basher",
    trash_tank_list: "Warddog, Meatwall",
    bump_list: "Nudge, Shove",
    fluffer_clerics: "Healbot, Mendy, Pious",
    debuffs: [
      { name: "Tash", value: "Addler" },
      { name: "Malo", value: "Debuffa" },
      { name: "OOS", value: "Sapper" },
      { name: "Slow", value: "Slowpoke" },
      { name: "ESlow", value: "Mezzer" },
      { name: "Cripple", value: "Sugarpop" },
    ],
    ch_chain: [
      { label: "111", cleric: "Cleric1", tank: "Tanky" },
      { label: "222", cleric: "Cleric2", tank: "Tanky" },
      { label: "333", cleric: "Cleric3", tank: "Tanky", dead: true },
      { label: "444", cleric: "Cleric4", tank: "Tanky" },
      // Slot 555's cleric is swapped for the tailed character at mount, so
      // the own-slot gold treatment previews here (the ring fires off 444).
      { label: "555", cleric: "Cleric5", tank: "Tanky" },
      // A greyed slot, to preview the stale treatment and its tooltip.
      {
        label: "666",
        cleric: "Cleric6",
        tank: "Tanky",
        stale: true,
        stale_why: "no cast for 2 chain cycles",
      },
      { label: "777", cleric: "Cleric7", tank: "Tanky" },
      { label: "RR1", cleric: "RampCleric1", tank: "Bruiser" },
      { label: "RR2", cleric: "RampCleric2", tank: "Bruiser" },
    ],
    loot: [
      {
        name: "Flowing Black Silk Sash",
        wiki_url: "https://wiki.project1999.com/Flowing_Black_Silk_Sash",
        price: "250 DKP · Tanky",
      },
      {
        name: "Cloak of Flames",
        wiki_url: "https://wiki.project1999.com/Cloak_of_Flames",
        price: "175 DKP · Pewpew",
      },
    ],
    raiders: { total: 14, groups: sampleGroups },
    discord_url: "https://discord.com/channels/0/0",
    // Zone is what makes the Attendance Logs button appear (the card shows it
    // whenever it has a raid to build a log for). A real zone rather than a
    // placeholder so the button behaves exactly as it does on a live card —
    // it takes a live capture and reports honestly when nobody's in there.
    zone: "Nagafen's Lair",
  };

  // ── simulated chain firing (sample card) ─────────────────────────────────
  // The main slots call in rotation every 2.5s — a realistic chain cadence
  // that keeps several of the 10s casts overlapping — and the rampage pair
  // alternates every 10s. The dead and stale slots never cast, exactly as at
  // a raid (their beats pass silently).
  // The cast bars re-read called_at_ms every animation frame, so plain
  // mutation of the sample card's slots is enough; no reactivity dance.
  let chainTimer;
  let chainIdx = 0;
  let rampIdx = 0;
  let rampAt = 0;
  function fireSampleChain() {
    const mains = sampleCard.ch_chain.filter((s) => !s.label.startsWith("RR"));
    const ramps = sampleCard.ch_chain.filter((s) => s.label.startsWith("RR"));
    const s = mains[chainIdx % mains.length];
    chainIdx++;
    if (!s.dead && !s.stale) s.called_at_ms = Date.now();
    if (ramps.length && Date.now() - rampAt >= 10000) {
      ramps[rampIdx % ramps.length].called_at_ms = Date.now();
      rampIdx++;
      rampAt = Date.now();
    }
  }

  function computePopped(d, isAdmin) {
    let list = d && d.mobs ? d.mobs.filter((m) => m.status === "popped") : [];
    // Sample raid card disabled for now.
    // if (isAdmin) {
    //   list = [
    //     {
    //       name: sampleCard.target + " (sample)",
    //       status: "popped",
    //       is_raid: true,
    //       sample: true,
    //       raid: sampleCard,
    //       trackers: [],
    //     },
    //     ...list,
    //   ];
    // }
    // Current raid always leads the popped list (server appends synthetic
    // off-board raid entries at the end, and board order is arbitrary).
    return [
      ...list.filter((m) => m.is_raid),
      ...list.filter((m) => !m.is_raid),
    ];
  }
  // Admins get two fake in-window rows so the signup menu, stop button,
  // pending notes and Learn menu are previewable in dev, where the board
  // scrape is usually dark. Sample rows never post anything (guarded in
  // signUp/stopSelf/saveGuide) and never count as "your" signup.
  function computeInWindow(d, isAdmin) {
    const list =
      d && d.mobs ? d.mobs.filter((m) => m.status === "in_window") : [];
    // Sample in-window rows disabled for now.
    return list;
    // if (!isAdmin) return list;
    // return [
    //   {
    //     name: "Lord Vyemm (sample)",
    //     status: "in_window",
    //     remaining: "6h 25m",
    //     sample: true,
    //     zone_tag: "tov",
    //     trackers: [{ name: "You", role: "coth", ago: "5 min ago", is_you: true }],
    //   },
    //   {
    //     name: "Klandicar (sample)",
    //     status: "in_window",
    //     remaining: "2h 10m",
    //     sample: true,
    //     trackers: [],
    //   },
    //   ...list,
    // ];
  }
  $: popped = computePopped(data, admin);
  $: inWindow = computeInWindow(data, admin);
  $: upcoming =
    data && data.mobs ? data.mobs.filter((m) => m.status === "upcoming") : [];
</script>

<div class="timers">
  {#if !$linked}
    <div class="empty">
      <div class="big">Link your Discord account</div>
      <div class="hint">
        You must link your Discord account to validate your Fuse membership and
        view tracking.
      </div>
      <button class="link-btn" on:click={() => activeTab.set("general")}
        >Link your account on the General tab →</button
      >
    </div>
  {:else if loading}
    <div class="empty">Loading raids…</div>
  {:else if !data || !data.verified}
    <div class="empty">
      <div class="big">Raids unavailable</div>
      <div class="hint">You could not be verified as a Fuse member.</div>
    </div>
  {:else}
    <div class="board">
      {#if data.porter}
        <div class="porter"><span class="ptag">PORTER</span> {data.porter}</div>
      {/if}
      {#if data.logistics}
        <div class="porter">
          <span class="ptag">LOGISTICS</span>
          {data.logistics}
        </div>
      {/if}
      {#if data.idol}
        <div class="porter"><span class="ptag">IDOL</span> {data.idol}</div>
      {/if}

      <!-- Popped (current raid = gold swords + expandable card; admin sample prepended) -->
      {#if popped.length || data.event_raid}
        <div class="group-title popped">
          Popped <span class="count"
            >({popped.length + (data.event_raid ? 1 : 0)})</span
          >
        </div>
        <!-- Event raid (Sky / HoT / Ring War): its own card above the mob
             list, titled by the event's "what we're doing" label. Keyed by
             event_key so collapse state survives the label flipping hours. -->
        {#if data.event_raid}
          {@const evKey = "event:" + (data.event_raid.event_key || "event")}
          <div class="mob">
            <div class="mob-head clickable" on:click={() => toggleRaid(evKey)}>
              <span class="swords" title="Event raid">⚔</span>
              <span class="mob-name raid"
                >{data.event_raid.label || "Event Raid"}</span
              >
              <span class="chev chev-auto"
                >{(openRaid[evKey] ?? true) ? "▾" : "▸"}</span
              >
            </div>
            <div class:collapsed={!(openRaid[evKey] ?? true)}>
              <RaidCardView card={data.event_raid} />
            </div>
          </div>
        {/if}
        {#each popped as m (m.name)}
          {@const shownTrackers = displayTrackers(m, pending, stoppedOther)}
          <div class="mob">
            <div
              class="mob-head"
              class:clickable={m.is_raid}
              on:click={() => {
                if (m.is_raid) toggleRaid(m.name);
              }}
            >
              {#if m.is_raid}
                <span class="swords" title="Current raid">⚔</span>
              {:else}
                <span class="dot {dotClass(m)}"></span>
              {/if}
              <span class="mob-name" class:raid={m.is_raid}>{m.name}</span>
              {#if !m.sample}
                <LearnMenu
                  mob={m.name}
                  zoneTag={m.zone_tag || ""}
                  {guides}
                  onOpen={openGuide}
                  onAdd={offerAddGuide}
                  onEdit={editGuide}
                />
              {/if}
              {#if m.is_raid}<span class="chev chev-auto"
                  >{(openRaid[m.name] ?? true) ? "▾" : "▸"}</span
                >{/if}
            </div>
            {#if m.is_raid && m.raid}
              <div class:collapsed={!(openRaid[m.name] ?? true)}>
                <RaidCardView card={m.raid} liveHP={raidLiveHP(mobHPs, m)} />
              </div>
            {:else if !m.is_raid && m.detail}
              <div class="mob-detail">{m.detail}</div>
            {/if}
            {#if shownTrackers.length}
              <div class="mob-trackers">
                {#each shownTrackers as t, i}{i > 0 ? ", " : ""}<span
                    class="trk-name"
                    class:you={t.is_you}
                    role="button"
                    tabindex="-1"
                    on:contextmenu={(e) => trackerMenu(e, m, t)}
                    >{trackerLabel(t)}</span
                  >{/each}
              </div>
            {/if}
          </div>
        {/each}
      {/if}

      <!-- Completed raids (last 2 hours) -->
      {#if data.completed_raids && data.completed_raids.length}
        <div class="group-title completed">
          Completed Raids <span class="count"
            >({data.completed_raids.length})</span
          >
        </div>
        {#each data.completed_raids as r, i}
          <div class="mob">
            <div class="mob-head clickable" on:click={() => toggleCompleted(i)}>
              <span class="dot completedDot"></span>
              <span class="mob-name">{r.target}</span>
              {#if r.killed_ago}<span class="remaining"
                  >killed {r.killed_ago}</span
                >{/if}
              <span class="chev">{completedOpen[i] ? "▾" : "▸"}</span>
            </div>
            {#if completedOpen[i]}
              <RaidCardView card={r} />
            {/if}
          </div>
        {/each}
      {/if}

      <!-- In Window -->
      {#if inWindow.length}
        <div class="group-title in_window">
          In Window <span class="count">({inWindow.length})</span>
        </div>
        {#each inWindow as m}
          {@const shownTrackers = displayTrackers(m, pending, stoppedOther)}
          <div class="mob">
            <div class="mob-head">
              <span class="dot {dotClass(m)}"></span>
              <span class="mob-name">{m.name}</span>
              <span
                class="hov-wrap"
                class:show={isYouShown(m, stopPending) ||
                  pendingStartOn(pending, m)}
              >
                <span
                  class="hov-trigger"
                  class:on={isYouShown(m, stopPending) ||
                    pendingStartOn(pending, m)}
                >
                  {#if isYouShown(m, stopPending)}{myRole(m)} ✓{:else if pendingStartOn(pending, m)}{pending.role}
                    …{:else}+ sign up{/if}
                </span>
                <div class="hov-menu">
                  <div class="hm-body">
                    {#if isYouShown(m, stopPending) || pendingStartOn(pending, m)}
                      <!-- Already doing a role here (or zone-wide): the only
                           sensible action is signing out. -->
                      <button
                        class="hm-item stop"
                        disabled={inflight}
                        on:click|stopPropagation={() => stopSelf(m)}
                        >stop tracking</button
                      >
                    {:else}
                      {#each ROLES as role}
                        <button
                          class="hm-item"
                          disabled={inflight}
                          on:click|stopPropagation={() => signUp(m, role)}
                          >{role}</button
                        >
                      {/each}
                    {/if}
                  </div>
                </div>
              </span>
              <LearnMenu
                mob={m.name}
                zoneTag={m.zone_tag || ""}
                {guides}
                onOpen={openGuide}
                onAdd={offerAddGuide}
                onEdit={editGuide}
              />
              {#if m.remaining}<span class="remaining"
                  >{m.remaining} remaining</span
                >{/if}
            </div>
            {#if shownTrackers.length}
              <div class="mob-trackers">
                {#each shownTrackers as t, i}{i > 0 ? ", " : ""}<span
                    class="trk-name"
                    class:you={t.is_you}
                    role="button"
                    tabindex="-1"
                    on:contextmenu={(e) => trackerMenu(e, m, t)}
                    >{trackerLabel(t)}</span
                  >{/each}
              </div>
            {/if}
            {#if pending && pending.mob === m.name && !pending.noteHidden}
              <div
                class="pending-note"
                class:err={pending.state === "error"}
                out:fade|local={{ duration: 400 }}
              >
                {#if pending.state === "inflight"}
                  {pending.kind === "stop"
                    ? "stopping…"
                    : `signing up (${pending.role}${
                        pending.zone ? ` · ${pending.zone} zone-wide` : ""
                      })…`}
                {:else}
                  {pending.msg}
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      {/if}

      <!-- Upcoming -->
      {#if upcoming.length}
        <div class="group-title upcoming">
          Upcoming <span class="count">({upcoming.length})</span>
        </div>
        {#each upcoming as m}
          <div class="mob">
            <div class="mob-head">
              <span class="dot {dotClass(m)}"></span>
              <span class="mob-name">{m.name}</span>
              <LearnMenu
                mob={m.name}
                zoneTag={m.zone_tag || ""}
                {guides}
                onOpen={openGuide}
                onAdd={offerAddGuide}
                onEdit={editGuide}
              />
            </div>
            {#if m.detail}<div class="mob-detail">{m.detail}</div>{/if}
          </div>
        {/each}
      {/if}

      {#if !popped.length && !inWindow.length && !upcoming.length && !(data.completed_raids && data.completed_raids.length)}
        <div class="empty">No timers reported</div>
      {/if}
    </div>

    <div class="footer">
      {#if data.summary}<span>{data.summary}</span>{/if}
    </div>
  {/if}
</div>

<!-- Officer: stop someone's tracker (right-click on a tracker name) -->
{#if trackerCtx}
  <div class="ctx-menu" style="left:{trackerCtx.x}px;top:{trackerCtx.y}px">
    <button class="ctx-item" on:click={stopOther}
      >Stop {trackerCtx.name}'s tracker</button
    >
  </div>
{/if}

<!-- FTE in a zone-grouped zone: this mob, or the whole zone? -->
{#if fteChoice}
  <div class="gm-back">
    <div class="gm">
      <div class="gm-title">FTE — {fteChoice.m.name}</div>
      <div class="gm-warn">
        {fteChoice.m.name} is in a zone-grouped tracking zone ({fteChoice.m
          .zone_tag}). Sign up FTE for just this mob, or for the whole zone?
      </div>
      <div class="gm-btns">
        <button class="gm-btn" on:click={() => (fteChoice = null)}
          >Cancel</button
        >
        <button
          class="gm-btn save"
          on:click={() => doSignUp(fteChoice.m, "fte", fteChoice.m.name)}
          >Just {fteChoice.m.name}</button
        >
        <button
          class="gm-btn save"
          on:click={() => doSignUp(fteChoice.m, "fte", fteChoice.m.zone_tag)}
          >Whole zone ({fteChoice.m.zone_tag})</button
        >
      </div>
    </div>
  </div>
{/if}

<!-- In-app guide viewer -->
{#if guideView}
  <div class="gm-back">
    <div class="gm gv">
      <div class="gv-head">
        <div class="gm-title">{guideView.label || `${guideView.key} ${guideView.role}`}</div>
        <button class="gv-x" on:click={() => (guideView = null)}>×</button>
      </div>
      {#if guideView.loading}
        <div class="gv-note">Loading guide post…</div>
      {:else if guideView.err}
        <div class="gm-err">{guideView.err}</div>
        <div class="gm-btns">
          <button
            class="gm-btn"
            on:click={() => {
              const gv = guideView;
              guideView = null;
              editGuide(gv.key, gv.role, "");
            }}>Replace link</button
          >
        </div>
      {:else if guideView.content}
        <div class="gv-meta">
          {#if guideView.content.author}posted by {guideView.content
              .author}{/if}
          {#if guideView.content.posted_at}
            · {guideView.content.posted_at}{/if}
        </div>
        <div class="gv-body">
          <DiscordMessage
            text={guideView.content.text}
            media={guideView.content.media || []}
          />
        </div>
        <div class="gm-btns">
          {#if guideView.content.url}
            <a
              class="gm-btn gv-src"
              href={guideView.content.url}
              target="_blank"
              rel="noreferrer">Open in Discord ↗</a
            >
          {/if}
          <button class="gm-btn" on:click={() => (guideView = null)}
            >Close</button
          >
        </div>
      {/if}
    </div>
  </div>
{/if}

<!-- Add / replace a role guide link -->
{#if guideModal}
  <div class="gm-back">
    <div class="gm">
      <div class="gm-title">
        {guideModal.existing ? "Replace" : "Add"} guide — {guideModal.role} on {guideModal.mob}
      </div>
      <div class="gm-warn">
        Only <b>one</b> link is saved per mob/role pairing — make sure this
        post has all of the appropriate information, video included, in a
        single Discord post. Right-click the post in Discord and choose
        <b>Copy Message Link</b>.
      </div>
      <input
        class="gm-input"
        bind:value={guideModal.url}
        placeholder="https://discord.com/channels/server/channel/message"
        spellcheck="false"
        on:keydown={(e) => e.key === "Enter" && saveGuide()}
      />
      {#if guideModal.err}<div class="gm-err">{guideModal.err}</div>{/if}
      <div class="gm-btns">
        <button class="gm-btn" on:click={() => (guideModal = null)}
          >Cancel</button
        >
        <button
          class="gm-btn save"
          disabled={guideModal.saving}
          on:click={saveGuide}
          >{guideModal.saving ? "Saving…" : "Save link"}</button
        >
      </div>
    </div>
  </div>
{/if}

<style>
  .timers {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }
  .board {
    flex: 1;
    overflow-y: auto;
    padding: 10px 14px;
  }

  .porter {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 10px;
    margin-bottom: 12px;
    font-size: 12px;
    color: var(--text-secondary);
  }
  .ptag {
    color: var(--accent);
    font-weight: 700;
    font-size: 10px;
    letter-spacing: 0.06em;
    margin-right: 6px;
  }

  .group-title {
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin: 14px 0 6px;
    color: var(--text-muted);
  }
  .group-title.popped {
    color: #ff7a7a;
  }
  .group-title.in_window {
    color: #3fb950;
  }
  .group-title.upcoming {
    color: var(--text-muted);
  }
  .group-title.completed {
    color: var(--success);
  }
  .group-title .count {
    font-weight: 400;
  }

  .mob {
    padding: 5px 0 6px;
    border-bottom: 1px solid var(--border);
  }
  .mob:last-child {
    border-bottom: none;
  }
  .mob-head {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .mob-head.clickable {
    cursor: pointer;
  }
  .mob-name {
    color: var(--text-primary);
    font-size: 13px;
    font-weight: 600;
  }
  .mob-name.raid {
    color: #e3a008;
  }
  .remaining {
    margin-left: auto;
    color: var(--text-secondary);
    font-size: 12px;
    white-space: nowrap;
  }
  .chev {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: 8px;
  }
  .chev-auto {
    margin-left: auto;
  }
  .collapsed {
    display: none;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .dot.popped {
    background: #ff5555;
  }
  .dot.in_window {
    background: #3fb950;
  }
  .dot.untracked {
    background: #ff5555;
  }
  .dot.upcoming {
    background: var(--text-muted);
  }
  .dot.completedDot {
    background: var(--success);
  }
  .swords {
    color: #e3a008;
    font-size: 14px;
    line-height: 1;
    flex-shrink: 0;
  }

  .mob-detail {
    color: var(--text-secondary);
    font-size: 12px;
    margin: 1px 0 0 15px;
  }
  .mob-trackers {
    color: var(--text-muted);
    font-size: 11px;
    font-style: italic;
    margin: 1px 0 0 15px;
  }
  .trk-name.you {
    color: var(--accent);
  }

  /* ── signup hover menu (quiet trigger on row hover; menu expands on
     trigger hover — a padding bridge, never a gap, so the cursor can travel
     into it without the menu closing) ─────────────────────────────────── */
  .hov-wrap {
    position: relative;
    display: none;
    margin-left: 2px;
  }
  .mob-head:hover .hov-wrap,
  .hov-wrap:hover,
  .hov-wrap.show {
    display: inline-block;
  }
  .hov-trigger {
    display: inline-block;
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-muted);
    font-size: 9px;
    font-weight: 600;
    /* Tight line box so the chip stays shorter than the 13px mob-name line —
       otherwise its reveal grows the row by a pixel on hover. */
    line-height: 1;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    white-space: nowrap;
  }
  .hov-wrap:hover .hov-trigger {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  .hov-trigger.on {
    color: var(--accent);
    border-color: var(--accent-dim);
  }
  .hov-menu {
    display: none;
    position: absolute;
    left: 0;
    top: 100%;
    padding-top: 3px;
    z-index: 30;
  }
  .hov-wrap:hover .hov-menu {
    display: block;
  }
  .hm-body {
    background: var(--bg-secondary);
    border: 1px solid var(--border-hover);
    border-radius: 4px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.6);
    padding: 3px 0;
    min-width: 130px;
  }
  .hm-item {
    display: block;
    width: 100%;
    text-align: left;
    background: transparent;
    border: none;
    color: var(--text-secondary);
    font-size: 11px;
    padding: 5px 12px;
    cursor: pointer;
    text-transform: capitalize;
    font-family: inherit;
    white-space: nowrap;
  }
  .hm-item:hover {
    background: rgba(200, 169, 81, 0.1);
    color: var(--accent);
  }
  .hm-item:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .hm-item.stop {
    color: #ff5555;
    text-transform: none;
  }
  .hm-item.stop:hover {
    background: rgba(255, 85, 85, 0.08);
    color: #ff7a7a;
  }
  .hm-div {
    height: 1px;
    background: var(--border);
    margin: 3px 0;
  }

  .pending-note {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--accent);
    font-size: 11px;
    font-style: italic;
    margin: 2px 0 0 15px;
  }
  .pending-note.err {
    color: #ff7a7a;
  }

  /* officer right-click menu */
  .ctx-menu {
    position: fixed;
    background: var(--bg-secondary);
    border: 1px solid var(--border-hover);
    border-radius: 4px;
    padding: 4px 0;
    z-index: 1000;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.6);
    min-width: 160px;
  }
  .ctx-item {
    display: block;
    width: 100%;
    text-align: left;
    background: transparent;
    border: none;
    color: var(--text-secondary);
    font-size: 12px;
    padding: 6px 14px;
    cursor: pointer;
    font-family: inherit;
  }
  .ctx-item:hover {
    background: rgba(200, 169, 81, 0.1);
    color: var(--accent);
  }

  /* guide add/replace modal */
  .gm-back {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    z-index: 1100;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .gm {
    background: var(--bg-secondary);
    border: 1px solid var(--border-hover);
    border-radius: 6px;
    padding: 14px 16px;
    width: 420px;
    max-width: 90%;
    display: flex;
    flex-direction: column;
    gap: 8px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.7);
  }
  .gm-title {
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--accent);
  }
  .gm-warn {
    font-size: 11.5px;
    color: var(--text-secondary);
    line-height: 1.5;
    background: rgba(200, 169, 81, 0.07);
    border: 1px solid rgba(200, 169, 81, 0.25);
    border-radius: 4px;
    padding: 7px 9px;
  }
  .gm-input {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 12px;
    padding: 6px 8px;
    font-family: var(--font-mono);
  }
  .gm-input:focus {
    outline: none;
    border-color: var(--accent-dim);
  }
  .gm-err {
    color: #ff7a7a;
    font-size: 11px;
  }
  .gm-btns {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .gm-btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-secondary);
    font-size: 12px;
    padding: 5px 12px;
    cursor: pointer;
  }
  .gm-btn:hover {
    border-color: var(--border-hover);
    color: var(--text-primary);
  }
  .gm-btn.save {
    border-color: var(--accent-dim);
    color: var(--accent);
  }
  .gm-btn.save:disabled {
    opacity: 0.5;
    cursor: default;
  }

  /* guide viewer (larger, scrollable body) */
  .gm.gv {
    width: 560px;
    max-height: 80vh;
  }
  .gv-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .gv-x {
    background: transparent;
    border: none;
    color: var(--text-muted);
    font-size: 16px;
    cursor: pointer;
    padding: 0 2px;
    line-height: 1;
  }
  .gv-x:hover {
    color: var(--text-primary);
  }
  .gv-meta {
    font-size: 11px;
    color: var(--text-muted);
    font-style: italic;
  }
  .gv-body {
    overflow-y: auto;
    min-height: 40px;
    padding-right: 4px;
  }
  .gv-note {
    color: var(--text-muted);
    font-size: 12px;
    font-style: italic;
    padding: 12px 0;
  }
  .gv-src {
    margin-right: auto;
    text-decoration: none;
    display: inline-flex;
    align-items: center;
  }

  .footer {
    flex-shrink: 0;
    display: flex;
    justify-content: space-between;
    gap: 10px;
    padding: 6px 14px;
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 11px;
  }
  .upd {
    white-space: nowrap;
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    height: 100%;
    gap: 6px;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
  .empty .big {
    color: var(--text-secondary);
    font-size: 15px;
    font-weight: 600;
  }
  .empty .hint {
    font-size: 12px;
    max-width: 340px;
    line-height: 1.5;
  }
  .link-btn {
    margin-top: 8px;
    background: var(--bg-panel);
    border: 1px solid var(--accent);
    color: var(--accent);
    border-radius: 4px;
    cursor: pointer;
    font-size: 12px;
    padding: 6px 14px;
    transition: background 0.15s;
  }
  .link-btn:hover {
    background: var(--bg-input);
  }
</style>
