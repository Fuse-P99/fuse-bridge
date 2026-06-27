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
  'the plane of sky': 'airplane',
  'plane of sky': 'airplane',
  'the plane of fear': 'fear',
  'plane of fear': 'fear',
  'the plane of hate': 'hate',
  'plane of hate': 'hate',
  'the plane of growth': 'growthplane',
  'plane of growth': 'growthplane',
  'plane of mischief': 'mischiefplane',
  'the plane of mischief': 'mischiefplane',
  'plane of air': 'airplane',
  'plane of disease': 'diseaseplane',
  'plane of justice': 'justice',
  'plane of innovation': 'innothuleb', // adjust if your pack names it differently
}

export function normalizeZone(name) {
  return (name || '')
    .toLowerCase()
    .replace(/^the\s+/, '')
    .replace(/[^a-z0-9]/g, '')
}

// resolveMapBase returns the manifest base key for a zone display name, or null.
// availableBases is the set/array of base names from manifest.json.
export function resolveMapBase(displayName, availableBases) {
  if (!displayName) return null
  const bases = availableBases instanceof Set ? availableBases : new Set(availableBases || [])
  const lower = displayName.toLowerCase()

  // 1. explicit override
  const ov = ZONE_OVERRIDES[lower]
  if (ov && bases.has(ov)) return ov

  // 2. normalized exact match
  const norm = normalizeZone(displayName)
  if (bases.has(norm)) return norm

  // 3. with "the" kept (some zones include it in the file name)
  const normWithThe = lower.replace(/[^a-z0-9]/g, '')
  if (bases.has(normWithThe)) return normWithThe

  return null
}
