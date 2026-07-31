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
  // Fuse-root-only XML round trip, officer-only. Left null for everyone else,
  // which is also what hides the buttons; children never render them (depth>0).
  export let onFuseExport = null; // () => void
  export let onFuseImport = null; // () => void
  // Audio mute for the Fuse subtree (null hides the buttons entirely).
  export let onToggleMute = null; // ("group"|"trigger", node|trigger) => void
  // Pop out the overlay a trigger feeds — the bridge between finding a timer
  // here and setting up where it shows. (null hides the buttons.)
  export let onPopoutCat = null; // ("timers"|"alerts", categoryName) => void
  // Window names of overlays currently open (Set, re-assigned each poll so
  // reactivity works). An already-open overlay's button disables and lights
  // up instead of opening a duplicate.
  export let popped = null;
  const isPopped = (kind, cat) =>
    !!popped && popped.has("popout-" + kind + "-" + cat);

  // Whether a trigger produces visible ALERT text (match, ending, or ended) —
  // decides if the "Alerts" popout button applies.
  const trigShowsAlert = (t) =>
    !!(
      (t.on_match?.use_text && (t.on_match?.display_text || "").trim()) ||
      (t.ending_enabled &&
        t.ending?.use_text &&
        (t.ending?.display_text || "").trim()) ||
      (t.ended_enabled &&
        t.ended?.use_text &&
        (t.ended?.display_text || "").trim())
    );

  // "Fuse Triggers" and "Personal" are the two headers this tree hangs off.
  // They're structural, not content, so they never dim — a partly-disabled
  // subtree shouldn't make the heading you navigate by hard to read.
  $: isRoot = depth === 0;
  // The Fuse root is the one top-level group that isn't Personal. 🌐 marks it as
  // guild-shared, matching the Fuse Shared Magelos button.
  $: isShared = isRoot && !node.personal;

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
    class:root={isRoot}
    class:dim={!node.enabled && !isRoot}
    role="button"
    tabindex="0"
    on:click={() => onToggle(node.id)}
    on:keydown={(e) => e.key === "Enter" && onToggle(node.id)}
    on:contextmenu|preventDefault={(e) => onMenu(e, "group", node)}
  >
    <span class="caret">{expanded[node.id] ? "▾" : "▸"}</span>
    <span class="grp-name">{@html hl(node.name, query)}</span>
    {#if isShared}
      <span class="shared" title="Shared with the guild">🌐</span>
    {/if}
    <!-- Fuse Triggers root only: the published revision this copy is based on,
         plus a badge while an officer's local edits await publishing. -->
    {#if node.version}
      <span class="ver" title="Published Fuse Triggers version"
        >(v{node.version})</span
      >
    {/if}
    {#if node.dirty}
      <span
        class="ver-dirty"
        title="Officer edits not yet published to the guild"
        >unpublished edits</span
      >
    {/if}
    {#if isShared && onFuseExport}
      <button
        class="io-btn"
        title="Save the Fuse Triggers set to an XML file you can edit"
        on:click|stopPropagation={onFuseExport}>Export XML</button
      >
      <button
        class="io-btn"
        title="Replace the Fuse Triggers set from an edited XML file"
        on:click|stopPropagation={onFuseImport}>Import XML</button
      >
    {/if}
    {#if node.total_triggers}
      <span
        class="cnt"
        title="{node.enabled_triggers} of {node.total_triggers} triggers enabled in this group"
        >{node.enabled_triggers}/{node.total_triggers}</span
      >
    {/if}
    {#if node.class_note}
      <span
        class="autonote"
        title="Each character automatically gets their own class's folder — the defaults don't decide this"
        >Class Auto-detected</span
      >
    {/if}
    <span class="acts">
      <!-- Group-level popout shortcuts: only when every categorized trigger
           beneath this group shares ONE category (uniform_* from the server).
           Mixed-category groups leave it to the per-trigger buttons. -->
      {#if onPopoutCat && node.uniform_category}
        {#if node.uniform_alerts}
          <button
            class="pop-btn"
            class:live={isPopped("alerts", node.uniform_category)}
            disabled={isPopped("alerts", node.uniform_category)}
            title={isPopped("alerts", node.uniform_category)
              ? `The “${node.uniform_category}” text alerts overlay is already popped out`
              : `Pop out the “${node.uniform_category}” text alerts overlay`}
            on:click|stopPropagation={() =>
              onPopoutCat("alerts", node.uniform_category)}
          >
            <svg
              viewBox="0 0 24 24"
              width="10"
              height="10"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path
                d="M10 6H6a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4"
              />
              <path d="M14 4h6v6" />
              <path d="M20 4 12 12" />
            </svg>
            Alerts</button
          >
        {/if}
        {#if node.uniform_timers}
          <button
            class="pop-btn"
            class:live={isPopped("timers", node.uniform_category)}
            disabled={isPopped("timers", node.uniform_category)}
            title={isPopped("timers", node.uniform_category)
              ? `The “${node.uniform_category}” timer bars overlay is already popped out`
              : `Pop out the “${node.uniform_category}” timer bars overlay`}
            on:click|stopPropagation={() =>
              onPopoutCat("timers", node.uniform_category)}
          >
            <svg
              viewBox="0 0 24 24"
              width="10"
              height="10"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path
                d="M10 6H6a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4"
              />
              <path d="M14 4h6v6" />
              <path d="M20 4 12 12" />
            </svg>
            Timers</button
          >
        {/if}
      {/if}
      <!-- Audio mute (Fuse subtree only): silences sounds/TTS for everything
           in this group while alerts and timer bars keep working. Sits left of
           the enable switch. Inherited mutes show dimmed — unmute at the
           ancestor. -->
      {#if onToggleMute && !node.personal}
        <button
          class="mute"
          class:on={node.muted_eff}
          class:inherited={node.muted_eff && !node.muted}
          title={node.muted_eff && !node.muted
            ? "Audio muted by a parent group — unmute it there (click to also mute this group)"
            : node.muted
              ? "Unmute this group's sounds and speech"
              : "Mute this group's sounds and speech (alerts and timer bars still show)"}
          on:click|stopPropagation={() => onToggleMute("group", node)}
          >{node.muted_eff ? "🔇" : "🔊"}</button
        >
      {/if}
      {#if node.class_auto}
        <span
          class="autonote"
          title="Inside the class-specific section — enabled automatically for characters of the matching class"
          >auto</span
        >
      {:else}
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
      {/if}
    </span>
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
        {onToggleMute}
        {onPopoutCat}
        {popped}
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
        {#if t.incomplete}
          <span class="todo" title={t.incomplete}>needs setup</span>
        {/if}
        <span class="acts">
          <!-- Popout shortcuts: jump straight from a trigger to the overlay
               it feeds. Only the relevant kinds show, and only with a
               category to open. -->
          {#if onPopoutCat && t.category}
            {#if trigShowsAlert(t)}
              <button
                class="pop-btn"
                class:live={isPopped("alerts", t.category)}
                disabled={isPopped("alerts", t.category)}
                title={isPopped("alerts", t.category)
                  ? `The “${t.category}” text alerts overlay is already popped out`
                  : `Pop out the “${t.category}” text alerts overlay`}
                on:click|stopPropagation={() => onPopoutCat("alerts", t.category)}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="10"
                  height="10"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path
                    d="M10 6H6a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4"
                  />
                  <path d="M14 4h6v6" />
                  <path d="M20 4 12 12" />
                </svg>
                Alerts</button
              >
            {/if}
            {#if t.timer_enabled}
              <button
                class="pop-btn"
                class:live={isPopped("timers", t.category)}
                disabled={isPopped("timers", t.category)}
                title={isPopped("timers", t.category)
                  ? `The “${t.category}” timer bars overlay is already popped out`
                  : `Pop out the “${t.category}” timer bars overlay`}
                on:click|stopPropagation={() => onPopoutCat("timers", t.category)}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="10"
                  height="10"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path
                    d="M10 6H6a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4"
                  />
                  <path d="M14 4h6v6" />
                  <path d="M20 4 12 12" />
                </svg>
                Timers</button
              >
            {/if}
          {/if}
          {#if onToggleMute && !node.personal}
            <button
              class="mute"
              class:on={t.muted_eff}
              class:inherited={t.muted_eff && !t.muted}
              title={t.muted_eff && !t.muted
                ? "Audio muted by a parent group — unmute it there (click to also mute this trigger)"
                : t.muted
                  ? "Unmute this trigger's sound and speech"
                  : "Mute this trigger's sound and speech (its alert and timer bar still show)"}
              on:click|stopPropagation={() => onToggleMute("trigger", t)}
              >{t.muted_eff ? "🔇" : "🔊"}</button
            >
          {/if}
          {#if t.class_auto}
            <span
              class="autonote"
              title="Inside the class-specific section — enabled automatically for characters of the matching class"
              >auto</span
            >
          {:else}
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
          {/if}
        </span>
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
  /* Section headers stay at full contrast whatever their children are doing. */
  .grp-row.root .grp-name {
    color: var(--text-primary);
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
  .ver {
    color: var(--text-muted);
    font-size: 10px;
    flex-shrink: 0;
  }
  .shared {
    font-size: 11px;
    line-height: 1;
    flex-shrink: 0;
  }
  /* Right-edge action cluster: [popouts] [mute] [enable switch], pushed to
     the row's end as one unit so ordering inside it is plain DOM order. */
  .acts {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
  }
  .acts .sw {
    margin-left: 0;
  }
  /* Popout shortcuts to the category overlay a trigger feeds. Hover-revealed
     like the mute speaker, so rows stay quiet until pointed at. */
  .pop-btn {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    background: none;
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 10px;
    line-height: 1;
    padding: 2px 5px;
    flex-shrink: 0;
    opacity: 0;
    transition: opacity 0.1s;
  }
  .grp-row:hover .pop-btn,
  .trig-row:hover .pop-btn {
    opacity: 1;
  }
  .pop-btn:hover:not(:disabled) {
    color: var(--text-primary);
    border-color: var(--accent-dim);
  }
  /* Already popped out: always visible, accent-lit, and inert — the state IS
     the indicator, so it doesn't wait for hover. */
  .pop-btn.live {
    opacity: 1;
    color: var(--accent);
    border-color: var(--accent-dim);
    cursor: default;
  }
  /* Defaults-editor markers: the class section's note, and the "auto" chip
     that replaces sliders inside it. */
  .autonote {
    color: var(--text-muted);
    font-size: 10px;
    font-style: italic;
    flex-shrink: 0;
    cursor: help;
  }
  /* Audio mute toggle, immediately left of the enable switch. Quiet until
     relevant: unmuted speakers only show on hover; a mute always shows. */
  .mute {
    background: none;
    border: none;
    cursor: pointer;
    font-size: 11px;
    line-height: 1;
    padding: 0 2px;
    flex-shrink: 0;
    opacity: 0;
    filter: grayscale(1);
    transition: opacity 0.1s;
  }
  .grp-row:hover .mute,
  .trig-row:hover .mute,
  .mute.on {
    opacity: 1;
  }
  .mute.on {
    filter: none;
  }
  /* Muted by an ancestor rather than here — shown, but visibly secondary. */
  .mute.inherited {
    opacity: 0.45;
  }
  /* Sits inline with the group title, so it stays quiet until hovered. */
  .io-btn {
    background: transparent;
    border: 1px solid var(--border);
    color: var(--text-muted);
    border-radius: 3px;
    font-size: 9px;
    font-family: inherit;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 5px;
    cursor: pointer;
    flex-shrink: 0;
  }
  .io-btn:hover {
    color: var(--text-primary);
    border-color: var(--accent-dim);
    background: rgba(255, 255, 255, 0.06);
  }
  .ver-dirty {
    color: var(--accent);
    font-size: 9px;
    border: 1px solid var(--accent-dim);
    border-radius: 3px;
    padding: 0 4px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    flex-shrink: 0;
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
  /* Grey, not red: the trigger isn't broken, it just can't show anything yet. */
  .todo {
    color: var(--text-muted);
    font-size: 9px;
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    cursor: help;
    flex-shrink: 0;
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
