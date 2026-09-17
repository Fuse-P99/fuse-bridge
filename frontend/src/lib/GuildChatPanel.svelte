<script>
  // The Guild Chat panel: the live #guild-stream — every guild message, the
  // raid-mob engage lines, and the world messages (GM broadcasts, server
  // announcements, quakes), as the server already relays them to Discord. It
  // fills the collapsible box GuildChatDock.svelte slides up out of the app
  // footer on every tab, and it opens to every linked member (the guild's own
  // chat is not officer business).
  //
  // Nothing is parsed here, but not everything is the server's either: Go reads
  // this client's own guild lines out of the log and shows them at once as
  // PENDING rows, then merges the server's copy — identity, officer/bot marks,
  // item names — onto the same row when it arrives a second or three later
  // (guildfeed.go). This component renders that one merged view and asks for it
  // by version, so a quiet chat costs an integer comparison.
  //
  // Because the server's half only fills a row in, the message must never move
  // under the reader when it lands. So the speaker's identity is not written on
  // the line at all: it lives in the hover card on the toon name, which starts
  // answering the moment the row is confirmed. The only thing drawn inline is
  // the officer/bot mark, and it has a reserved slot between the time and the
  // toon — held open from the row's first frame, so the mark slides into it
  // without moving a character of the message.
  //
  // It reads like the game's own chat window: newest line at the BOTTOM, scroll
  // up for older ones, and the box follows the conversation as lines arrive.
  // "Newest first" (newestTop) flips it, and the follow goes with it: the live
  // edge is whichever end the newest line is at.
  //
  // The feed is a POOLED thing — it exists because installs forward what they
  // hear — so a client that has switched its forwarding off is not shown it.
  // The gate below reads the five Forwarded Messages toggles guild chat rides
  // on and, with any of them off, renders a note instead of ever asking Go for
  // the feed (which is also what keeps Go's server poller from starting).
  import { createEventDispatcher, onMount, onDestroy, tick } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { fly } from "svelte/transition";
  import {
    GetGuildFeed,
    GetSettings,
    LookupItems,
    WhoHasItem,
    GetMemberInfo,
  } from "../../bindings/FuseBridge/app.js";
  import Icon from "./Icon.svelte";
  import ItemTipCard from "./ItemTipCard.svelte";
  import MemberTipCard from "./MemberTipCard.svelte";
  import { scale } from "./scale.js";
  import { FWD_OPTIONS } from "./settingsMeta.js";
  import { openGeneralTab } from "./nav.js";
  import { chatLive, chatSnapSeq } from "./chatDock.js";

  // Reading order, from the gear popover in the dock's divider row
  // (GuildChatDock.svelte owns it and its storage).
  export let newestTop = false;
  // The gear popover's text style, owned and persisted by the dock. Font, bold
  // and color dress the MESSAGE alone, so the time, the mark, the toon and the
  // colon keep the panel's own look and the columns stay a readable, uniform
  // ledger no matter what the conversation is dressed in. Size is the
  // exception — it is set on the whole ROW, so asking for bigger chat gives
  // bigger chat rather than a large message beside a tiny clock. Each
  // empty/zero/false value means "inherit".
  export let msgFont = "";
  export let msgBold = false;
  export let msgSize = 0;
  export let msgColor = "";

  // "New lines arrived while the reader was scrolled away" — the dock pulses
  // the header on it, which is the only way to say so without moving the page
  // out from under someone who is reading history.
  const dispatch = createEventDispatcher();

  // The merged view as Go holds it: oldest first (local rows and server rows
  // alike arrive in that order), rendered in that order unless newestTop
  // reverses them below. Replaced wholesale on every change — Go owns the
  // capping, the deduplication and the local/server merge, so there is nothing
  // to reconcile here.
  let entries = [];
  let version = 0; // the view version last rendered; Go answers "unchanged"
  let el; // the scroll container, so new lines can keep it pinned to the bottom
  // Has the box ever been filled? The first backfill has to land at the newest
  // line rather than at the oldest one the server still holds.
  let filledOnce = false;
  // Assume yes until told otherwise, so the panel doesn't flash its refusal
  // note on its first frame.
  let allowed = true;
  let seenAny = false; // a server answer, or a line of our own, has arrived
  let timer;
  let off; // the change-event unsubscribe
  let refreshing = false,
    refreshAgain = false;

  // ── forwarding gate ─────────────────────────────────────────────────────
  // The five toggles guild chat rides on, in the order the General tab lists
  // them. Taken from the shared metadata rather than retyped, so the note
  // names the checkboxes by exactly the labels the user sees there.
  const FWD_NEEDED = FWD_OPTIONS.slice(0, 5);
  const FWD_LABELS = FWD_NEEDED.map((o) => o.label);
  const FWD_LIST =
    FWD_LABELS.slice(0, -1).join(", ") +
    " and " +
    FWD_LABELS[FWD_LABELS.length - 1];
  let gated = false;

  // checkGate runs before anything else asks Go for the feed, so a gated
  // install never starts the server poller at all. It runs once per mount, and
  // the dock mounts this component on each expand of the box, so a toggle
  // flipped on the General tab is picked up the next time the box is opened
  // without anything having to watch for it.
  async function checkGate() {
    try {
      const s = await GetSettings();
      gated = FWD_NEEDED.some((o) => !s[o.key]);
    } catch {
      // A settings read that failed says nothing about the user's choices, so
      // it must not produce the note. Carry on and let the feed answer.
      gated = false;
    }
  }

  async function refresh() {
    if (refreshing) {
      refreshAgain = true;
      return;
    }
    refreshing = true;
    try {
      const r = await GetGuildFeed(version);
      if (r) {
        // Carried on every answer, changed view or not, so an install that
        // links mid-session stops being told to link without a line having to
        // arrive first.
        allowed = !!r.allowed;
        if (r.ok) seenAny = true;
        if (r.changed) {
          // Where the reader is sitting, measured BEFORE the new lines land.
          const wasAtEdge = atLiveEdge();
          const firstFill = !filledOnce;
          // The newest row's id, so "did this refresh ADD anything?" survives
          // the two cases a length comparison gets wrong: a pending row that
          // merely gains its server half (same length, nothing new to read)
          // and a view already at Go's 500-row cap, where every new line
          // drops an old one and the length never moves.
          const wasNewest = newestID(entries);
          entries = r.entries || [];
          version = r.version;
          if (entries.length) {
            seenAny = true;
            filledOnce = true;
            // Two reasons to follow the conversation, and one to stay put.
            // Follow when the reader was already at the live edge (they are
            // reading it live), and when this is the first fill (the box
            // should open on the newest line). Otherwise leave the scroll
            // alone: someone who scrolled away to read history must not be
            // yanked back by every line that arrives. They are told instead —
            // the header pulses on this event — since nothing on screen would
            // otherwise say the conversation had moved on.
            if (wasAtEdge || firstFill) await snapToEdge();
            else if (newestID(entries) !== wasNewest) dispatch("newlines");
          }
        }
      }
    } catch {
      /* keep last */
    }
    refreshing = false;
    if (refreshAgain) {
      refreshAgain = false;
      refresh();
    }
  }

  // Go hands the view over oldest-first whichever way it is rendered, so the
  // newest row is the last one. 0 for an empty view.
  function newestID(list) {
    return list.length ? list[list.length - 1].id : 0;
  }

  // The LIVE EDGE is the end the newest line lands at: the bottom in reading
  // order, the top under "Newest first". Within 24px of it counts as being
  // there — the edge row is often part-cut and the browser's scroll numbers
  // are fractional.
  function atLiveEdge() {
    if (!el) return true;
    return newestTop
      ? el.scrollTop < 24
      : el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  }

  // snapToEdge pins the box to that edge, after Svelte has put the new rows in
  // the DOM (tick) so the scroll extent it measures is the real one.
  async function snapToEdge() {
    await tick();
    if (!el) return;
    el.scrollTop = newestTop ? 0 : el.scrollHeight;
    chatLive.set(true);
  }

  // Whether the reader is at the live edge is published continuously, because
  // it is not only this panel's business: the footer opener says so while they
  // are scrolled away, and that is the only thing on screen that can, since the
  // panel deliberately never moves under them. The browser's own scroll event
  // is the trigger — cheap, and it fires for a wheel, a drag and a keystroke
  // alike.
  function onScroll() {
    chatLive.set(atLiveEdge());
  }
  // The footer's "jump back to live" request (requestChatSnap).
  let snapSeen = $chatSnapSeq;
  $: if ($chatSnapSeq !== snapSeen) {
    snapSeen = $chatSnapSeq;
    snapToEdge();
  }

  onMount(async () => {
    // The gate first, and nothing else if it holds: a gated install must not
    // call GetGuildFeed at all, since asking is what starts Go's poller.
    await checkGate();
    if (gated) return;
    await refresh();
    // The event is the fast path — Go fires it the moment a line of ours is
    // logged or the server's half merges. The interval is a safety net for the
    // one case an event can't cover (a dropped event while the panel was being
    // built), and asking for the view is also what keeps Go's server poller
    // alive, so it must stay comfortably inside that poller's idle timeout.
    off = Events.On("guildchat-changed", refresh);
    timer = setInterval(refresh, 2000);
  });
  onDestroy(() => {
    clearInterval(timer);
    if (off) off();
    hideCards();
    // Nobody is scrolled away from a panel that no longer exists.
    chatLive.set(true);
  });

  // ── hover cards ─────────────────────────────────────────────────────────
  // Drawn INLINE in this window, the way the raid loot cards are (see
  // RaidCardView.svelte): the panel lives inside the app shell, so a card
  // positioned at the cursor has the whole window to sit in and needs no
  // separate transparent window of its own.
  const CARD_W = 260; // both cards' fixed width (ItemTipCard/MemberTipCard)
  const CARD_PAD = 14;
  // How much room a card wants BELOW the cursor before it is drawn downward.
  // Only a threshold, not a measurement: a card that opens upward is anchored
  // by its bottom edge (below), so the number decides which way it opens and
  // nothing else. Pre-zoom px, like every other size here.
  const CARD_MIN_BELOW = 200;

  // cardPos returns the inline position for the card's WRAPPER, as a pair of
  // edges rather than a top-left corner. That is the whole trick: this panel
  // sits at the bottom of the window, so a card nearly always opens upward, and
  // a "top" computed from a guessed card height put a 60px member card 320px
  // above the cursor. Anchoring the wrapper's BOTTOM edge instead means the
  // card's real height decides where its top lands, whatever is in it.
  // Horizontally either edge is exact, since both cards are a known 260px wide.
  // The shell is CSS-zoomed (lib/scale.js), so the cursor's screen coordinates
  // divide by the UI scale or the card drifts at Medium/Large.
  function cardPos(ev) {
    const z = $scale || 1;
    const vw = window.innerWidth / z;
    const vh = window.innerHeight / z;
    const cx = ev.clientX / z;
    const cy = ev.clientY / z;
    const h =
      cx + CARD_PAD + CARD_W > vw
        ? `right:${Math.max(0, vw - cx + CARD_PAD)}px`
        : `left:${Math.max(0, cx + CARD_PAD)}px`;
    const v =
      vh - cy < CARD_MIN_BELOW
        ? `bottom:${Math.max(0, vh - cy + CARD_PAD)}px`
        : `top:${Math.max(0, cy + CARD_PAD)}px`;
    return `${h};${v}`;
  }

  // Item records never change, so they are cached for the life of the session.
  // undefined = never looked up; null = looked up and not in the item DB.
  let itemCache = {};
  let tip = null; // { name, item, pos } — pos is the wrapper's edge style

  // "Also held by" — the user's own characters holding the hovered item
  // (local inventory dumps only; never guildmates).
  let tipHolders = [];
  let tipHoldersFor = "";
  $: tipItemName = tip ? tip.name : "";
  $: if (tipItemName !== tipHoldersFor) loadTipHolders(tipItemName);
  async function loadTipHolders(name) {
    tipHoldersFor = name;
    tipHolders = [];
    if (!name) return;
    try {
      const hits = (await WhoHasItem(name)) || [];
      if (tipHoldersFor === name) tipHolders = hits;
    } catch {
      /* the footer is a bonus — a failed lookup shows nothing */
    }
  }

  async function showItem(ev, name) {
    hideMember();
    const key = name.toLowerCase();
    tip = { name, item: itemCache[key] ?? null, pos: cardPos(ev) };
    if (itemCache[key] === undefined) {
      try {
        const res = await LookupItems([name]);
        itemCache[key] = (res && res.items && res.items[key]) || null;
      } catch {
        itemCache[key] = null;
      }
      // Only adopt the result if the cursor is still on the same item.
      if (tip && tip.name === name) tip = { ...tip, item: itemCache[key] };
    }
  }
  function hideItem() {
    tip = null;
  }

  // A member lookup costs the server a Discord avatar fetch, so it is cached
  // too — but only briefly, since DKP and attendance move.
  const MEMBER_TTL = 5 * 60 * 1000;
  const memberCache = new Map(); // lower(query) → { at, info }
  let mtip = null; // { q, info, loading, pos }
  // One member hover at a time: every request bumps this, and a lookup that
  // lands after the cursor has moved on drops its own result.
  let mseq = 0;

  // The toon's hover card resolves the way Discord's /who does: the handle is
  // unique, and a toon with no handle resolves to whoever owns it. With
  // neither there is nothing to look up.
  async function showMember(ev, e) {
    // A pending row is one the server hasn't answered for: nothing is known
    // about its speaker yet, so there is no card to draw. The hover starts
    // working on its own the moment the answer merges onto the row — which is
    // also when the toon's dotted underline appears (class:gc-hover).
    if (e.pending) return;
    const q = (e.handle || e.toon || "").trim();
    if (!q) return;
    hideItem();
    const s = ++mseq;
    const key = q.toLowerCase();
    const pos = cardPos(ev);
    const hit = memberCache.get(key);
    if (hit && Date.now() - hit.at < MEMBER_TTL) {
      mtip = { q, info: hit.info, loading: false, pos };
      return;
    }
    // "Looking up…" rather than a blank card while the request is in flight.
    mtip = { q, info: null, loading: true, pos };
    let info = null;
    try {
      info = (await GetMemberInfo(q)) || null;
      // The server answers for an unknown name with an empty record; that is
      // "not a member", not a card with blanks in it.
      if (info && !info.handle && !info.display_name) info = null;
    } catch {
      /* unknown, unrostered, or the server is unreachable */
    }
    memberCache.set(key, { at: Date.now(), info });
    if (s !== mseq || !mtip) return;
    mtip = { ...mtip, info, loading: false };
  }
  function hideMember() {
    mseq++; // a lookup still in flight must not re-open the card
    mtip = null;
  }
  function hideCards() {
    hideItem();
    hideMember();
  }

  // segments splits a message into plain runs and item-name runs, so the
  // markup can underline the items without {@html} — the text is other
  // people's and never goes near innerHTML.
  //
  // Matching is exact and case-sensitive (the server found these names in
  // this very text), and longest-first so "Cloak of Flames" claims the line
  // before a shorter "Cloak" can split it in half.
  function segments(text, items) {
    const t = text || "";
    if (!items || !items.length) return [{ t, item: null }];
    const names = [...new Set(items.filter(Boolean))].sort(
      (a, b) => b.length - a.length,
    );
    const out = [];
    let plain = "";
    let i = 0;
    outer: while (i < t.length) {
      for (const n of names) {
        if (t.startsWith(n, i)) {
          if (plain) {
            out.push({ t: plain, item: null });
            plain = "";
          }
          out.push({ t: n, item: n });
          i += n.length;
          continue outer;
        }
      }
      plain += t[i];
      i++;
    }
    if (plain) out.push({ t: plain, item: null });
    return out;
  }

  // Flipping the order moves the live edge to the other end of the box, so go
  // there: the reader asked to see the newest line, not to keep the pixel
  // offset they happened to be sitting at.
  $: newestTop, snapToEdge();

  // Lines nobody in the guild said: the world-wide messages every install
  // forwards and the bot posts to #guild-stream alongside the chat. They have
  // no speaker, so the mark slot and the toon are replaced by one short tag —
  // the label, and the row's colour with it, is what says where the line came
  // from. Item underlines belong to guild chat only, so these render as plain
  // text.
  const TAGS = { gm: "GM", server: "SERVER", quake: "QUAKE" };

  // Reading order. The default is the game's: oldest first, so the newest line
  // sits at the bottom and older ones are above it. "Newest first" flips that.
  // Every line Go hands over is rendered — the engage lines are part of the
  // conversation, not a view someone has to switch on.
  $: rows = newestTop ? [...entries].reverse() : entries;

  // Text size dresses the whole ROW, so the time, the mark slot and the toon
  // grow and shrink with the message they belong to — the row is one line of
  // chat, not a message with furniture around it. Everything left of the
  // message sizes in em (.gc-time, .gc-mark), so this one declaration scales
  // all of it. Unset (0) leaves the panel's own size in charge. Authored in
  // CSS px, like every other size here: the shell's UI scale multiplies on top
  // of it (lib/scale.js), so a bigger UI and a bigger chat font compose
  // instead of fighting.
  $: rowStyle = msgSize > 0 ? `font-size:${msgSize}px;` : "";

  // Message-text style, assembled from only the settings that are actually
  // set: an empty inline style leaves the stylesheet in charge. Two versions,
  // because an engage line's gold is the point of the engage line — the font
  // and the weight dress it like the rest of the chat, but a custom message
  // color must not repaint it.
  $: msgStyleBase =
    (msgFont ? `font-family:${msgFont};` : "") +
    (msgBold ? "font-weight:700;" : "");
  $: msgStyle = msgColor ? `${msgStyleBase}color:${msgColor};` : msgStyleBase;

  // HH:MM AM/PM in LOCAL time, built by hand rather than with
  // toLocaleTimeString: a machine set to a 24-hour locale would otherwise turn
  // this into 21:31, and the format is meant to match what Discord shows.
  function fmtTime(ms) {
    const d = new Date(ms);
    let h = d.getHours();
    const ampm = h >= 12 ? "PM" : "AM";
    h = h % 12 || 12;
    return `${String(h).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")} ${ampm}`;
  }

  // What the empty box says. The refusal belongs on screen only while there is
  // nothing to show: an unlinked install still sees the lines read from its
  // OWN log, which are its own chat rather than the guild's stream.
  // "Connecting…" holds until either half has produced anything at all.
  $: emptyNote = !allowed
    ? "Guild chat needs a linked account."
    : seenAny
      ? "No guild chat yet"
      : "Connecting…";
