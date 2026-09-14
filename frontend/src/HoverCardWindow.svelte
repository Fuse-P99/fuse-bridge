<script>
  // The hover-card window's whole page. Go owns the window (hovercard.go):
  // it places it beside the cursor, shows and hides it, and announces what to
  // draw with the "hovercard" app event. This side only listens and renders,
  // which is why it can stay this small.
  //
  // Everything but the card itself is transparent, so the window's generous
  // fixed size costs nothing on screen — the card sits in whichever corner is
  // nearest the cursor (flip_x / flip_y, decided by Go when the card would
  // otherwise run off the screen).
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    LookupItems,
    WhoHasItem,
    GetMemberInfo,
  } from "../bindings/FuseBridge/app.js";
  import ItemTipCard from "./lib/ItemTipCard.svelte";
  import MemberTipCard from "./lib/MemberTipCard.svelte";

  let kind = "";
  let query = "";
  let flipX = false;
  let flipY = false;
  let loading = false;
  let item = null;
  let holders = [];
  let member = null;

  // One hover at a time: every request bumps this, and a lookup that lands
  // after the cursor has moved on drops its own result.
  let seq = 0;

  // Item records never change, so they are cached for the life of the window.
  const itemCache = new Map(); // lower(name) → item | null
  // A member lookup costs the server a Discord avatar fetch, so it is cached
  // too — but only briefly, since DKP and attendance move.
  const memberCache = new Map(); // lower(query) → { at, info }
  const MEMBER_TTL = 5 * 60 * 1000;

  function onCard(p) {
    const k = p && p.kind === "member" ? "member" : "item";
    const q = String((p && p.query) || "");
    flipX = !!(p && p.flip_x);
    flipY = !!(p && p.flip_y);
    // Nothing of the previous card survives into this one: a stale DKP line
    // under a new name would be worse than an empty card.
    const s = ++seq;
    kind = k;
    query = q;
    item = null;
    holders = [];
    member = null;
    loading = true;
    if (!q) {
      loading = false;
      return;
    }
    if (k === "member") loadMember(q, s);
    else loadItem(q, s);
  }

  async function loadItem(name, s) {
    const key = name.toLowerCase();
    if (itemCache.has(key)) {
      if (s === seq) {
        item = itemCache.get(key);
        loading = false;
      }
    } else {
      let got = null;
      try {
        const res = await LookupItems([name]);
        got = (res && res.items && res.items[key]) || null;
        itemCache.set(key, got);
      } catch {
        /* leave it null — the card says it isn't in the DB */
      }
      if (s !== seq) return;
      item = got;
      loading = false;
    }
    // "Held by" is a bonus footer, loaded after the card is already readable.
    try {
      const hits = (await WhoHasItem(name)) || [];
      if (s === seq) holders = hits;
    } catch {
      /* a failed lookup simply shows no footer */
    }
  }

  async function loadMember(q, s) {
    const key = q.toLowerCase();
    const hit = memberCache.get(key);
    if (hit && Date.now() - hit.at < MEMBER_TTL) {
      if (s === seq) {
        member = hit.info;
        loading = false;
      }
      return;
    }
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
    if (s !== seq) return;
    member = info;
    loading = false;
  }

  onMount(() => {
    // Same transparency the overlays set for themselves (Popout.svelte): the
    // stylesheet's opaque page background would otherwise paint a grey slab
    // the size of this window over the game.
    document.documentElement.style.background = "transparent";
    document.body.style.background = "transparent";
    // Scopes the one global rule below to this window alone.
    document.body.classList.add("hovercard");
    return Events.On("hovercard", (ev) => {
      const d = ev && ev.data != null ? ev.data : ev;
      onCard(Array.isArray(d) ? d[0] : d);
    });
  });
</script>

<div class="wrap" class:right={flipX} class:bottom={flipY}>
  {#if kind === "member"}
    <MemberTipCard {query} info={member} {loading} />
  {:else if kind === "item"}
    <ItemTipCard
      name={query}
      {item}
      {holders}
      x={0}
      y={0}
      fallback={loading ? "Looking up…" : "Not in the item DB yet."}
    />
  {/if}
</div>

<style>
  /* The cards position themselves (position: fixed at the x/y their pane
     gives them) because everywhere else they float inside a full app window.
     Here the WINDOW is the positioning — Go already put it beside the cursor —
     so the card is put back in flow and this flex box parks it in the corner
     nearest the cursor. Keyed on the body class this window alone sets, so
     the rule can't reach the cards in the main window. */
  :global(body.hovercard .tip) {
    position: static !important;
  }
  .wrap {
    position: fixed;
    inset: 0;
    display: flex;
    align-items: flex-start;
    justify-content: flex-start;
  }
  .wrap.bottom {
    align-items: flex-end;
  }
  .wrap.right {
    justify-content: flex-end;
  }
</style>
