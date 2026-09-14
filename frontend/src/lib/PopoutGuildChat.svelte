<script>
  // Guild Chat special overlay: the live #guild-stream — every guild message,
  // plus the raid-mob engage lines, as the server already relays them to
  // Discord. Officers only, enforced server-side.
  //
  // No log parsing happens here: the lines are the SERVER's (guildmates' as
  // well as this user's), so the whole overlay is one polled endpoint. The poll
  // runs only while this component is mounted, which is to say only while the
  // overlay is open.
  //
  // It reads like the game's own chat window: newest line at the BOTTOM, scroll
  // up for older ones, and the box follows the conversation as lines arrive.
  // The "Reverse Messages" setting (newestTop) flips it back to newest-first,
  // the layout this overlay shipped with.
  import { onMount, onDestroy, tick } from "svelte";
  import {
    GetGuildFeed,
    ShowHoverCard,
    HideHoverCard,
  } from "../../bindings/FuseBridge/app.js";
  import Icon from "./Icon.svelte";

  // No hasContent prop, deliberately: the popout shell treats a special
  // overlay with no content as empty and hides its whole content area once
  // the placement grace ends, leaving the title bar alone. This box is the
  // content — it is sized and made opaque to cover the game's own chat window
  // — so the shell counts it as always full (Popout.svelte), and "No guild
  // chat yet" stays on screen inside it.
  // Gear / Manage Overlays option: the engage lines are raid noise to someone
  // who only wants the conversation.
  export let showEngages = true;
  // The overlay is click-through once it locks (only with its "Stay unlocked"
  // setting off — see Popout.svelte), and a click-through window can't be
  // scrolled: the scrollbar would be a lie, so it's taken away.
  export let locked = false;
  // Gear / Manage Overlays option ("Reverse Messages"): put the newest line at
  // the TOP instead of the bottom, which is how this overlay used to read. Off
  // by default.
  export let newestTop = false;
  // Gear / Manage Overlays options for the message text: font and color dress
  // the MESSAGE alone, so the time, the identity bracket, the toon and the
  // colon keep the overlay's own look and the columns stay a readable, uniform
  // ledger no matter what the conversation is dressed in. Size is the
  // exception — it is set on the whole ROW, so asking for bigger chat gives
  // bigger chat rather than a large message beside a tiny clock. Each
  // empty/zero value means "inherit".
  export let msgFont = "";
  export let msgSize = 0;
  export let msgColor = "";
  // This window's Go-side name (Popout.svelte's WINDOW_NAME), so the hover
  // card can be placed relative to this overlay's position.
  export let windowName = "";

  // Held oldest-first (the ids arrive that way); rendered in that order unless
  // newestTop reverses them below.
  let entries = [];
  let el; // the scroll container, so new lines can keep it pinned to the bottom
  // Has the box ever been filled? The first backfill has to land at the newest
  // line rather than at the oldest one the server still holds.
  let filledOnce = false;
  let after = 0; // highest id seen, so each poll asks only for what's new
  let bootMs = 0; // server start time — a change means the ids restarted
  let officer = true; // assume yes until told otherwise, so the overlay
  // doesn't flash "Officers only" on its first frame
  let okOnce = false; // has the server answered even once
  let timer;
  let polling = false,
    pollAgain = false;

  // As many lines as the server will hand back in one go. Older ones fall off
  // the top; this is a live stream, not a searchable archive.
  const MAX_ROWS = 500;

  async function poll() {
    if (polling) {
      pollAgain = true;
      return;
    }
    polling = true;
    try {
      const r = await GetGuildFeed(after);
      // No answer (timeout, server hiccup): keep what's on screen. Only a real
      // reply may change what this overlay shows.
      if (r && r.ok) {
        okOnce = true;
        officer = !!r.officer;
        if (officer) {
          // Where the reader is sitting, measured BEFORE the new lines land.
          // Within 24px of the bottom counts as "at the bottom": the last row
          // is often part-cut and the browser's scroll numbers are fractional.
          const wasAtBottom =
            !el || el.scrollHeight - el.scrollTop - el.clientHeight < 24;
          const firstFill = !filledOnce;
          // Server restarted: its ids start over, so anything held would
          // collide with the new numbering. Drop it and take the backfill this
          // same response carries.
          if (bootMs && r.boot_ms !== bootMs) {
            entries = [];
            after = 0;
          }
          bootMs = r.boot_ms || bootMs;
          const seen = new Set(entries.map((e) => e.id));
          const add = (r.entries || []).filter((e) => !seen.has(e.id));
          if (add.length) {
            const all = entries.concat(add);
            entries = all.length > MAX_ROWS ? all.slice(-MAX_ROWS) : all;
          }
          if (r.latest_id) after = r.latest_id;
          if (add.length) {
            filledOnce = true;
            // Three reasons to follow the conversation down, and one to stay
            // put. Follow when the reader was already at the bottom (they are
            // reading it live); when this is the first fill (the box should
            // open on the newest line); and when the overlay is locked, since
            // a click-through window can't be scrolled by hand at all, so the
            // latest lines are the only ones worth showing. Otherwise leave
            // the scroll alone: someone who scrolled up to read history must
            // not be yanked back down by every line that arrives, and because
            // rows are appended BELOW them nothing they are reading moves —
            // no scroll-position correction is needed.
            if (wasAtBottom || firstFill || locked) await snapToBottom();
          }
        }
      }
    } catch {
      /* keep last */
    }
    polling = false;
    if (pollAgain) {
      pollAgain = false;
      poll();
    }
  }

  // snapToBottom pins the box to its newest line, after Svelte has put the new
  // rows in the DOM (tick) so the scroll extent it measures is the real one.
  // Bottom mode only: with newestTop on, the newest line is already the first
  // one on screen and the top is where the reader wants to be.
  async function snapToBottom() {
    await tick();
    if (el && !newestTop) el.scrollTop = el.scrollHeight;
  }

  onMount(async () => {
    await poll();
    timer = setInterval(poll, 1000);
  });
  onDestroy(() => {
    clearInterval(timer);
    // The card is a window of its own (hovercard.go) — closing this overlay
    // with the cursor still on a line would otherwise leave it hanging there
    // until its safety timer fired.
    hideCard();
  });

  // ── hover cards ─────────────────────────────────────────────────────────
  // Both hovers hand off to the same Go-side window, which draws the shared
  // item/member card next to the cursor. It has to be a separate window: this
  // overlay is small and usually sits over the game's chat box, and a card
  // drawn inside it would be clipped to that box.
  //
  // `locked` makes the overlay click-through, so no hover can reach us then
  // anyway — the guard is here so that can never stop being true by accident.
  function showItem(ev, name) {
    if (locked || !windowName) return;
    ShowHoverCard(windowName, "item", name, ev.clientX, ev.clientY).catch(
      () => {},
    );
  }
  // The identity bracket resolves the way Discord's /who does: the handle is
  // unique, and a toon with no handle resolves to whoever owns it. With
  // neither there is nothing to look up.
  function showMember(ev, e) {
    if (locked || !windowName) return;
    const q = (e.handle || e.toon || "").trim();
    if (!q) return;
    ShowHoverCard(windowName, "member", q, ev.clientX, ev.clientY).catch(
      () => {},
    );
  }
  function hideCard() {
    HideHoverCard().catch(() => {});
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

  // Turning "Reverse Messages" back off means asking for the chat-window
  // layout, so show what that layout is for: the newest line, at the bottom.
  $: newestTop, snapToBottom();
  // Locking makes the overlay click-through — the reader loses the scrollbar,
  // so drop them at the live end of the log rather than wherever they were.
  $: if (locked) snapToBottom();

  $: shown = showEngages ? entries : entries.filter((e) => e.kind !== "engage");
  // Reading order. The default is the game's: oldest first, so the newest line
  // sits at the bottom and older ones are above it. "Reverse Messages" flips
  // that, the layout this overlay shipped with — the line you care about is the
  // one that just landed, and the window is short.
  $: rows = newestTop ? [...shown].reverse() : shown;

  // Text size dresses the whole ROW, so the time, the identity bracket and the
  // toon grow and shrink with the message they belong to — the row is one line
  // of chat, not a message with furniture around it. Everything left of the
  // message sizes in em (see .gc-time), so this one declaration scales all of
  // it. Unset (0) leaves the overlay's own size in charge.
  $: rowStyle = msgSize > 0 ? `font-size:${msgSize}px;` : "";

  // Message-text style, assembled from only the settings that are actually
  // set: an empty inline style leaves the stylesheet (and the overlay's own
  // font) in charge. Two versions, because an engage line's gold is the point
  // of the engage line — the font dresses it like the rest of the chat, but a
  // custom message color must not repaint it.
  $: msgStyleBase = msgFont ? `font-family:${msgFont};` : "";
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

  // Who the toon belongs to: their Discord display name, else their handle.
  // Truncated so the identity column can't push the message off the line —
  // "unrostered" for a toon the server can't attribute to a member.
  //
  // The server also says whether that owner is an officer (e.officer) or a
  // known guild bot (e.bot), and the bracket below draws the same badge and
  // robot marks #guild-stream puts on the line. That bracket is written as one
  // unbroken line on purpose: Svelte keeps the whitespace between elements, so
  // a wrapped tag would open the bracket with a stray space. The single space
  // after a mark is the deliberate {" "}.
  function ident(e) {
    return (e.display || e.handle || "unrostered").slice(0, 10);
  }
</script>

<div class="gc" class:locked bind:this={el}>
  {#if !officer}
    <div class="gc-note">Officers only</div>
  {:else if !rows.length}
    <div class="gc-note">{okOnce ? "No guild chat yet" : "Connecting…"}</div>
  {:else}
    <div class="gc-lines" class:bottom={!newestTop}>
      {#each rows as e (e.id)}
        <div
          class="gc-row"
          class:gc-engage={e.kind === "engage"}
          style={rowStyle}
        >
          <span class="gc-time">{fmtTime(e.at_ms)}</span>
          <!-- svelte-ignore a11y-no-static-element-interactions -->
          <span class="gc-who" on:mouseenter={(ev) => showMember(ev, e)} on:mouseleave={hideCard}>[{#if e.officer}<Icon name="badge" title="Officer" />{" "}{:else if e.bot}<Icon name="robot" title="Guild bot" />{" "}{/if}{ident(e)}]</span>
          <span class="gc-toon">{e.toon}</span>
          <span class="gc-sep">:</span>
          <!-- The item spans are written hard against the text around them —
               no whitespace inside the {#each}/{#if} — because Svelte would
               keep it and a space would open up mid-sentence. The
               svelte-ignore is the same one the raid loot rows carry: a hover
               card is not an interaction a keyboard can reach, and giving the
               span a role it doesn't have would be a worse answer. -->
          <span
            class="gc-msg"
            style={e.kind === "engage"
              ? msgStyleBase
              : msgStyle}>{#each segments(e.text, e.items) as s}{#if s.item}<!-- svelte-ignore a11y-no-static-element-interactions --><span
                  class="gc-item"
                  on:mouseenter={(ev) => showItem(ev, s.item)}
                  on:mouseleave={hideCard}>{s.t}</span
                >{:else}{s.t}{/if}{/each}</span
          >
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .gc {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 6px 8px;
    /* The one special overlay that is READ by scrolling, so it keeps its own
       scrollbar — and, while its "Stay unlocked" setting is on (the default,
       the map's rule), keeps the mouse even with the overlays locked. */
    overflow-y: auto;
    font-size: 12.5px;
    /* Readable over the game even on a transparent background. */
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
  /* With "Stay unlocked" off the overlay locks like every other one: the window
     is click-through, so the wheel never reaches this element. Drop the
     scrollbar rather than show one nothing can move — it then reads as what it
     is, the most recent messages and no further back. */
  .gc.locked {
    overflow-y: hidden;
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
  /* Bottom mode, the default: a log too short to fill the window sits on the
     bottom edge and grows upward, the way the game's chat window does. This is
     an auto margin rather than justify-content: flex-end on the scroll
     container because Chromium puts the overflow of a flex-end container above
     its top, where the scrollbar can never reach it — and this box is meant to
     be scrolled back through. */
  .gc-lines.bottom {
    margin-top: auto;
  }
  /* A block, not a flex row: a long message has to wrap under itself rather
     than squeezing the time and name columns. */
  .gc-row {
    display: block;
    white-space: normal;
    line-height: 1.35;
    flex: 0 0 auto;
  }
  /* Relative, not the 11px it used to be: the Text size setting is applied to
     the row, so everything left of the message has to be expressed in terms of
     it. 0.88em is that old 11px against the overlay's own 12.5px. */
  .gc-time {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 0.88em;
  }
  /* Hovering the bracket opens the member card. No underline: the brackets
     already mark it out, and the cursor stays an arrow because there is
     nothing to click — the card is the whole interaction. */
  .gc-who {
    color: var(--text-secondary);
    cursor: default;
  }
  /* An item name the server recognised in the line. It keeps the message's
     color (a custom one included), so the dotted underline is the only mark
     that says a card is waiting behind it. */
  .gc-item {
    text-decoration: underline dotted;
    text-underline-offset: 2px;
    cursor: default;
  }
  .gc-toon {
    color: var(--text-primary);
    font-weight: 700;
  }
  .gc-sep {
    color: var(--text-muted);
  }
  /* The message text alone answers to the Message font and color settings,
     applied inline by the markup above — so these are the defaults it falls
     back to when nothing is set. (Text size is on the row, not here.) */
  .gc-msg {
    color: var(--text-primary);
    /* Unbroken spam — a pasted link, a wall of characters — wraps instead of
       widening the row past the window. */
    overflow-wrap: anywhere;
  }
  /* Engage lines: the whole row gold, so a pull stands out of the chat. The
     message-color setting is deliberately withheld from these rows (see
     msgStyleBase), so nothing outranks the gold here. */
  .gc-engage .gc-time,
  .gc-engage .gc-who,
  .gc-engage .gc-toon,
  .gc-engage .gc-sep,
  .gc-engage .gc-msg {
    color: #ffc94a;
  }
  .gc-note {
    font-size: 11px;
    font-style: italic;
    color: var(--text-muted);
  }
</style>
