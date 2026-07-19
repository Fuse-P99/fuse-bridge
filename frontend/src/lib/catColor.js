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

export function catColor(name) {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}
