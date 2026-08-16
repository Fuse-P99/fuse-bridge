// Classic (Velious-era) P99 character math.
//
// Single home for the derived-stat formulas so the magelo character panel and
// the Fuse Library cards can never drift apart — they used to carry separate
// copies of the HP and mana math.
//
// Tables cross-checked against Vanifac's P99 character calculator
// (docs.google.com/spreadsheets/d/1vsBL7dopif5JwIfGP-on_2cWPHYF5evPSJs1DKz83Og):
// all 76 race/class base-stat combos and all 14 creation-point budgets match,
// as do the HP multiplier bands for every archetype. Its one disagreement with
// us is mana (see MANA_STAT_OFFSET) and its own notes flag mana as unresolved,
// so ours — calibrated against live characters — stays.

export const STAT_NAMES = ["STR", "STA", "AGI", "DEX", "WIS", "INT", "CHA"];
// Item/buff field names, parallel to STAT_NAMES.
export const GKEY = ["str", "sta", "agi", "dex", "wis", "int", "cha"];
export const RESIST_NAMES = ["FIRE", "COLD", "DISEASE", "POISON", "MAGIC"];
// Resist field names on gear and on buffs, parallel to RESIST_NAMES. The gear
// one is exported so views can show each save's item contribution beside the
// total, the way GKEY drives the same badge on the attribute rows.
export const RESIST_GEAR_KEY = ["fire", "cold", "disease", "poison", "magic"];
const RESIST_BUFF_KEY = ["svf", "svc", "svd", "svp", "svm"];

// Race base stats from wiki.project1999.com/Character_Races.
export const RACE_BASE = {
  human: [75, 75, 75, 75, 75, 75, 75],
  erudite: [60, 70, 70, 70, 83, 107, 70],
  barbarian: [103, 95, 82, 70, 70, 60, 55],
  halfling: [70, 75, 95, 90, 80, 67, 50],
  dwarf: [90, 90, 70, 90, 83, 60, 45],
  gnome: [60, 70, 85, 85, 67, 98, 60],
  "half elf": [70, 70, 90, 85, 60, 75, 75],
  "wood elf": [65, 65, 95, 80, 80, 75, 75],
  "high elf": [55, 65, 85, 70, 95, 92, 80],
  "dark elf": [60, 65, 90, 75, 83, 99, 60],
  ogre: [130, 122, 70, 70, 67, 60, 37],
  troll: [108, 109, 83, 75, 60, 52, 40],
  iksar: [70, 70, 90, 85, 80, 75, 55],
};

// Classic class creation bonuses [STR,STA,AGI,DEX,WIS,INT,CHA] + point budget.
export const CLASS_BONUS = {
  warrior: { s: [10, 10, 5, 0, 0, 0, 0], pts: 25 },
  cleric: { s: [5, 5, 0, 0, 10, 0, 0], pts: 30 },
  paladin: { s: [10, 5, 0, 0, 5, 0, 10], pts: 20 },
  ranger: { s: [5, 10, 10, 0, 5, 0, 0], pts: 20 },
  "shadow knight": { s: [10, 5, 0, 0, 0, 10, 5], pts: 20 },
  druid: { s: [0, 10, 0, 0, 10, 0, 0], pts: 30 },
  monk: { s: [5, 5, 10, 10, 0, 0, 0], pts: 20 },
  bard: { s: [5, 0, 0, 10, 0, 0, 10], pts: 25 },
  rogue: { s: [0, 0, 10, 10, 0, 0, 0], pts: 30 },
  shaman: { s: [0, 5, 0, 0, 10, 0, 5], pts: 30 },
  necromancer: { s: [0, 0, 0, 10, 0, 10, 0], pts: 30 },
  wizard: { s: [0, 10, 0, 0, 0, 10, 0], pts: 30 },
  magician: { s: [0, 10, 0, 0, 0, 10, 0], pts: 30 },
  enchanter: { s: [0, 0, 0, 0, 0, 10, 10], pts: 30 },
};

// Every character starts with these resists regardless of race or class:
// MR/FR/CR 25, PR/DR 15. Confirmed from the calculator, which reports them for
// a naked High Elf (a race with no racial modifiers) both at level 60 and with
// the level field blank — so they're flat, not level-scaled. Warriors add an
// innate level-scaled MR on top; see innateResists.
// [FIRE, COLD, DISEASE, POISON, MAGIC]
export const BASE_RESIST = [25, 25, 15, 15, 25];

