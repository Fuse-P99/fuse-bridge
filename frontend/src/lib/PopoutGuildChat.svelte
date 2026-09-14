<script>
  // Guild Chat special overlay: the live #guild-stream — every guild message,
  // plus the raid-mob engage lines, as the server already relays them to
  // Discord. Officers only for the guild's WHOLE stream, enforced server-side.
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
  // the line at all any more: it lives in the hover card on the toon name,
  // which starts answering the moment the row is confirmed. The only thing
  // still drawn inline is the officer/bot mark, and it has a reserved slot
  // between the time and the toon — held open from the row's first frame, so
  // the mark slides into it without moving a character of the message.
  //
  // It reads like the game's own chat window: newest line at the BOTTOM, scroll
  // up for older ones, and the box follows the conversation as lines arrive.
  // The "Reverse Messages" setting (newestTop) flips it back to newest-first,
  // the layout this overlay shipped with.
  import { onMount, onDestroy, tick } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { fly } from "svelte/transition";
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
  // Gear / Manage Overlays options for the message text: font, bold and color
  // dress the MESSAGE alone, so the time, the mark, the toon and the colon keep
  // the overlay's own look and the columns stay a readable, uniform ledger no
  // matter what the conversation is dressed in. Size is the exception — it is
  // set on the whole ROW, so asking for bigger chat gives bigger chat rather
  // than a large message beside a tiny clock. Each empty/zero/false value means
  // "inherit".
  export let msgFont = "";
  export let msgSize = 0;
  export let msgColor = "";
  export let msgBold = false;
  // This window's Go-side name (Popout.svelte's WINDOW_NAME), so the hover
  // card can be placed relative to this overlay's position.
  export let windowName = "";

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
  let officer = true; // assume yes until told otherwise, so the overlay
  // doesn't flash "Officers only" on its first frame
  let seenAny = false; // a server answer, or a line of our own, has arrived
  let timer;
  let off; // the change-event unsubscribe
  let refreshing = false,
    refreshAgain = false;

  async function refresh() {
    if (refreshing) {
      refreshAgain = true;
      return;
    }
    refreshing = true;
    try {
      const r = await GetGuildFeed(version);
      if (r) {
        // Carried on every answer, changed view or not, so a member promoted
        // mid-session stops being told "Officers only" without a line having
        // to arrive first.
        officer = !!r.officer;
        if (r.ok) seenAny = true;
        if (r.changed) {
          // Where the reader is sitting, measured BEFORE the new lines land.
          // Within 24px of the bottom counts as "at the bottom": the last row
          // is often part-cut and the browser's scroll numbers are fractional.
          const wasAtBottom =
            !el || el.scrollHeight - el.scrollTop - el.clientHeight < 24;
          const firstFill = !filledOnce;
          entries = r.entries || [];
          version = r.version;
          if (entries.length) {
            seenAny = true;
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
    refreshing = false;
    if (refreshAgain) {
      refreshAgain = false;
      refresh();
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
    await refresh();
    // The event is the fast path — Go fires it the moment a line of ours is
    // logged or the server's half merges. The interval is a safety net for the
    // one case an event can't cover (a dropped event while the window was
    // being created), and asking for the view is also what keeps Go's server
    // poller alive, so it must stay comfortably inside that poller's idle
    // timeout.
    off = Events.On("guildchat-changed", refresh);
    timer = setInterval(refresh, 2000);
  });
  onDestroy(() => {
    clearInterval(timer);
    if (off) off();
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
  // The toon's hover card resolves the way Discord's /who does: the handle is
  // unique, and a toon with no handle resolves to whoever owns it. With
  // neither there is nothing to look up.
  function showMember(ev, e) {
    if (locked || !windowName) return;
    // A pending row is one the server hasn't answered for: nothing is known
    // about its speaker yet, so there is no card to draw. The hover starts
    // working on its own the moment the answer merges onto the row — which is
    // also when the toon's dotted underline appears (class:gc-hover).
    if (e.pending) return;
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

  // Text size dresses the whole ROW, so the time, the mark slot and the toon
  // grow and shrink with the message they belong to — the row is one line of
  // chat, not a message with furniture around it. Everything left of the
  // message sizes in em (see .gc-time and .gc-mark), so this one declaration
  // scales all of it. Unset (0) leaves the overlay's own size in charge.
  $: rowStyle = msgSize > 0 ? `font-size:${msgSize}px;` : "";

  // Message-text style, assembled from only the settings that are actually
  // set: an empty inline style leaves the stylesheet (and the overlay's own
  // font) in charge. Two versions, because an engage line's gold is the point
  // of the engage line — the font and the weight dress it like the rest of the
  // chat, but a custom message color must not repaint it.
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

  // What the empty box says. "Officers only" is the server's refusal, and it
  // belongs on screen only while there is nothing to show: a member who is not
  // an officer still sees the lines read from their OWN log, which are their
  // own chat rather than the guild's stream. "Connecting…" holds until either
  // half has produced anything at all.
  $: emptyNote = !officer
    ? "Officers only"
    : seenAny
      ? "No guild chat yet"
      : "Connecting…";
</script>

<div class="gc" class:locked bind:this={el}>
  {#if !rows.length}
    <div class="gc-note">{emptyNote}</div>
  {:else}
    <div class="gc-lines" class:bottom={!newestTop}>
      {#each rows as e (e.id)}
        <div
          class="gc-row"
          class:gc-engage={e.kind === "engage"}
          class:pending={e.pending}
          style={rowStyle}
        >
          <div class="gc-body">
            <span class="gc-time">{fmtTime(e.at_ms)}</span>
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
            <!-- Who the toon belongs to is the hover card's business now, not
                 the line's: the card already leads with the full display name
                 or handle, and putting it here cost a column the message
                 wanted. The dotted underline appears only once the row is
                 confirmed, which is exactly when the card has something to
                 say (showMember). The svelte-ignore is the same one the item
                 spans below carry: a hover card is not an interaction a
                 keyboard can reach, and giving the span a role it doesn't have
                 would be a worse answer. -->
            <!-- svelte-ignore a11y-no-static-element-interactions -->
            <span
              class="gc-toon"
              class:gc-hover={!e.pending}
              on:mouseenter={(ev) => showMember(ev, e)}
              on:mouseleave={hideCard}>{e.toon}</span
            >
            <span class="gc-sep">:</span>
            <!-- The item spans are written hard against the text around them —
                 no whitespace inside the {#each}/{#if} — because Svelte would
                 keep it and a space would open up mid-sentence. The
                 svelte-ignore is the same one the raid loot rows carry: a
                 hover card is not an interaction a keyboard can reach, and
                 giving the span a role it doesn't have would be a worse
                 answer. -->
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
  /* One line of chat, and nothing beside it: with the identity in the hover
     card there is no second column left to lay out, so the row is a plain
     block again. flex: 0 0 auto is still needed — .gc-lines is a flex column,
     and a row allowed to shrink would squeeze a wrapped message. */
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
  /* Relative, not the 11px it used to be: the Text size setting is applied to
     the row, so everything left of the message has to be expressed in terms of
     it. 0.88em is that old 11px against the overlay's own 12.5px. */
  .gc-time {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 0.88em;
  }
  /* The officer/bot mark's reserved slot, between the time and the toon. Its
     width is in em so it tracks the Text size setting, and it is held open on
     every line — an empty slot on a line whose speaker is neither, and on one
     the server hasn't answered for yet — so the columns are identical from the
     row's first frame and the mark's arrival moves nothing. */
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
  /* The message text alone answers to the Message font, Bold and color
     settings, applied inline by the markup above — so these are the defaults
     it falls back to when nothing is set. (Text size is on the row, not
     here.) */
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
  .gc-engage .gc-mark,
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
