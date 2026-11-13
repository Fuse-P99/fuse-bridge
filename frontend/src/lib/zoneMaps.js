// Resolve an EverQuest zone display name (from "You have entered <name>." or
// /who) to a bundled map file base name (the keys in /maps/manifest.json).
//
// Most zones resolve by simple normalization (lowercase, drop "the", strip
// non-alphanumerics): "North Qeynos" -> "northqeynos", "Befallen" -> "befallen".
// Zones whose internal/map name differs from the display name need an explicit
// override below. Add entries here as you find gaps — unresolved zones simply
// show "no map bundled".

// Display name (lowercased) -> map base name. Extend as needed.
export const ZONE_OVERRIDES = {
  "the plane of sky": "airplane",
  "plane of sky": "airplane",
  "the plane of fear": "fear",
  "plane of fear": "fear",
  "the plane of hate": "hate",
  "plane of hate": "hate",
  "the plane of growth": "growthplane",
  "plane of growth": "growthplane",
  "plane of mischief": "mischiefplane",
  "the plane of mischief": "mischiefplane",
  "plane of air": "airplane",
  "plane of disease": "diseaseplane",
  "plane of justice": "justice",
  "plane of innovation": "innothuleb",
  "ocean of tears": "oot",
  "northern plains of karana": "northkarana",
  skyshrine: "skyshrine",
  "the nektulos forest": "nektulos",
  "sleepers tomb": "sleeper",
  "sleeper's tomb": "sleeper",
  erudin: "erudnext",
  "kedge keep": "kedge",
  "ak'anon": "akanon",
  "warsliks woods": "warslikswood",
  "castle mistmoore": "mistmoore",
  "castle of mistmoore": "mistmoore",
  "high keep": "highkeep",
  "highpass hold": "highpass",
  "qeynos aqueduct system": "qcat",
  "lake of ill omen": "lakeofillomen",
  "kael drakkel": "kael",
  "tower of frozen shadow": "frozenshadow",
  "icewell keep": "thurgadinb",
  "the feerrott": "feerrott",
  "ruins of sebilis": "sebilis",
  "old sebilis": "sebilis",
  "east commonlands": "ecommons",
  "cabilis east": "cabeast",
  "veeshan's peak": "veeshan",
  "surefall glade": "qrg",
  "innothule swamp": "innothule",
  halas: "halas",
  "domain of frost": "myriah",
  "solusek's eye": "soldunga",
  "estate of unrest": "unrest",
  blackburrow: "blackburrow",
  "gorge of king xorbb": "beholder",
  "plane of hate": "hateplane",
  "west commonlands": "commons",
  "north qeynos": "qeynos2",
  "cobalt scar": "cobaltscar",
  befallen: "befallen",
  paineel: "paineel",
  "north freeport": "freportn",
  "nagafen's lair": "soldungb",
  "runnyeye citadel": "runnyeye",
  "frontier mountains": "frontiermtns",
  "the city of mist": "citymist",
  "west freeport": "freportw",
  "butcherblock mountains": "butcher",
  "permafrost caverns": "permafrost",
  "the hole": "hole",
  "qeynos hills": "qeytoqrg",
  arena: "arena",
  "lavastorm mountains": "lavastorm",
  "plane of growth": "growthplane",
  "misty thicket": "misty",
  "city of thurgadin": "thurgadina",
  "northern desert of ro": "nro",
  "neriak foreign quarter": "neriaka",
  "neriak - foreign quarter": "neriaka",
  "infected paw": "paw",
  "lair of the splitpaw": "paw",
  "plane of air": "airplane",
  "southern felwithe": "felwitheb",
  "velketor's labyrinth": "velketor",
  "cabilis west": "cabwest",
  "lake rathetear": "lakerathe",
  "kurn's tower": "kurn",
  "oops, all icebones!": "towerfrost",
  "dagnor's cauldron": "cauldron",
  "western wastes": "westwastes",
  "temple of veeshan": "templeveeshan",
  "lesser faydark": "lfaydark",
  everfrost: "everfrost",
  "trakanon's teeth": "trakanon",
  "eastern plains of karana": "eastkarana",
  "north kaladim": "kaladimb",
  dreadlands: "dreadlands",
  "south qeynos": "qeynos",
  "plane of fear": "fearplane",
  "rathe mountains": "rathemtn",
  "the wakening lands": "wakening",
  "southern desert of ro": "sro",
  "the burning wood": "burningwood",
  "greater faydark": "gfaydark",
  "dragon necropolis": "necropolis",
  guk: "guktop",
  "the overthere": "overthere",
  "eastern wastelands": "eastwastes",
  "field of bone": "fieldofbone",
  "neriak third gate": "neriakc",
  "neriak - 3rd gate": "neriakc",
  "erud's crossing": "erudsxing",
  "northern felwithe": "felwithea",
  "firiona vie": "firiona",
  "east freeport": "freporte",
  "swamp of no hope": "swampofnohope",
  "timorous deep": "timorous",
  dalnir: "dalnir",
  "southern plains of karana": "southkarana",
  "western plains of karana": "qey2hh1",
  "skyfire mountains": "skyfire",
  "mines of nurga": "nurga",
  "oasis of marr": "oasis",
  "the emerald jungle": "emeraldjungle",
  "great divide": "greatdivide",
  greatdivide: "greatdivide",
  "sirens grotto": "sirens",
  "erudin palace": "erudnint",
  "toxxulia forest": "tox",
  "ruins of old guk": "gukbottom",
  "steamfont mountains": "steamfont",
  "south kaladim": "kaladima",
  najena: "najena",
  "stonebrunt mountains": "stonebrunt",
  "howling stones": "charasis",
  "kerra isle": "kerraridge",
  "lost temple of cazic-thule": "cazicthule",
  "lost temple of cazicthule": "cazicthule",
  "neriak - commons": "neriakb",
  "neriak commons": "neriakb",
  "karnor's castle": "karnor",
  "crystal caverns": "crystal",
  "iceclad ocean": "iceclad",
  warrens: "warrens",
  oggok: "oggok",
  grobb: "grobb",
  rivervale: "rivervale",
  "plane of mischief": "mischiefplane",
  kaesora: "kaesora",
  "temple of droga": "droga",
  crushbone: "crushbone",
  chardok: "chardok",
  "kithicor woods": "kithicor",
  "temple of solusek ro": "soltemple", // adjust if your pack names it differently
};

