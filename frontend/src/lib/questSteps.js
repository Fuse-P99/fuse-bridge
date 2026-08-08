// Shared quest-step rendering: the pure functions that turn a QuestStep into
// the walkthrough token list ("Give X to Y in Z … to receive W"). Extracted
// from QuestEditor.svelte so the per-character Quests sub-tab renders steps
// exactly the way the editor does — one implementation, two screens.

export function stepIns(s) {
  return (s.items || []).filter((i) => i.role !== "out");
}
export function stepOuts(s) {
  return (s.items || []).filter((i) => i.role === "out");
}
// Every item that satisfies one slot, joined — "Yelinak's Talisman or
// Lendiniara's Talisman or …".
export function slotNames(i) {
  return [i.name, ...(i.alts || [])];
}
// Slots asking for the same thing, rolled into one entry with a count.
// Hand-ins take unstacked items, so a Velious breastplate is three separate
// Flawless Diamond slots — correct in the data, and "X + X + X" on screen.
//
// Keyed on the whole slot, alternatives and returned-flag included: two slots
// are only the same requirement if either would be satisfied the same way.
export function rollIns(s) {
  const out = [];
  const at = new Map();
  for (const it of stepIns(s)) {
    const key =
      slotNames(it).join("|").toLowerCase() + "|" + (it.consumed_ok === false);
    if (at.has(key)) {
      out[at.get(key)].n += 1;
      continue;
    }
    at.set(key, out.length);
    out.push({ it, n: 1 });
  }
  return out;
}
// Where the mobs are, when they agree on a zone. Loot steps usually name
// several mobs in one zone, and repeating it per mob is noise.
export function mobZone(s) {
  const zones = [...new Set((s.mobs || []).map((m) => m.zone).filter(Boolean))];
  return zones.length === 1 ? zones[0] : "";
}

// The single spot a step happens at, or null.
//
// Deliberately null whenever there is more than one candidate: several mobs
// can drop the item, or the one mob wanders and has no recorded spot. A map
// marker asserts "it is HERE", and drawing one of four possible places — or
// one point for a mob that roams the zone — would be a guess dressed as a
// fact. No marker is the honest answer; the zone name still tells you where
// to go.
export function stepPoint(s) {
  if (s.has_loc) return { y: s.loc_y, x: s.loc_x, what: "here" };
  const located = (s.mobs || []).filter((m) => m.has_loc);
  if ((s.mobs || []).length === 1 && located.length === 1) {
    return { y: located[0].loc_y, x: located[0].loc_x, what: located[0].name };
  }
  return null;
}

// stepLine flattens a step into one readable sentence, as a token list the
// template renders: connective words stay dim, while the things you act on —
// NPCs, zones, items, say-lines — are high-visibility and keep their
// interactive elements (item tooltips, click-to-copy). "Talk to Konia
// Swiftfoot in Western Karana and say '…' to receive Torch of Misty."
export function stepLine(s) {
  const seg = [];
  const outs = stepOuts(s);
  const zone = mobZone(s) || s.zone_name || s.zone_id;
  const pt = stepPoint(s);
  const mobs = (s.mobs || []).map((m) => m.name);
  const text = (t) => seg.push({ t: "text", s: t });
  const sep = (t) => seg.push({ t: "sep", s: t });
  const item = (name, out) => seg.push({ t: "item", name, out });
  const where = () => {
    if (zone) {
      text("in");
      seg.push({ t: "zone", name: zone, pt });
    }
  };
  const mobTok = () => seg.push({ t: "mob", names: mobs });
  const ins = () => {
    rollIns(s).forEach((row, k) => {
      if (k) sep("+");
      slotNames(row.it).forEach((nm, ai) => {
        if (ai) sep("or");
        item(nm);
      });
      if (row.n > 1) seg.push({ t: "mult", n: row.n });
      if (row.it.consumed_ok === false) seg.push({ t: "ret" });
    });
    if (s.plat_cost) {
      if (stepIns(s).length) sep("+");
      seg.push({ t: "plat", n: s.plat_cost });
    }
  };
  const receive = (verb) => {
    if (!outs.length) return;
    text(verb);
    outs.forEach((o, k) => {
      if (k) sep("+");
      item(o.name, true);
    });
  };
  const says = () => s.say && seg.push({ t: "say" });

  switch (s.kind) {
    case "dialogue":
      if (mobs.length) {
        text("Talk to");
        mobTok();
        where();
        if (s.say) {
          text("and say");
          says();
        }
      } else {
        text("Say");
        says();
        where();
      }
      receive("to receive");
      break;
    case "handin":
      text("Give");
      ins();
      text("to");
      mobTok();
      where();
      if (s.say) {
        text("saying");
        says();
      }
      receive("to receive");
      break;
    case "loot":
      text("Kill");
      mobTok();
      where();
      receive("and loot");
      break;
    case "combine": {
      text("Combine");
      ins();
      if (s.tradeskill || s.skill_req) {
        seg.push({
          t: "skill",
          s: `${s.tradeskill || "no-fail"}${s.skill_req ? " " + s.skill_req : ""}`,
        });
      }
      receive("to make");
      break;
    }
    case "acquire": {
      const verb =
        {
          ground: "Pick up",
          forage: "Forage",
          fish: "Fish up",
          purchase: "Buy",
          pickpocket: "Pickpocket",
        }[s.method] || "Acquire";
      text(verb);
      outs.forEach((o, k) => {
        if (k) sep("+");
        item(o.name, true);
      });
      if (mobs.length) {
        text("from");
        mobTok();
      } else if (s.method === "ground") {
        text("from the ground");
      }
      where();
      if (s.plat_cost) {
        text("for");
        seg.push({ t: "plat", n: s.plat_cost });
      }
      break;
    }
    default:
      receive("Receive");
  }
  if (s.faction_level) {
    seg.push({ t: "gate", s: `${s.faction_level} with ${s.faction_group}` });
  }
  return seg;
}

// What to call a step. Acquire is a family rather than a single action, so it
// shows the specific one — "Pickpocket", "Purchase" — and falls back to the
// bare kind only when the method wasn't recorded.
export const KIND_LABEL = {
  handin: "Hand In",
  combine: "Tradeskill combine",
  loot: "Loot",
  acquire: "Acquire",
  dialogue: "Dialogue",
};
export const METHOD_LABEL = {
  ground: "Ground spawn",
  forage: "Forage",
  fish: "Fish",
  purchase: "Purchase",
  pickpocket: "Pickpocket",
};
export function kindLabel(s) {
  if (s.kind === "acquire" && METHOD_LABEL[s.method]) {
    return METHOD_LABEL[s.method];
  }
  return KIND_LABEL[s.kind] || s.kind;
}

export function rewardText(r) {
  if (r.kind === "faction") {
    return `${r.faction_delta > 0 ? "+" : ""}${r.faction_delta} ${r.faction_group}`;
  }
  if (r.kind === "cycle") return (r.cycle || []).join(" → ");
  return r.name;
}

// A dialogue step is often a whole conversation, written as one field with
// the replies separated by an arrow: "I will take it → What task? → Agreed".
// Those are typed into the game ONE AT A TIME, so the chain is what to show
// and a single line is what to copy — copying the joined string hands over
// something no NPC will ever answer to.
//
// "->" is accepted alongside "→" because it's what gets typed when a quest is
// entered by hand.
export function sayLines(text) {
  return String(text || "")
    .split(/\s*(?:→|->)\s*/)
    .map((s) => s.trim())
    .filter(Boolean);
}
