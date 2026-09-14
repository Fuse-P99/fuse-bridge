<script>
  // Guild Chat special overlay: the live #guild-stream — every guild message,
  // plus the raid-mob engage lines, as the server already relays them to
  // Discord. Officers only, enforced server-side.
  //
  // No log parsing happens here: the lines are the SERVER's (guildmates' as
  // well as this user's), so the whole overlay is one polled endpoint. The poll
  // runs only while this component is mounted, which is to say only while the
  // overlay is open.
  import { onMount, onDestroy } from "svelte";
  import { GetGuildFeed } from "../../bindings/FuseBridge/app.js";
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

  // Held oldest-first (the ids arrive that way); rendered newest-first below.
  let entries = [];
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

  onMount(async () => {
    await poll();
    timer = setInterval(poll, 1000);
  });
  onDestroy(() => clearInterval(timer));

  $: shown = showEngages ? entries : entries.filter((e) => e.kind !== "engage");
  // Newest at the top: the line you care about is the one that just landed, and
  // the window is short. Nothing auto-scrolls — scrolling back through the log
  // stays exactly where the reader left it.
  $: rows = [...shown].reverse();

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

<div class="gc" class:locked>
  {#if !officer}
    <div class="gc-note">Officers only</div>
  {:else if !rows.length}
    <div class="gc-note">{okOnce ? "No guild chat yet" : "Connecting…"}</div>
  {:else}
    {#each rows as e (e.id)}
      <div class="gc-row" class:gc-engage={e.kind === "engage"}>
        <span class="gc-time">{fmtTime(e.at_ms)}</span>
        <span class="gc-who">[{#if e.officer}<Icon name="badge" title="Officer" />{" "}{:else if e.bot}<Icon name="robot" title="Guild bot" />{" "}{/if}{ident(e)}]</span>
        <span class="gc-toon">{e.toon}</span>
        <span class="gc-sep">:</span>
        <span class="gc-msg">{e.text}</span>
      </div>
    {/each}
  {/if}
</div>

<style>
  .gc {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
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
  /* A block, not a flex row: a long message has to wrap under itself rather
     than squeezing the time and name columns. */
  .gc-row {
    display: block;
    white-space: normal;
    line-height: 1.35;
    flex: 0 0 auto;
  }
  .gc-time {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .gc-who {
    color: var(--text-secondary);
  }
  .gc-toon {
    color: var(--text-primary);
    font-weight: 700;
  }
  .gc-sep {
    color: var(--text-muted);
  }
  .gc-msg {
    color: var(--text-primary);
    /* Unbroken spam — a pasted link, a wall of characters — wraps instead of
       widening the row past the window. */
    overflow-wrap: anywhere;
  }
  /* Engage lines: the whole row gold, so a pull stands out of the chat. */
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
