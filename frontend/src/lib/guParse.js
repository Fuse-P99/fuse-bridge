// One-line guild-chat summary of a parse, shaped like the lines GamParse and
// similar parsers paste, because that is the shape raiders already read at a
// glance in /gu:
//
//   /GU Tunare in 394s, 688k @1746 | Gambel 30322@(83 in 362s) | ...
//
// Shared by ParseDialog (live/completed raid card) and search/ParseDetail
// (archived parse) so the same fight copies to the same text from either one.
//
// The number in the parentheses is SDPS (damage over the WHOLE fight) and the
// list is sorted by it. The reference program prints engaged-seconds DPS
// there; the owner chose SDPS on purpose so the copied line ranks people the
// same way our table's Rank column does.

const MAX_ENTRIES = 10;

const int = (n) => Math.round(Number(n) || 0);

// Raid totals run to six or seven digits; "688k" keeps the header short enough
// that the player entries, which are the point, still fit in one chat line.
function shortTotal(n) {
  const v = int(n);
  return v >= 1000 ? Math.round(v / 1000) + "k" : String(v);
}

export function guParseLine({ mob, secs, total, raidDps, rows }) {
  const name = (mob || "").trim() || "Unknown";
  // "/GU " up front so pasting the line sends it to guild chat, not /say.
  let line = `/GU ${name} in ${int(secs)}s, ${shortTotal(total)} @${int(raidDps)}`;

  // The rows are what the dialog already lists (the server applied the
  // engaged-share rule), so the copied line matches the table. Zero-damage
  // rows have nothing to say in chat.
  const top = (rows || [])
    .filter((r) => Number(r.total) > 0)
    .slice()
    .sort(
      (a, b) =>
        (Number(b.sdps) || 0) - (Number(a.sdps) || 0) ||
        (Number(b.total) || 0) - (Number(a.total) || 0) ||
        String(a.name || "").localeCompare(String(b.name || "")),
    )
    .slice(0, MAX_ENTRIES);

  // No thousands separators: EQ chat players read these as raw numbers and
  // commas would look like list separators inside the entry.
  for (const r of top) {
    line += ` | ${r.name} ${int(r.total)}@(${int(r.sdps)} in ${int(r.engaged_s)}s)`;
  }
  return line;
}
