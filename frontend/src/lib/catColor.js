// Timer-category palette — complementary to the gold/dark theme (--accent
// #c8a951). Shared by the Timers tab and the popout overlays so a category
// keeps the same color everywhere. Colors are assigned by stable name-hash.
export const PALETTE = [
  "#c8a951", // gold (accent)
  "#4fb3a9", // teal
  "#6b9bd1", // steel blue
  "#a58fd6", // violet
  "#d1706b", // brick
  "#7fb069", // moss
  "#d19a5b", // amber
  "#c67fb0", // rose
  "#5bbcd1", // cyan
  "#a9b05f", // olive
];

// Keep in sync with paletteColor() in eq-relay/triggers_categories.go — the Go
// side resolves configured styles and defaults a category's color the same way,
// so a mismatch would make colors jump once the style loads.
export function catColor(name) {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

// rgba("#4fb3a9", 0.5) -> "rgba(79,179,169,0.5)". Category styles store color
// and opacity separately so each can be edited on its own.
export function rgba(hex, opacity) {
  const a = Math.max(0, Math.min(1, Number(opacity) || 0));
  const m = /^#?([0-9a-f]{6})$/i.exec(hex || "");
  if (!m) return `rgba(0,0,0,${a})`;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
}
