// The Guild Chat dock's state, in one place because the dock is now in two.
// GuildChatFooter.svelte draws the opener inside the app footer and owns the
// gear popover; GuildChatDock.svelte draws the panel body above the footer.
// Neither contains the other, so what they share — open/closed, the box's
// height, the reading order, the text style, the popover's own open state and
// the new-line flash — lives here as stores instead of props.
//
// Persistence lives here too: everything a user chooses is written to the
// browser store the moment they choose it, since none of it survives a restart
// otherwise.
import { get, writable } from "svelte/store";
import {
  IsMemberVerified,
  SetOverlayPrefs,
} from "../../bindings/FuseBridge/app.js";
import { linked } from "./linkState.js";

const OPEN_KEY = "fuse.chat.open";
const H_KEY = "fuse.chat.h";
const NEWEST_KEY = "fuse.chat.newest";
const STYLE_KEY = "fuse.chat.style";

export const CHAT_MIN = 100;
export const CHAT_MAX = 600;
// What a color input shows while nothing is set. It cannot render "unset" — it
// would show black and read as a choice — so it previews the color the messages
// actually get, which is white.
export const CHAT_MSG_COLOR = "#ffffff";

export const clampChatH = (v) =>
  Math.min(CHAT_MAX, Math.max(CHAT_MIN, Math.round(v)));

// Size is 0 (the panel's own 12.5px) or a legible 8-40px; the in-between sizes
// are nobody's intent, so a typed 3 becomes 8 rather than three-pixel guild
// chat. The same rule the overlay gear panels apply.
export function clampMsgSize(v) {
  const n = Math.round(Number(v) || 0);
  if (n <= 0) return 0;
  return Math.max(8, Math.min(40, n));
}

// Ten lines of chat AT THE CURRENT TEXT SIZE: each row is line-height 1.35 of
// the font size plus the 2px row gap, and the box adds 6px of padding top and
// bottom. All CSS px — the shell's UI scale zooms the whole window on top
// (lib/scale.js), so the panel must never measure itself against the screen (no
// vh) or a Large UI would get ten lines' worth of pixels showing six lines of
// text.
const tenLineH = (size) => Math.round(10 * ((size || 12.5) * 1.35 + 2) + 12);

function read(key, dflt) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? dflt : v;
  } catch {
    // Storage blocked or unavailable — the defaults stand for this session.
    return dflt;
  }
}
function save(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* quota — the setting still applies for this session */
  }
}

let style = { font: "", bold: false, size: 0, color: "" };
try {
  const st = JSON.parse(read(STYLE_KEY, "{}") || "{}") || {};
  style = {
    font: typeof st.font === "string" ? st.font : "",
    bold: !!st.bold,
    size: clampMsgSize(st.size),
    color: typeof st.color === "string" ? st.color : "",
  };
} catch {
  /* a corrupt blob — the defaults stand */
}

// A height the user has actually chosen. Until there is one the panel opens to
// ten lines and tracks the text size; the moment they drag the splitter their
// height wins and stops following it.
let hSaved = false;
let height = tenLineH(style.size);
{
  const v = parseInt(read(H_KEY, ""), 10);
  if (!isNaN(v)) {
    height = clampChatH(v);
    hSaved = true;
  }
}

// Whether this install may have the feature at all: a LINKED client whose
// token the server still honours. An unlinked install has no feed to read, and
// an offboarded one — a former member whose token the removal cascade revoked —
// must not be shown so much as the opener: the guild's chat is not theirs any
// more. False until the first check answers, so nothing flashes into view and
// back out on a fresh start. refreshChatEligibility re-asks; the footer calls
// it on mount, whenever the linked state flips, and on a slow timer so a
// revocation lands within a minute without a restart.
export const chatEligible = writable(false);
export async function refreshChatEligibility() {
  let ok = false;
  try {
    ok = get(linked) && !!(await IsMemberVerified());
  } catch {
    ok = false; // the server could not be asked — fail closed
  }
  chatEligible.set(ok);
  return ok;
}

