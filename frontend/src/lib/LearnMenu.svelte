<script>
  // Hover-expanding "learn" menu — one saved Discord guide post per
  // (mob, role) pairing, shown beside every mob on the Raids tab regardless
  // of window state. The trigger appears on row hover; hovering it expands
  // the menu (no click). Roles with a guide open the post in the browser
  // (never discord:// — deep links don't work for admins-run Discord);
  // roles without one offer to add it. The add/replace modal lives in
  // RaidsTab (shared), reached through the callback props.
  export let mob;
  export let zoneTag = ""; // Gynok zone tag when the mob's zone is group-tracked
  export let guides = {}; // lower(mob or tag) → role → url
  export let onAdd = () => {};
  export let onEdit = () => {};

  const ROLES = ["track", "coth", "fte"];

  // One row per role normally; in a zone-grouped zone each role doubles into
  // a mob-scoped and a zone-scoped row ("Lord Vyemm track" / "tov track") —
  // zone guides are keyed by the tag, so every mob in the zone shares them.
  $: entries = ROLES.flatMap((role) => {
    const rows = [
      { key: mob, role, label: zoneTag ? `${mob} ${role}` : `${role} guide` },
    ];
    if (zoneTag) rows.push({ key: zoneTag, role, label: `${zoneTag} ${role}` });
    return rows;
  });

  function urlFor(gmap, key, role) {
    const r = gmap[(key || "").toLowerCase()];
    return r ? r[role] || "" : "";
  }
</script>

<span class="hov-wrap">
  <span class="hov-trigger">learn</span>
  <div class="hov-menu">
    <div class="hm-body">
      {#each entries as en}
        {#if urlFor(guides, en.key, en.role)}
          <div class="hm-row">
            <a
              class="hm-item"
              href={urlFor(guides, en.key, en.role)}
              target="_blank"
              rel="noreferrer"
              on:click|stopPropagation>{en.label} ↗</a
            >
            <button
              class="hm-mini"
              title="Replace the saved guide link (one link per mob/role)"
              on:click|stopPropagation={() =>
                onEdit(en.key, en.role, urlFor(guides, en.key, en.role))}
              >✎</button
            >
          </div>
        {:else}
          <button
            class="hm-item dim"
            on:click|stopPropagation={() => onAdd(en.key, en.role)}
            >{en.label} — add</button
          >
        {/if}
      {/each}
    </div>
  </div>
</span>

<style>
  .hov-wrap {
    position: relative;
    display: none;
    margin-left: 2px;
  }
  /* Revealed by the row (the parent .mob-head owns the hover), kept open
     while the cursor is anywhere inside the wrap — including the expanded
     menu, which hangs off it with a padding bridge instead of a gap. */
  :global(.mob-head:hover) .hov-wrap,
  .hov-wrap:hover {
    display: inline-block;
  }
  .hov-trigger {
    display: inline-block;
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-muted);
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    white-space: nowrap;
  }
  .hov-wrap:hover .hov-trigger {
    color: var(--text-primary);
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
    min-width: 140px;
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
    text-decoration: none;
    font-family: inherit;
    white-space: nowrap;
  }
  .hm-item:hover {
    background: rgba(200, 169, 81, 0.1);
    color: var(--accent);
  }
  .hm-item.dim {
    color: var(--text-muted);
    font-style: italic;
    text-transform: none;
  }
  .hm-row {
    display: flex;
    align-items: center;
  }
  .hm-row .hm-item {
    flex: 1;
  }
  .hm-mini {
    background: transparent;
    border: none;
    color: var(--text-muted);
    font-size: 11px;
    padding: 5px 8px 5px 2px;
    cursor: pointer;
  }
  .hm-mini:hover {
    color: var(--accent);
  }
</style>
