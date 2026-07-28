// Item tooltip lines, shared by the Magelo sheet and the quest walkthrough.
// Takes a MageloItem as the server returns it and produces wiki-style stat
// lines. Lives outside MageloView so the quest editor can show the same card
// for a reward without duplicating the formatting.

export function tipStats(it) {
  // Wiki-style stat lines assembled from the DB record.
  const lines = [];
  const flags = [
    it.magic && "MAGIC ITEM",
    it.lore && "LORE ITEM",
    it.nodrop && "NO DROP",
    it.norent && "NO RENT",
  ].filter(Boolean);
  if (flags.length) lines.push(flags.join("  "));
  if (it.slot) lines.push("Slot: " + it.slot);
  if (it.skill) lines.push("Skill: " + it.skill + "  Atk Delay: " + it.delay);
  if (it.dmg) lines.push("DMG: " + it.dmg + (it.ac ? "  AC: " + it.ac : ""));
  else if (it.ac) lines.push("AC: " + it.ac);
  const s = [];
  for (const [k, v] of [
    ["STR", it.str],
    ["STA", it.sta],
    ["AGI", it.agi],
    ["DEX", it.dex],
    ["WIS", it.wis],
    ["INT", it.int],
    ["CHA", it.cha],
    ["HP", it.hp],
    ["MANA", it.mana],
  ])
    if (v) s.push(`${k}: ${v > 0 ? "+" : ""}${v}`);
  if (s.length) lines.push(s.join("  "));
  const sv = [];
  for (const [k, v] of [
    ["SV FIRE", it.sv_fire],
    ["SV COLD", it.sv_cold],
    ["SV DISEASE", it.sv_disease],
    ["SV POISON", it.sv_poison],
    ["SV MAGIC", it.sv_magic],
  ])
    if (v) sv.push(`${k}: ${v > 0 ? "+" : ""}${v}`);
  if (sv.length) lines.push(sv.join("  "));
  if (it.effect) lines.push("Effect: " + it.effect);
  if (it.capacity)
    lines.push(
      `Capacity: ${it.capacity}  Size Capacity: ${it.size_capacity}` +
        (it.wr ? `  WR: ${it.wr}%` : ""),
    );
  lines.push(`WT: ${(it.wt || 0).toFixed(1)}  Size: ${it.size || "?"}`);
  if (it.classes) lines.push("Class: " + it.classes);
  if (it.races) lines.push("Race: " + it.races);
  // DKP purchase overview (server fills this for linked lookups only):
  // "DKP: 12 sales · med 150 · last 175 (Jun 2026) ↑"
  if (it.dkp_count) {
    const tr = it.dkp_trend > 0 ? " ↑" : it.dkp_trend < 0 ? " ↓" : "";
    lines.push(
      `DKP: ${it.dkp_count} sale${it.dkp_count === 1 ? "" : "s"} · med ${it.dkp_median} · last ${it.dkp_last}` +
        (it.dkp_last_at ? ` (${it.dkp_last_at})` : "") +
        tr,
    );
  }
  // Quest reward: it was handed out for components rather than auctioned, so
  // it has no DKP line of its own above. Price it by what went into it. The
  // server has already resolved each component through any turn-in chain, so
  // a component that is itself an earlier quest's reward carries that quest's
  // total — labelled as such rather than passed off as a sale price.
  // Quest inputs, when the item is a quest reward and the server actually
  // priced them (it skips the walk entirely for an item with its own sale
  // history — that price, printed above, is the better answer).
  //
  // quest_items is the SHOPPING LIST: what the quest needs from OUTSIDE, with
  // everything its own steps produce already netted out server-side. It is
  // not the step list — a twenty-step epic usually needs two or three things.
  //
  // Deliberately no quest name, class, step count, faction, coin or route
  // count: this block answers "what would this have cost me", and the rest is
  // what the editor and the wiki link are for. Plenty of gear has no DKP
  // price at all, and showing nothing is the right answer when we know
  // nothing.
  if (it.quest_routes && it.quest_priced) {
    const comps = it.quest_items || [];
    const shown = comps.filter((c) => c.value > 0);
    if (shown.length) {
      for (const c of shown) {
        lines.push(
          `${c.name} — ${c.value} DKP` +
            (c.dkp_count
              ? ` (${c.dkp_count} sale${c.dkp_count === 1 ? "" : "s"})`
              : " (from quest)"),
        );
      }
      // Only worth a total when it isn't just the line above restated.
      if (shown.length > 1) {
        lines.push(`Total: ${it.quest_value} DKP`);
      }
    }
  }
  return lines;
}
