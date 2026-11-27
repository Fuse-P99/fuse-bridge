<script>
  // The Guild Chat opener, in the middle of the app footer. Closed, this is the
  // whole feature on screen: a few characters of gold, rather than a full-width
  // header bar spent on saying that a box is shut. Open, it gains the gear that
  // opens the style popover, and the panel body slides up above the footer
  // (GuildChatDock.svelte). State is shared through lib/chatDock.js.
  //
  // The flash lives here too: a line arriving while the reader is scrolled away
  // pulses this control, which is on screen on every tab.
  import { onDestroy, onMount } from "svelte";
  import Icon from "./Icon.svelte";
  import { FONTS } from "./fonts.js";
  import { linked } from "./linkState.js";
  import {
    CHAT_MSG_COLOR,
    bumpChatFlash,
    chatEligible,
    chatFlash,
    chatLive,
    chatNewest,
    chatOpen,
    chatStyle,
    chatStyleOpen,
    clampMsgSize,
    refreshChatEligibility,
    requestChatSnap,
    saveChatStyle,
    setChatNewest,
    toggleChat,
  } from "./chatDock.js";

  // Members only. The opener — the whole feature, while closed — exists solely
  // for a linked client the server still recognises; an unlinked install or an
  // offboarded member (token revoked by the removal cascade) never sees it. The
  // linked store flips the instant linking or unlinking happens here; the timer
  // is for the other direction, a revocation made in Discord while the app is
  // running, so it lands within a minute rather than at the next restart.
  const ELIGIBLE_RECHECK_MS = 60000;
  let eligTimer;
  let unsubLinked;
  onMount(() => {
    // subscribe fires with the current value at once, which is the first check.
    unsubLinked = linked.subscribe(() => {
      refreshChatEligibility();
    });
    eligTimer = setInterval(refreshChatEligibility, ELIGIBLE_RECHECK_MS);
  });

  // Scrolled back through history with the chat still open: the reader is not
  // seeing what is being said now, and the panel will not drag them back to it
  // (being yanked mid-sentence is worse). So the control keeps saying so —
  // one pulse every couple of seconds for as long as it lasts, on top of the
  // single pulse each new line already gives. Clicking it goes back to live.
  const NOT_LIVE_PULSE_MS = 2000;
  let notLiveTimer;
  $: notLive = $chatEligible && $chatOpen && !$chatLive;
  $: {
    clearInterval(notLiveTimer);
    if (notLive) notLiveTimer = setInterval(bumpChatFlash, NOT_LIVE_PULSE_MS);
  }
  onDestroy(() => {
    clearInterval(notLiveTimer);
    clearInterval(eligTimer);
    if (unsubLinked) unsubLinked();
  });

  // The control's click has two jobs, and which one it is doing is exactly what
  // the pulsing says: while the reader is away from the live edge it takes them
  // back there (closing the panel they are reading would be the wrong answer to
  // "you are behind"), and otherwise it opens and closes the panel.
  function onOpenClick() {
    if (notLive) requestChatSnap();
    else toggleChat();
  }

  function setChatSize(e) {
    const size = clampMsgSize(e.target.value);
    e.target.value = size; // show what was actually taken
    saveChatStyle({ ...$chatStyle, size });
  }
  // "" is the default, not a color: it leaves the stylesheet's white in charge
  // and keeps the snapshot's "custom color set" honest.
  function resetChatColor() {
    saveChatStyle({ ...$chatStyle, color: "" });
  }
  // Clicking anywhere else in the app puts the popover away. The listener is
  // the window's, because this control is on screen on every tab and the click
  // that dismisses it can land anywhere. Two clicks are not "elsewhere": one
  // inside the popover is the user working its controls, and the gear's own
  // click is the toggle that opened it.
  function onWindowClick(e) {
    if (!$chatStyleOpen) return;
    const t = e && e.target;
    if (t && t.closest && (t.closest(".gc-pop") || t.closest(".gc-gear")))
      return;
    chatStyleOpen.set(false);
  }
</script>

<svelte:window on:click={onWindowClick} />