// Warriors alone gain innate magic resistance as they level: +1 MR every 2
// levels. No other class has an innate resist gain and no other resist scales,
// so this is one term rather than a class x resist table — if that ever stops
// being true, this is the function to widen.
//
// NOTE (unresolved): floor(level/2) gives 30 at level 60, but a measured level
// 60 warrior read 194 in game against 171 in the app — a gap of 23, not 30. The
// documented rule is implemented here rather than the measurement, because
// fitting the constant to one reading would make level 60 right by accident and
// every other level wrong. The residual 7 is more likely an input error (a
// mis-scraped item's MR, or a stale race — dwarf and erudite both carry +5 MR)
// than a different rate. Recheck against a warrior at another level before
// touching this.
const WARRIOR_MR_LEVELS_PER_POINT = 2;

// innateResists returns the class's own level-scaled resist contribution,
// parallel to RESIST_NAMES. Zeroes for everyone but warriors.
export function innateResists(cls, level) {
  const out = [0, 0, 0, 0, 0];
  if ((cls || "").toLowerCase() === "warrior" && level > 0) {
    out[4] = Math.floor(level / WARRIOR_MR_LEVELS_PER_POINT); // index 4 = MAGIC
  }
  return out;
}

// Racial resist modifiers layered on top of BASE_RESIST. Races absent here
// have none. [FIRE, COLD, DISEASE, POISON, MAGIC]
export const RACE_RESIST = {
  barbarian: [0, 10, 0, 0, 0],
  erudite: [0, 0, -5, 0, 5],
  halfling: [0, 0, 5, 5, 0],
  dwarf: [0, 0, 0, 5, 5],
  iksar: [5, -10, 0, 0, 0],
  troll: [-20, 0, 0, 0, 0],
};

// Which stat drives a class's mana pool. WIS classes are the priests plus
// paladin and ranger; INT classes are the four INT casters plus shadow knight
// and bard.
export const CASTER_WIS = ["cleric", "druid", "shaman", "paladin", "ranger"];
export const CASTER_INT = [
  "necromancer",
  "wizard",
  "magician",
  "enchanter",
  "shadow knight",
  "bard",
];

// Hybrids have no mana pool at all until level 9, when their first spell
// gems open up. The calculator's per-class mana columns are blank for
// levels 1-8 and start at 9.
export const HYBRID_MANA_LEVEL = 9;
const HYBRIDS = ["paladin", "ranger", "shadow knight", "bard"];

// Era hard cap: every attribute and resist tops out at 255. Overcap is wasted —
// the capped value is what feeds HP/mana and every other derived number, so the
// cap is applied at the totals level.
export const cap255 = (v) => Math.min(255, v);

// Returns the index into STAT_NAMES of a class's casting stat, or -1 when the
// class has no mana at all.
export function manaStatIndex(cls) {
  const c = (cls || "").toLowerCase();
  if (CASTER_WIS.includes(c)) return 4;
  if (CASTER_INT.includes(c)) return 5;
  return -1;
}

// HP = 5 + mult×level + STA×level×mult/300, with these class/level-banded
// multipliers. The bands reproduce the wiki's STA→HP tables and match the
// calculator's full level 1-60 table exactly for every archetype.
export function hpMultiplier(c, l) {
  if (c === "warrior")
    return l < 20
      ? 22
      : l < 30
        ? 23
        : l < 40
          ? 25
          : l < 53
            ? 27
            : l < 57
              ? 28
              : l < 60
                ? 29
                : 30;
  if (c === "paladin" || c === "shadow knight")
    return l < 35
      ? 21
      : l < 45
        ? 22
        : l < 51
          ? 23
          : l < 56
            ? 24
            : l < 60
              ? 25
              : 26;
  if (c === "ranger") return l < 58 ? 20 : 21;
  if (c === "monk" || c === "rogue" || c === "bard")
    return l < 51 ? 18 : l < 58 ? 19 : 20;
  if (c === "cleric" || c === "druid" || c === "shaman") return 15;
  return 12; // int casters
}

// Haste cap by level (wiki Haste_Guide): worn + spell haste stack, but the
// total is clamped. No overhaste in this era.
export function hasteCap(l) {
  return l >= 60 ? 100 : l >= 55 ? 94 : l >= 51 ? 84 : l >= 31 ? 74 : 50;
}

