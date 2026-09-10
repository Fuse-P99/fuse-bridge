// The app's icon set — every mark the UI draws that used to be an emoji.
// Emoji are out: Windows 10's Segoe UI Emoji stops at Emoji 12 (🪦 rendered as
// a box there) and the colored glyphs never matched the app's look. Each entry
// is a list of 24-viewBox stroked paths in the feather/lucide idiom (2px
// stroke, round caps), drawn in currentColor unless the entry carries its own
// color. Icon.svelte renders them in components; iconElement() builds the same
// SVG through DOM APIs for tooltip.js, which never uses innerHTML.
//
// Icons draw as small as 12px, where a 2-unit stroke is a single pixel. A
// feature that has to read at that size — a die's pips — goes in `dots`
// (filled discs of ICON_DOT_R at [cx, cy]) rather than as the hairline
// "M8 8h.01" dots the feather set uses, which vanish below ~20px.
//
// Add an icon here, never an emoji at a call site.

// The blue the 🌐 emoji had — the globe keeps its color where every other
// icon takes the text's.
const GLOBE_BLUE = "#4f9fe8";

export const ICONS = {
  // "Died" mark on parse rows and respawn bars.
  headstone: {
    paths: ["M6 20V10a6 6 0 0 1 12 0v10", "M3 20h18", "M9.5 11.5h5M9.5 15h5"],
  },
  // Shared with the guild / Fuse-wide.
  globe: {
    color: GLOBE_BLUE,
    paths: [
      "M22 12A10 10 0 1 1 2 12a10 10 0 0 1 20 0z",
      "M2 12h20",
      "M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z",
    ],
  },
  volume: {
    paths: [
      "M11 5L6 9H2v6h4l5 4V5z",
      "M15.54 8.46a5 5 0 0 1 0 7.07",
      "M19.07 4.93a10 10 0 0 1 0 14.14",
    ],
  },
  "volume-off": {
    paths: ["M11 5L6 9H2v6h4l5 4V5z", "M23 9l-6 6", "M17 9l6 6"],
  },
  clipboard: {
    paths: [
      "M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2",
      "M15 2H9a1 1 0 0 0-1 1v2a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1V3a1 1 0 0 0-1-1z",
    ],
  },
  check: { paths: ["M20 6L9 17l-5-5"] },
  x: { paths: ["M18 6L6 18", "M6 6l12 12"] },
  // Circle with a slash: not rostered / not allowed.
  ban: {
    paths: ["M22 12A10 10 0 1 1 2 12a10 10 0 0 1 20 0z", "M4.93 4.93l14.14 14.14"],
  },
  shield: { paths: ["M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"] },
  // Two crossed swords: a raid.
  swords: {
    paths: [
      "M14.5 17.5L3 6V3h3l11.5 11.5",
      "M13 19l6-6",
      "M16 16l4 4",
      "M19 21l2-2",
      "M14.5 6.5L18 3h3v3l-3.5 3.5",
      "M5 14l4 4",
      "M7 17l-3 3",
      "M3 19l2 2",
    ],
  },
  // A die: three pips on the diagonal. Filled discs, not stroked dots — see
  // the note on `dots` above; the randoms overlay draws this at 12px.
  dice: {
    paths: ["M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z"],
    dots: [
      [7.25, 7.25],
      [12, 12],
      [16.75, 16.75],
    ],
  },
  bolt: { paths: ["M13 2L3 14h9l-1 8 10-12h-9l1-8z"] },
  // Beacon light: the map strobe.
  siren: {
    paths: [
      "M7 18v-6a5 5 0 1 1 10 0v6",
      "M5 21a1 1 0 0 1-1-1v-1a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v1a1 1 0 0 1-1 1z",
      "M21 12h1",
      "M18.5 4.5L18 5",
      "M2 12h1",
      "M12 2v1",
      "M4.93 4.93l.7.7",
      "M12 12v6",
    ],
  },
  pin: {
    paths: [
      "M12 17v5",
      "M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z",
    ],
  },
  paperclip: {
    paths: [
      "M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48",
    ],
  },
  warning: {
    paths: [
      "M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z",
      "M12 9v4",
      "M12 17h.01",
    ],
  },
  // Pop out into an overlay.
  popout: {
    paths: [
      "M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6",
      "M15 3h6v6",
      "M10 14L21 3",
    ],
  },
  // What an unknown name draws, so a typo is visible rather than blank.
  missing: { paths: ["M4 4h16v16H4z", "M4 4l16 16"] },
};

// Radius of a `dots` disc, in viewBox units: about 2px at the 12px minimum,
// with the three-pip diagonal still leaving a pixel between pips.
export const ICON_DOT_R = 2.1;

// Named tones for icons that carry a state, so a "yes" and a "no" read at a
// glance without call sites inventing colors. App tokens, with the hex values
// style.css gives them as fallbacks.
export const ICON_TONES = {
  ok: "var(--success, #4caf50)",
  bad: "var(--error, #ef5350)",
  muted: "var(--text-muted, #4d5270)",
  accent: "var(--accent, #c8a951)",
};

// iconColor resolves what an icon draws in: an explicit color, else a named
// tone, else the icon's own color, else the surrounding text's.
export function iconColor(def, tone = "", color = "") {
  return color || ICON_TONES[tone] || def.color || "currentColor";
}

const SVG_NS = "http://www.w3.org/2000/svg";

// iconElement builds an icon as a DOM element — for code that composes the
// document by hand (the tooltip renderer) rather than through Svelte. Returns
// null for an unknown name so the caller can keep its text literal instead.
export function iconElement(name, tone = "") {
  const def = ICONS[name];
  if (!def) return null;
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", "fb-icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  // Stroke as a style property, not the attribute, so a var(--token) resolves.
  const color = iconColor(def, tone);
  svg.style.cssText = "width:1em;height:1em;vertical-align:-0.125em";
  svg.style.stroke = color;
  for (const d of def.paths) {
    const p = document.createElementNS(SVG_NS, "path");
    p.setAttribute("d", d);
    svg.appendChild(p);
  }
  for (const [cx, cy] of def.dots || []) {
    const c = document.createElementNS(SVG_NS, "circle");
    c.setAttribute("cx", String(cx));
    c.setAttribute("cy", String(cy));
    c.setAttribute("r", String(ICON_DOT_R));
    c.style.fill = color;
    c.style.stroke = "none";
    svg.appendChild(c);
  }
  return svg;
}
