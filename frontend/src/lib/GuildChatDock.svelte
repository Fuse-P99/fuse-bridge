<script>
  // The Guild Chat panel's BODY: the drag handle and the chat box under it,
  // sliding up out of the app footer above every tab. The opener, the gear and
  // the style popover live in the footer itself (GuildChatFooter.svelte) — a
  // closed panel is meant to cost nothing but the few characters in the footer,
  // so there is no header bar here to collapse. What the two halves share is in
  // lib/chatDock.js.
  //
  // App.svelte mounts this between the tab content and the footer, and the tab
  // content (min-height: 0) is what gives the box its room, so the footer never
  // leaves the window.
  import { onMount } from "svelte";
  import { slide } from "svelte/transition";
  import GuildChatPanel from "./GuildChatPanel.svelte";
  import { scale } from "./scale.js";
  import {
    chatEligible,
    chatOpen,
    chatH,
    chatNewest,
    chatStyle,
    bumpChatFlash,
    clampChatH,
    pushChatPrefs,
    saveChatH,
    setChatH,
  } from "./chatDock.js";

  // Draggable split between the tab content and the chat panel: the drag sets
  // the panel's height and the tab content (flex: 1, min-height: 0) soaks up
  // the rest, so an open panel takes its room from the tab and never pushes the
  // footer past the window.
  let chatDragging = false;
  let chatDragY = 0;
  let chatDragH = 220;
  function chatSplitStart(e) {
    chatDragging = true;
    chatDragY = e.clientY;
    chatDragH = $chatH;
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function chatSplitMove(e) {
    if (!chatDragging) return;
    // The shell is CSS-zoomed; pointer coords are screen-space (lib/scale.js).
    const dy = (e.clientY - chatDragY) / ($scale || 1);
    setChatH(chatDragH - dy);
  }
  function chatSplitEnd() {
    if (!chatDragging) return;
    chatDragging = false;
    saveChatH();
  }
  // Keyboard resize for the separator: up grows the panel, down shrinks it.
  function chatSplitKey(e) {
    const step = e.key === "ArrowUp" ? 20 : e.key === "ArrowDown" ? -20 : 0;
    if (!step) return;
    e.preventDefault();
    setChatH(clampChatH($chatH + step));
    saveChatH();
  }

  onMount(() => {
    pushChatPrefs(); // mirror the chat settings for the daily snapshot
  });
</script>

<!-- Eligibility gates the body as well as the opener: a member offboarded
     mid-session loses the box on the footer's next check, not at restart. -->
{#if $chatOpen && $chatEligible}
  <div class="gc-open" transition:slide|local={{ duration: 160 }}>
    <!-- Drag handle: resizes the tab content above against the chat below
         (also arrow-key adjustable when focused). This IS the ARIA focusable-
         separator widget pattern (role + tabindex + arrow keys); svelte's
         a11y tables just classify separator as non-interactive. -->
    <!-- svelte-ignore a11y-no-noninteractive-tabindex a11y-no-noninteractive-element-interactions -->
    <div
      class="gc-split"
      class:dragging={chatDragging}
      role="separator"
      aria-orientation="horizontal"
      aria-label="Resize guild chat"
      tabindex="0"
      on:pointerdown={chatSplitStart}
      on:pointermove={chatSplitMove}
      on:pointerup={chatSplitEnd}
      on:pointercancel={chatSplitEnd}
      on:keydown={chatSplitKey}
    ></div>
    <div class="gc-chat" style="height:{$chatH}px">
      <GuildChatPanel
        newestTop={$chatNewest}
        msgFont={$chatStyle.font}
        msgBold={$chatStyle.bold}
        msgSize={$chatStyle.size}
        msgColor={$chatStyle.color}
        on:newlines={bumpChatFlash}
      />
    </div>
  </div>
{/if}

<style>
  /* Never shrinks: the tab content above it is the flex item that gives way. */
  .gc-open {
    flex: 0 0 auto;
    display: flex;
    flex-direction: column;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
  }
  /* The chat's own height, set by the drag handle above it. */
  .gc-chat {
    flex: 0 0 auto;
    overflow: hidden;
  }
  /* Drag handle between the tab content and the chat, the Clients tab's
     splitter: an 8px grab strip with a 2px bar in it that brightens on
     hover/drag/focus. */
  .gc-split {
    flex-shrink: 0;
    height: 8px;
    margin: 3px 0;
    cursor: ns-resize;
    position: relative;
  }
  .gc-split::after {
    content: "";
    position: absolute;
    left: 0;
    right: 0;
    top: 3px;
    height: 2px;
    border-radius: 1px;
    background: var(--border);
  }
  .gc-split:hover::after,
  .gc-split.dragging::after,
  .gc-split:focus-visible::after {
    background: var(--accent-dim);
  }
  .gc-split:focus {
    outline: none;
  }
</style>