// Empirical p99 calibration against live characters:
//   L1  DE cleric     WIS 93            → 21
//   L1  Iksar shaman  WIS 90            → 21
//   L13 gnome wizard  INT 118           → 336
//   L52 halfling druid WIS 110          → 1267
//   L52 halfling druid WIS 124, +80 item → 1484
// All five are exact ONLY with round() (not floor — the Iksar and the wizard
// bracket every floor offset out). Each observation constrains the offset to a
// band; the two level-52 points are by far the tightest and their intersection
// with the wizard's is [19.39, 19.49], so 19.44 sits mid-band and hits every
// point on the nose. The wiki page's bare rate (offset 0, floor) reads
// consistently short.
//
// The one holdout is a L49 ranger (WIS 103, +125 gear → 1253), which wants
// ~19.3 and reads 1 high here. Rangers are hybrids and hybrids demonstrably
// follow a different mana curve — they have no pool at all until level 9 (see
// HYBRID_MANA_LEVEL) — so they shouldn't constrain the pure-caster fit. If
// hybrid mana matters later it needs its own rate, not a compromised offset.
export const MANA_STAT_OFFSET = 19.44;

// Casting stat above this contributes at a reduced rate. The 50% we use for the
// reduced band is the unverified part: the P99 wiki's piecewise formula implies
// 33/85 ≈ 38.8% instead, but its below-200 branch is measurably wrong — it can't
// produce the observed 1267 at level 52 for any integer WIS, and its slope
// (85/425 per level) misses the 80/425 the two level-52 measurements bracket —
// so its above-200 branch isn't imported on faith. One measurement from a 200+
// WIS/INT character would settle it.
export const MANA_SOFTCAP = 200;

/**
 * Derived stats for one character.
 *
 * @param {object}   o
 * @param {string}   o.cls       class name, any case
 * @param {string}   o.race      race name, any case
 * @param {number}   o.level
 * @param {number[]} [o.assigned] user-assigned creation points, parallel to
 *                                STAT_NAMES (omit for shared library entries —
 *                                they don't travel with a snapshot)
 * @param {object}   [o.gear]    summed worn gear: GKEY fields plus hp, mana and
 *                               the RESIST_GEAR_KEY fields
 * @param {object}   [o.buff]    summed active buffs: same, but resists use the
 *                               RESIST_BUFF_KEY (svf/svc/…) names
 * @returns {{totals: number[], hasBase: boolean, hp: number, mana: number,
 *            resists: [string, number][]}}
 *          mana is -1 for classes that never have mana, so callers can render
 *          "—" rather than a misleading 0.
 */
export function characterStats({
  cls = "",
  race = "",
  level = 0,
  assigned = null,
  gear = {},
  buff = {},
} = {}) {
  const c = (cls || "").toLowerCase();
  const rc = (race || "").toLowerCase();
  const base = RACE_BASE[rc] || null;
  const bonus = CLASS_BONUS[c] || null;
  const g = (k) => gear[k] || 0;
  const b = (k) => buff[k] || 0;

  const totals = STAT_NAMES.map((_, i) =>
    cap255(
      (base ? base[i] : 0) +
        (bonus ? bonus.s[i] : 0) +
        (assigned ? assigned[i] || 0 : 0) +
        g(GKEY[i]) +
        b(GKEY[i]),
    ),
  );

  const mult = hpMultiplier(c, level);
  const hp =
    level && c
      ? Math.floor(5 + mult * level + ((totals[1] || 0) * level * mult) / 300) +
        g("hp") +
        b("hp")
      : 0;

  const mIdx = manaStatIndex(c);
  let mana = -1;
  if (mIdx >= 0 && level) {
    if (HYBRIDS.includes(c) && level < HYBRID_MANA_LEVEL) {
      mana = 0; // no pool yet, so gear/buff mana is inert too
    } else {
      // The softcap applies to the casting stat itself, so MANA_STAT_OFFSET —
      // a per-level base term, not stat points — stays outside the Math.min.
      // Folding it in tripped the cap at a raw stat of 180.56 and understated a
      // raid-geared level 60 caster by ~80 mana.
      const stat = totals[mIdx] || 0;
      mana =
        Math.round(
          ((80 * level) / 425) *
            (MANA_STAT_OFFSET + Math.min(stat, MANA_SOFTCAP)) +
            ((40 * level) / 425) * Math.max(0, stat - MANA_SOFTCAP),
        ) +
        g("mana") +
        b("mana");
    }
  }

  const rr = RACE_RESIST[rc] || [0, 0, 0, 0, 0];
  const ir = innateResists(c, level);
  const resists = RESIST_NAMES.map((n, i) => [
    n,
    cap255(
      BASE_RESIST[i] +
        rr[i] +
        ir[i] +
        g(RESIST_GEAR_KEY[i]) +
        b(RESIST_BUFF_KEY[i]),
    ),
  ]);

  return { totals, hasBase: !!base, hp, mana, resists };
}