</script>

<div class="gc" class:gc-gated={gated} bind:this={el} on:scroll={onScroll}>
  {#if gated}
    <!-- The feed is pooled: an install that has stopped forwarding is not
         shown other people's lines. The five names come from the same
         metadata the General tab labels its checkboxes with, and the link
         lands on that section rather than just on the tab. -->
    <div class="gc-gate">
      Guild chat relies on the messages your client shares back to the server.
      Enable {FWD_LIST} under <button
        class="gc-link"
        on:click={() => openGeneralTab({ section: "forwarded" })}
        >Forwarded Messages on the General tab</button
      >.
    </div>
  {:else if !rows.length}
    <div class="gc-note">{emptyNote}</div>
  {:else}
    <div class="gc-lines" class:bottom={!newestTop}>
      {#each rows as e (e.id)}
        <div
          class="gc-row"
          class:gc-engage={e.kind === "engage"}
          class:gc-gm={e.kind === "gm"}
          class:gc-server={e.kind === "server"}
          class:gc-quake={e.kind === "quake"}
          class:pending={e.pending}
          style={rowStyle}
        >
          <div class="gc-body">
            <span class="gc-time">{fmtTime(e.at_ms)}</span>
            {#if TAGS[e.kind]}
              <!-- A world message: one tag where the mark and the toon would
                   be, and the line itself after it. The message keeps the
                   base style only (family and weight) — a custom message
                   colour must not repaint a row whose colour is the point,
                   exactly as it must not repaint an engage. -->
              <span class="gc-tag">{TAGS[e.kind]}</span>
              <span class="gc-msg" style={msgStyleBase}>{e.text}</span>
            {:else}
            <!-- The mark slot: always rendered, even with nothing in it, so
                 every line keeps the same columns and not a character of the
                 message moves when the server's answer lands — the badge or
                 robot slides leftward into space that was already held open.
                 Intro only, never an outro: a list rebuilt on every change
                 strands outro-ing elements. Written as one unbroken run
                 because Svelte keeps the whitespace between elements, and a
                 stray space inside a fixed-width slot would shove the mark off
                 its centre. -->
            <span class="gc-mark"
              >{#if e.officer}<span in:fly|local={{ x: 10, duration: 220 }}
                  ><Icon name="badge" title="Officer" /></span
                >{:else if e.bot}<span in:fly|local={{ x: 10, duration: 220 }}
                  ><Icon name="robot" title="Guild bot" /></span
                >{/if}</span
            >
            <!-- Who the toon belongs to is the hover card's business, not the
                 line's: the card leads with the full display name or handle,
                 and putting it here cost a column the message wanted. The
                 dotted underline appears only once the row is confirmed, which
                 is exactly when the card has something to say (showMember).
                 The svelte-ignore is the same one the item spans below carry:
                 a hover card is not an interaction a keyboard can reach, and
                 giving the span a role it doesn't have would be a worse
                 answer. -->
            <!-- svelte-ignore a11y-no-static-element-interactions -->
            <span
              class="gc-toon"
              class:gc-hover={!e.pending}
              on:mouseenter={(ev) => showMember(ev, e)}
              on:mouseleave={hideMember}>{e.toon}</span
            >
            <span class="gc-sep">:</span>
            <!-- The item spans are written hard against the text around them —
                 no whitespace inside the {#each}/{#if} — because Svelte would
                 keep it and a space would open up mid-sentence. -->
            <span
              class="gc-msg"
              style={e.kind === "engage"
                ? msgStyleBase
                : msgStyle}>{#each segments(e.text, e.items) as s}{#if s.item}<!-- svelte-ignore a11y-no-static-element-interactions --><span
                    class="gc-item"
                    on:mouseenter={(ev) => showItem(ev, s.item)}
                    on:mouseleave={hideItem}>{s.t}</span
                  >{:else}{s.t}{/if}{/each}</span
            >
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<!-- The shared cards every surface shows, anchored beside the cursor. The
     wrapper is what carries the position; the card inside it is put back into
     normal flow (see .gc-tipwrap below), so x and y are handed the zeroes that
     mean "wherever the wrapper puts you". -->
{#if tip}
  <div class="gc-tipwrap" style={tip.pos}>
    <ItemTipCard
      name={tip.name}
      item={tip.item}
      holders={tipHolders}
      x={0}
      y={0}
    />
  </div>
{/if}
{#if mtip}
  <div class="gc-tipwrap" style={mtip.pos}>
    <MemberTipCard
      query={mtip.q}
      info={mtip.info}
      loading={mtip.loading}
      x={0}
      y={0}
    />
  </div>
{/if}

<style>
  .gc {
    height: 100%;
    display: flex;
    flex-direction: column;
    padding: 6px 14px;
    box-sizing: border-box;
    /* The panel is READ by scrolling, so it keeps its own scrollbar. */
    overflow-y: auto;
    /* The base row size the gear's Text size overrides, in CSS px — the UI
       scale zooms the whole shell on top of this (lib/scale.js). */
    font-size: 12.5px;
  }
  /* The gate note is one centred paragraph, not a log: nothing to scroll, and
     it sits in the middle of the box rather than at the bottom edge the chat
     grows from. */
  .gc.gc-gated {
    overflow-y: hidden;
    justify-content: center;
    align-items: center;
  }
  /* The rows live in their own column inside the scroll container so that
     container can push them against its bottom edge (below). flex: 0 0 auto
     keeps the block at its content height — a flex item that may shrink would
     fight the scrolling. */
  .gc-lines {
    display: flex;
    flex-direction: column;
    gap: 2px;
    flex: 0 0 auto;
  }
  /* Bottom mode, the default: a log too short to fill the box sits on the
     bottom edge and grows upward, the way the game's chat window does. This is
     an auto margin rather than justify-content: flex-end on the scroll
     container because Chromium puts the overflow of a flex-end container above
     its top, where the scrollbar can never reach it — and this box is meant to
     be scrolled back through. */
  .gc-lines.bottom {
    margin-top: auto;
  }
  /* One line of chat, and nothing beside it: with the identity in the hover
     card there is no second column left to lay out, so the row is a plain
     block. flex: 0 0 auto is still needed — .gc-lines is a flex column, and a
     row allowed to shrink would squeeze a wrapped message. */
  .gc-row {
    line-height: 1.35;
    flex: 0 0 auto;
  }
  /* A row whose server half hasn't landed yet — it is already readable, so
     the only mark is a slight fade that clears when the line is confirmed. */
  .gc-row.pending {
    opacity: 0.85;
  }
  /* The line itself: inline spans, so a long message wraps under itself rather
     than squeezing the time and name columns. */
  .gc-body {
    white-space: normal;
  }
  .gc-time {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 0.88em;
  }
  /* The officer/bot mark's reserved slot, between the time and the toon. It is
     held open on every line — an empty slot on a line whose speaker is
     neither, and on one the server hasn't answered for yet — so the columns
     are identical from the row's first frame and the mark's arrival moves
     nothing. */
  .gc-mark {
    display: inline-block;
    width: 1.25em;
    text-align: center;
    vertical-align: -0.125em;
  }
  /* The slot does the vertical nudging for both of them: Icon.svelte drops
     itself 0.125em to sit on the text baseline, and applied a second time
     inside a slot that has already been dropped it would ride visibly low. */
  .gc-mark :global(svg) {
    vertical-align: baseline;
  }
  /* An item name the server recognised in the line. It keeps the message's
     color, so the dotted underline is the only mark that says a card is
     waiting behind it. */
  .gc-item {
    text-decoration: underline dotted;
    text-underline-offset: 2px;
    cursor: default;
  }
  .gc-toon {
    color: var(--text-primary);
    font-weight: 700;
    /* Nothing here is clickable — the card IS the interaction — so the cursor
       stays an arrow. */
    cursor: default;
  }
  /* A confirmed row: the server has said who this is, so the name is worth
     hovering. The dotted underline is the same quiet affordance the item names
     carry, and its appearing is how the row announces that its card is ready. */
  .gc-hover {
    text-decoration: underline dotted;
    text-underline-offset: 2px;
  }
  .gc-sep {
    color: var(--text-muted);
  }
  /* White by default — chat is the thing being read here, so it gets the
     brightest text in the row and the time, mark and toon stay in their
     quieter tokens around it. The gear's Color setting overrides this inline,
     and engage rows outrank both (below). */
  .gc-msg {
    color: #ffffff;
    /* Unbroken spam — a pasted link, a wall of characters — wraps instead of
       widening the row past the panel. */
    overflow-wrap: anywhere;
  }
  /* Engage lines: the whole row gold, so a pull stands out of the chat. */
  .gc-engage .gc-time,
  .gc-engage .gc-mark,
  .gc-engage .gc-toon,
  .gc-engage .gc-sep,
  .gc-engage .gc-msg {
    color: #ffc94a;
  }
  /* The world-message tag, in the mark and toon's place: small, uppercase and
     the same size relationship to the row that the time has, so it reads as a
     column rather than as shouting. */
  .gc-tag {
    font-size: 0.8em;
    font-weight: 800;
    letter-spacing: 0.06em;
  }
  /* One colour per kind, carried across the whole row the way an engage's gold
     is: the colour IS the label at a glance, and all three are already in the
     app's palette — the map/quest blue, the warm orange of the loot markers,
     and the popped-mob red. */
  .gc-gm .gc-time,
  .gc-gm .gc-tag,
  .gc-gm .gc-msg {
    color: #6ecbff;
  }
  .gc-server .gc-time,
  .gc-server .gc-tag,
  .gc-server .gc-msg {
    color: #ff8c5a;
  }
  .gc-quake .gc-time,
  .gc-quake .gc-tag,
  .gc-quake .gc-msg {
    color: #ff5555;
  }
  .gc-note {
    font-size: 11px;
    font-style: italic;
    color: var(--text-muted);
  }
  /* Same muted italic as the other empty states, but centred both ways and
     given a readable measure — this one is a sentence, not a status word. */
  .gc-gate {
    font-size: 11px;
    font-style: italic;
    color: var(--text-muted);
    text-align: center;
    max-width: 420px;
    line-height: 1.6;
  }
  /* A link, not a button: it navigates. It cannot be an <a href> — the app is
     one webview with no routes, and a real href would try to leave the page. */
  .gc-link {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    color: var(--accent);
    text-decoration: underline;
    cursor: pointer;
  }
  .gc-link:hover {
    color: var(--text-primary);
  }

  /* The hover cards' anchor. Both cards draw themselves as a fixed .tip at an
     (x, y) their pane hands them, which can only place a TOP edge — and a top
     edge needs the card's height to be known before it is drawn. Here the
     WRAPPER is positioned instead, by whichever two edges keep it beside the
     cursor, and the card is returned to normal flow inside it. A card anchored
     by its bottom edge then sits just above the cursor at whatever height it
     turns out to be, which is the whole point: the panel lives at the bottom of
     the window, so nearly every card opens upward. */
  .gc-tipwrap {
    position: fixed;
    z-index: 500;
    width: 260px;
    pointer-events: none;
  }
  .gc-tipwrap :global(.tip) {
    position: static;
  }
</style>
