// EverQuest / Project 1999 three-letter class abbreviations.
// https://wiki.project1999.com/Classes
export const CLASS_ABBR = {
  bard: 'BRD',
  cleric: 'CLR',
  druid: 'DRU',
  enchanter: 'ENC',
  magician: 'MAG',
  monk: 'MNK',
  necromancer: 'NEC',
  paladin: 'PAL',
  ranger: 'RNG',
  rogue: 'ROG',
  'shadow knight': 'SHD',
  shaman: 'SHM',
  warrior: 'WAR',
  wizard: 'WIZ',
}

// classAbbr returns the 3-letter abbreviation for a class name. Returns '' for
// empty/unknown-but-non-class values (e.g. "Anon", "Role", "Unknown").
export function classAbbr(cls) {
  if (!cls) return ''
  return CLASS_ABBR[cls.toLowerCase()] || ''
}
