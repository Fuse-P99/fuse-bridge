// Item hover card content, shared by every surface that shows an item — the
// Magelo sheet, the quest walkthrough (Characters tab + editor), raid loot,
// and the Search tab's Items and Raids panes. ItemTipCard.svelte renders
// these: tipStats() fills the boxed wiki-style stats panel, tipMoney() the
// DKP/quest pricing lines below it. Lives outside the components so every
// surface formats an item identically.

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
  return lines;
}

const fmtSaleDate = (ms) =>
  new Date(ms).toLocaleDateString([], {
    year: "numeric",
    month: "short",
    day: "numeric",
  });

// Money, not stats — two different kinds of fact about the same item, which
// is why the card draws them outside the boxed stats panel. Everything here
// is member-only enrichment: the server strips the fields for non-members,
// so an empty result IS the non-member view.
export function tipMoney(it) {
  const lines = [];

  // DKP purchase overview. Median and mean are both computed server-side
  // over the outlier-filtered sale set (the /item command's rule: with 5+
  // records, anything outside [med/5, med×5] isn't a price). An old server
  // sends no dkp_mean — fall back to restating the median rather than
  // printing "mean 0".
  if (it.dkp_count) {
    const tr = it.dkp_trend > 0 ? " ↑" : it.dkp_trend < 0 ? " ↓" : "";
    lines.push(
      `DKP: ${it.dkp_count} sale${it.dkp_count === 1 ? "" : "s"} · med ${it.dkp_median} · mean ${it.dkp_mean || it.dkp_median}` +
        tr,
    );
    if (it.dkp_per_year) lines.push(`Drops: ~${it.dkp_per_year}/year`);
    const recent = it.dkp_recent || [];
    if (recent.length) {
      lines.push("Recent sales:");
      for (const r of recent)
        lines.push(`${r.price} DKP — ${r.name} · ${fmtSaleDate(r.at_ms)}`);
    }
  }
  // Quest reward: it was handed out for components rather than auctioned, so
  // it has no DKP line of its own above. Price it by what went into it. The
  // server has already resolved each component through any turn-in chain, so
  // a component that is itself an earlier quest's reward carries that quest's
  // total — labelled as such rather than passed off as a sale price.
  // Quest inputs, when the item is a quest reward. Shown even when the item
  // has a DKP line of its own above: a class epic is uncontested and gets
  // recorded at a nominal 1 DKP, which says nothing about the ~650 DKP scale
  // that went into it. The two lines answer different questions.
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
    // The server sends one entry per SLOT, because that is what the quest
    // requires — a Velious breastplate takes three separate Flawless Diamonds,
    // unstacked. Three identical lines is noise, so they roll up into one with
    // a count, carrying the summed value so the Total still adds up.
    const rolled = [];
    const at = new Map();
    for (const c of shown) {
      const k = (c.name || "").toLowerCase();
      if (at.has(k)) {
        const r = rolled[at.get(k)];
        r.n += 1;
        r.value += c.value;
        continue;
      }
      at.set(k, rolled.length);
      rolled.push({ ...c, n: 1 });
    }
    for (const c of rolled) {
      lines.push(
        `${c.name}${c.n > 1 ? ` ×${c.n}` : ""} — ${c.value} DKP` +
          (c.dkp_count
            ? ` (${c.dkp_count} sale${c.dkp_count === 1 ? "" : "s"})`
            : " (from quest)"),
      );
    }
    // Only worth a total when it isn't just the line above restated — which a
    // single rolled-up entry is, since its value is already the sum.
    if (rolled.length > 1) {
      lines.push(`Total: ${it.quest_value} DKP`);
    }
  }
  return lines;
}