<!-- Nothing at all for an install that may not read the feed (see the
     eligibility note in the script): not a disabled control, not a hint. -->
{#if $chatEligible}
<!-- The popover is a SIBLING of the buttons inside this wrapper, which is what
     it anchors to: it opens UPWARD, since the control it belongs to is at the
     very bottom of the window. -->
<div
  class="gc-foot"
  class:flash-a={$chatFlash > 0 && $chatFlash % 2 === 1}
  class:flash-b={$chatFlash > 0 && $chatFlash % 2 === 0}
>
  {#if $chatOpen}
    <!-- The app's own gear glyph, as the Timers tab writes it: a text
         character, never an emoji (WebView2 would draw a color one from the
         OS font). -->
    <button
      class="gc-gear"
      class:on={$chatStyleOpen}
      title="Chat settings: order, font, bold, size and color"
      aria-label="Chat settings"
      aria-expanded={$chatStyleOpen}
      on:click|stopPropagation={() => chatStyleOpen.set(!$chatStyleOpen)}
      >⚙</button
    >
  {/if}
  <button
    class="gc-openbtn"
    class:notlive={notLive}
    aria-expanded={$chatOpen}
    title={notLive
      ? "New messages below — jump back to live chat"
      : $chatOpen
        ? "Close guild chat"
        : "Open guild chat"}
    on:click={onOpenClick}
  >
    <span class="gc-title">Guild Chat</span>
    {#if notLive}
      <!-- The steady half of "you are not looking at live chat". The pulse
           carries it for most people; this dot is what is left when the
           animation is suppressed for reduced motion, so the CSS shows it
           only there. -->
      <span class="gc-livedot" aria-hidden="true"></span>
    {/if}
    <span class="gc-chev"
      ><Icon name={$chatOpen ? "chevron-down" : "chevron-up"} /></span
    >
  </button>

  {#if $chatOpen && $chatStyleOpen}
    <div class="gc-pop">
      <div class="gc-poprow">
        <span class="gc-poplabel">Order</span>
        <label
          class="gc-popcheck"
          title="Put the newest message at the top instead of the bottom"
        >
          <input
            type="checkbox"
            checked={$chatNewest}
            on:change={(e) => setChatNewest(e.currentTarget.checked)}
          />
          Newest first
        </label>
      </div>
      <div class="gc-poprow">
        <span class="gc-poplabel">Font</span>
        <select
          class="gc-popsel"
          aria-label="Chat font"
          value={$chatStyle.font}
          on:change={(e) =>
            saveChatStyle({ ...$chatStyle, font: e.currentTarget.value })}
        >
          {#each FONTS as f (f.v)}
            <option value={f.v}>{f.label}</option>
          {/each}
        </select>
        <label class="gc-popcheck">
          <input
            type="checkbox"
            checked={$chatStyle.bold}
            on:change={(e) =>
              saveChatStyle({ ...$chatStyle, bold: e.currentTarget.checked })}
          />
          Bold
        </label>
      </div>
      <div class="gc-poprow">
        <span class="gc-poplabel">Text size</span>
        <!-- on:change, not on:input: clamping per keystroke would rewrite a
             half-typed "1" of "12" into the 8px floor. -->
        <input
          class="gc-popnum"
          type="number"
          min="0"
          max="40"
          aria-label="Chat text size"
          value={$chatStyle.size}
          on:change={setChatSize}
        />
        <span class="gc-pophint">0 = default (12.5)</span>
      </div>
      <div class="gc-poprow">
        <span class="gc-poplabel">Color</span>
        <!-- on:input previews while the picker is open — a bare store set,
             which repaints the chat and writes nothing — and on:change is what
             writes it down, so dragging through a hundred shades is one save
             and one telemetry push. -->
        <input
          class="gc-popcolor"
          type="color"
          aria-label="Chat message color"
          value={$chatStyle.color || CHAT_MSG_COLOR}
          on:input={(e) =>
            chatStyle.set({ ...$chatStyle, color: e.currentTarget.value })}
          on:change={(e) =>
            saveChatStyle({ ...$chatStyle, color: e.currentTarget.value })}
        />
        <button class="gc-popbtn" on:click={resetChatColor}>Default</button>
        <span class="gc-pophint">message text only</span>
      </div>
    </div>
  {/if}
</div>
{/if}

<style>
  .gc-foot {
    position: relative;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    /* The footer's own nowrap/clip rules apply around this; the control is a
       fixed few characters wide and never part of the clipping. */
    border-radius: 3px;
    padding: 0 4px;
  }
  /* New lines while the reader is scrolled away: one short gold pulse of the
     control. Two identical animations, alternated by the counter's parity — the
     animation-name has to CHANGE for the browser to run it again, so
     back-to-back messages each pulse instead of the second being swallowed. */
  .gc-foot.flash-a {
    animation: gc-pulse-a 700ms ease-out;
  }
  .gc-foot.flash-b {
    animation: gc-pulse-b 700ms ease-out;
  }
  @keyframes gc-pulse-a {
    0% {
      background: transparent;
      box-shadow: none;
    }
    25% {
      background: rgba(227, 160, 8, 0.22);
      box-shadow: inset 0 0 0 1px #e3a008;
    }
    100% {
      background: transparent;
      box-shadow: none;
    }
  }
  @keyframes gc-pulse-b {
    0% {
      background: transparent;
      box-shadow: none;
    }
    25% {
      background: rgba(227, 160, 8, 0.22);
      box-shadow: inset 0 0 0 1px #e3a008;
    }
    100% {
      background: transparent;
      box-shadow: none;
    }
  }
  /* Someone who has asked for less motion is not shown a pulsing control — but
     "you are not looking at live chat" is information, not decoration, so with
     the animation gone the steady dot takes over saying it. */
  @media (prefers-reduced-motion: reduce) {
    .gc-foot.flash-a,
    .gc-foot.flash-b {
      animation: none;
    }
    .gc-openbtn.notlive .gc-livedot {
      display: inline-block;
    }
  }
  /* Hidden by default: while the control can pulse, the pulse is the message. */
  .gc-livedot {
    display: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: #e3a008;
    flex-shrink: 0;
  }
  /* The opener wears the .group-title treatment in the app's gold accent, the
     same label style the panel's header bar used to. */
  .gc-openbtn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: none;
    border: none;
    padding: 1px 2px;
    color: var(--accent);
    cursor: pointer;
    font-family: inherit;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    white-space: nowrap;
  }
  .gc-openbtn:hover .gc-title {
    color: #ffd45e;
  }
  .gc-openbtn:focus-visible {
    outline: 1px solid var(--accent-dim);
    outline-offset: 1px;
  }
  .gc-chev {
    display: flex;
    align-items: center;
    color: var(--text-muted);
    font-size: 13px;
  }
  .gc-openbtn:hover .gc-chev {
    color: var(--accent);
  }
  /* The gear, in the Timers tab's plain-glyph style: a quiet mark that
     brightens on hover and stays gold while its popover is open. */
  .gc-gear {
    background: none;
    border: none;
    padding: 0 2px;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 12px;
    line-height: 1;
  }
  .gc-gear:hover,
  .gc-gear.on {
    color: var(--accent);
  }

  /* The style popover, anchored above the control and centred on it. Always
     upward: the footer is the bottom of the window. */
  .gc-pop {
    position: absolute;
    bottom: 100%;
    left: 50%;
    transform: translateX(-50%);
    margin-bottom: 6px;
    z-index: 60;
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px 10px;
    background: var(--bg-panel);
    border: 1px solid var(--accent-dim);
    border-radius: 5px;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.55);
    /* The opener's uppercase gold is a label style, not a form style. */
    color: var(--text-secondary);
    font-size: 11px;
    font-weight: 400;
    letter-spacing: 0;
    text-transform: none;
    white-space: nowrap;
    cursor: default;
  }
  .gc-poprow {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .gc-poplabel {
    width: 62px;
    flex: none;
    color: var(--text-muted);
  }
  .gc-popsel,
  .gc-popnum {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-primary);
    font-size: 11px;
    padding: 2px 4px;
  }
  .gc-popnum {
    width: 56px;
  }
  .gc-popcheck {
    display: flex;
    align-items: center;
    gap: 4px;
    cursor: pointer;
  }
  .gc-popcolor {
    width: 34px;
    height: 20px;
    padding: 0;
    background: none;
    border: 1px solid var(--border);
    border-radius: 3px;
    cursor: pointer;
  }
  .gc-popbtn {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 10px;
    padding: 2px 8px;
  }
  .gc-popbtn:hover {
    color: var(--text-primary);
  }
  .gc-pophint {
    color: var(--text-muted);
    font-size: 10px;
    font-style: italic;
  }
</style>