export const chatOpen = writable(read(OPEN_KEY, "0") === "1");
export const chatH = writable(clampChatH(height));
export const chatNewest = writable(read(NEWEST_KEY, "0") === "1");
export const chatStyle = writable(style);
// The gear popover. Not persisted: a popover is a gesture, not a setting.
export const chatStyleOpen = writable(false);
// New lines arrived while the reader was scrolled away from the live edge.
// Bumping this swaps which of the two identical flash classes the opener wears,
// and a CHANGED animation-name is what makes the browser run the animation
// again — removing and re-adding one class inside a single frame does not.
// Back-to-back messages therefore each pulse.
export const chatFlash = writable(0);
// Is the open panel sitting at the live edge? False means the reader has
// scrolled back through history and is NOT looking at what is being said now,
// which the footer says out loud (a pulse every couple of seconds, and a steady
// mark for anyone who has asked for less motion). True whenever the panel is
// closed or freshly snapped — nobody is being misled then.
export const chatLive = writable(true);
// Bumped by the footer to ask the panel to jump back to the live edge. A
// counter rather than a flag because the same request can be made twice, and
// the panel acts on the change.
export const chatSnapSeq = writable(0);

// SETTINGS TELEMETRY (CLAUDE.md): the view toggle and the text style join the
// daily snapshot the way an overlay's gear settings do, under the kind
// "chatpanel" with no category and no character — the panel is one box in the
// main window, not a per-character overlay. Pushed on mount and on every
// change, since Go cannot read the browser store itself.
//
// The style keys travel as themselves only for msgbold; the font, the color and
// the size are reduced to "is it customised at all" on the Go side
// (sanitizeOverlayPrefs in settings_snapshot.go), so which font or which color
// was picked never leaves the machine.
export function pushChatPrefs() {
  const s = get(chatStyle);
  SetOverlayPrefs(
    "chatpanel",
    "",
    JSON.stringify({
      newesttop: get(chatNewest),
      msgfont: s.font,
      msgbold: s.bold,
      msgsize: s.size,
      msgcolor: s.color,
    }),
    "",
  ).catch(() => {});
}

export function toggleChat() {
  const open = !get(chatOpen);
  chatOpen.set(open);
  save(OPEN_KEY, open ? "1" : "0");
  // A closed panel has no text to style, and leaving the popover armed would
  // pop it open again with the panel. It is not "scrolled away from live"
  // either — there is nothing on screen to be away from — and it opens at the
  // newest line, so the footer's not-live mark goes with it.
  if (!open) {
    chatStyleOpen.set(false);
    chatLive.set(true);
  }
}

export function setChatNewest(v) {
  chatNewest.set(!!v);
  save(NEWEST_KEY, v ? "1" : "0");
  pushChatPrefs();
}

// setChatH moves the box without writing anything down — it runs on every
// pointer move of a drag. saveChatH is the commit, and it is also what promotes
// the height above the ten-line default for good.
export function setChatH(v) {
  chatH.set(clampChatH(v));
}
export function saveChatH() {
  hSaved = true;
  save(H_KEY, String(get(chatH)));
}

// saveChatStyle takes the next style object whole (one identity per change) and
// writes it down. While no height has been chosen by hand, the ten-line default
// follows the text size — a bigger font gets a taller box, not six lines.
export function saveChatStyle(next) {
  style = { ...next };
  chatStyle.set(style);
  save(STYLE_KEY, JSON.stringify(style));
  if (!hSaved) chatH.set(clampChatH(tenLineH(style.size)));
  pushChatPrefs();
}

export function bumpChatFlash() {
  chatFlash.update((n) => n + 1);
}

// requestChatSnap asks the open panel to scroll back to the live edge. The
// "live" flag is set here rather than waiting for the panel's own scroll event,
// so the pulsing stops on the click that asked for it.
export function requestChatSnap() {
  chatLive.set(true);
  chatSnapSeq.update((n) => n + 1);
}
