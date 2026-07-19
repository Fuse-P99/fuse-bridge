<script>
  // One group row in the trigger edit tree, rendering its child groups
  // recursively (svelte:self) and its triggers as leaves. Click expands;
  // right-click opens the context menu; the slider on the right enables or
  // disables the group/trigger (handled by the parent tab).
  export let node; // TriggerGroupUI {id,name,enabled,groups,triggers}
  export let depth = 0;
  export let expanded = {}; // groupId -> true
  export let highlightId = 0; // trigger session id to highlight
  export let onToggle; // (groupId) => void
  export let onMenu; // (event, "group"|"trigger", node|trigger) => void
  export let onToggleEnable; // ("group"|"trigger", node|trigger, checked) => void
  export let query = ""; // active search text — matches get <mark>ed in names

  // HTML-escape then wrap query matches in <mark> (same pattern as ZonesTab).
  const esc = (s) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  function hl(text, q) {
    if (!q) return esc(text);
    const lower = text.toLowerCase();
    let out = "";
    let i = 0;
    for (;;) {
      const j = lower.indexOf(q, i);
      if (j < 0) {
        out += esc(text.slice(i));
        break;
      }
      out +=
        esc(text.slice(i, j)) +
        "<mark>" +
        esc(text.slice(j, j + q.length)) +
        "</mark>";
      i = j + q.length;
    }
    return out;
  }
</script>

<div class="node" style="margin-left:{depth ? 14 : 0}px">
  <div
    class="grp-row"
    class:dim={!node.enabled}
    role="button"
    tabindex="0"
    on:click={() => onToggle(node.id)}
    on:keydown={(e) => e.key === "Enter" && onToggle(node.id)}
    on:contextmenu|preventDefault={(e) => onMenu(e, "group", node)}
  >
    <span class="caret">{expanded[node.id] ? "▾" : "▸"}</span>
    <span class="grp-name">{@html hl(node.name, query)}</span>
    {#if node.total_triggers}
      <span class="cnt">{node.total_triggers}</span>
    {/if}
    <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-noninteractive-element-interactions -->
    <label
      class="sw"
      title={node.enabled ? "Disable group" : "Enable group"}
      on:click|stopPropagation
    >
      <input
        type="checkbox"
        checked={node.enabled}
        on:change={(e) => onToggleEnable("group", node, e.target.checked)}
      />
      <span class="knob"></span>
    </label>
  </div>

  {#if expanded[node.id]}
    {#each node.groups || [] as g (g.id)}
      <svelte:self
        node={g}
        depth={depth + 1}
        {expanded}
        {highlightId}
        {query}
        {onToggle}
        {onMenu}
        {onToggleEnable}
      />
    {/each}
    {#each node.triggers || [] as t (t.id)}
      <div
        id="trig-{t.id}"
        class="trig-row"
        class:hl={t.id === highlightId}
        class:dim={!t.enabled || t.unsupported}
        style="margin-left:14px"
        on:contextmenu|preventDefault={(e) => onMenu(e, "trigger", t)}
        role="treeitem"
        aria-selected={t.id === highlightId}
        tabindex="-1"
      >
        <span class="trig-dot" class:timer={t.timer_enabled}></span>
        <span class="trig-name">{@html hl(t.name, query)}</span>
        {#if t.unsupported}
          <span class="bad" title="This regex uses features the app can't run">
            unsupported
          </span>
        {/if}
        <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-noninteractive-element-interactions -->
        <label
          class="sw"
          class:disabled={t.unsupported}
          title={t.unsupported
            ? "Unsupported regex — can't be enabled"
            : t.enabled
              ? "Disable trigger"
              : "Enable trigger"}
          on:click|stopPropagation
        >
          <input
            type="checkbox"
            checked={t.enabled && !t.unsupported}
            disabled={t.unsupported}
            on:change={(e) => onToggleEnable("trigger", t, e.target.checked)}
          />
          <span class="knob"></span>
        </label>
      </div>
    {/each}
  {/if}
</div>

<style>
  .node {
    display: flex;
    flex-direction: column;
  }
  .grp-row {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 12px;
    padding: 3px 6px;
    border-radius: 4px;
    width: 100%;
  }
  .grp-row:hover {
    background: rgba(255, 255, 255, 0.05);
  }
  .grp-row.dim .grp-name {
    color: var(--text-muted);
  }
  .caret {
    color: var(--text-muted);
    font-size: 10px;
    width: 10px;
    flex-shrink: 0;
  }
  .grp-name {
    font-weight: 600;
  }
  .cnt {
    color: var(--text-muted);
    font-size: 10px;
  }
  .trig-row {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-secondary);
    font-size: 12px;
    padding: 2px 6px 2px 16px;
    border-radius: 4px;
    cursor: context-menu;
  }
  .trig-row:hover {
    background: rgba(255, 255, 255, 0.04);
  }
  .trig-row.dim .trig-name {
    color: var(--text-muted);
  }
  .trig-row.hl {
    background: rgba(200, 169, 81, 0.18);
    color: var(--text-primary);
    outline: 1px solid var(--accent-dim);
  }
  .trig-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-muted);
    flex-shrink: 0;
  }
  .trig-dot.timer {
    background: var(--accent);
  }
  .grp-name :global(mark),
  .trig-name :global(mark) {
    background: rgba(200, 169, 81, 0.35);
    color: var(--text-primary);
    border-radius: 2px;
    padding: 0 1px;
  }
  .bad {
    color: #e05c5c;
    font-size: 9px;
    border: 1px solid rgba(224, 92, 92, 0.5);
    border-radius: 3px;
    padding: 0 4px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  /* on/off slider, floated right */
  .sw {
    position: relative;
    width: 26px;
    height: 14px;
    margin-left: auto;
    flex-shrink: 0;
    cursor: pointer;
  }
  .sw input {
    position: absolute;
    inset: 0;
    opacity: 0;
    margin: 0;
    cursor: pointer;
  }
  .sw .knob {
    position: absolute;
    inset: 0;
    background: var(--border);
    border-radius: 8px;
    transition: background 0.15s;
    pointer-events: none;
  }
  .sw .knob::after {
    content: "";
    position: absolute;
    top: 2px;
    left: 2px;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--text-secondary);
    transition:
      left 0.15s,
      background 0.15s;
  }
  .sw input:checked + .knob {
    background: rgba(200, 169, 81, 0.45);
  }
  .sw input:checked + .knob::after {
    left: 14px;
    background: var(--accent);
  }
  .sw.disabled {
    opacity: 0.3;
    pointer-events: none;
  }
</style>