export function normalizeZone(name) {
  return (name || "")
    .toLowerCase()
    .replace(/^the\s+/, "")
    .replace(/[^a-z0-9]/g, "");
}

// resolveMapBase returns the manifest base key for a zone display name, or null.
// availableBases is the set/array of base names from manifest.json.
export function resolveMapBase(displayName, availableBases) {
  if (!displayName) return null;
  const bases =
    availableBases instanceof Set
      ? availableBases
      : new Set(availableBases || []);
  const lower = displayName.toLowerCase();

  // 1. explicit override
  const ov = ZONE_OVERRIDES[lower];
  if (ov && bases.has(ov)) return ov;

  // 2. normalized exact match
  const norm = normalizeZone(displayName);
  if (bases.has(norm)) return norm;

  // 3. with "the" kept (some zones include it in the file name)
  const normWithThe = lower.replace(/[^a-z0-9]/g, "");
  if (bases.has(normWithThe)) return normWithThe;

  // 4. prefix fallback: a bundled base that is a prefix of the normalized zone
  // name. Handles maps named by a short zone name and small spelling differences
  // (e.g. "Kael Drakkal" -> "kaeldrakkal" -> "kael"). Longest match wins; min
  // length 4 avoids spurious matches on very short bases.
  let best = "";
  for (const b of bases) {
    if (b.length >= 4 && norm.startsWith(b) && b.length > best.length) best = b;
  }
  if (best) return best;

  return null;
}
