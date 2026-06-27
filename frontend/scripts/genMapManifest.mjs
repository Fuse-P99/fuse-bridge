// Generates public/maps/manifest.json from the map .txt files present in
// public/maps. Runs automatically before `vite build` (see package.json), so the
// manifest always matches whatever map files are bundled. Keyed by lowercase base
// name (for case-insensitive zone resolution) with the real-case filename base
// preserved so fetches work on case-sensitive hosts.
import { readdirSync, writeFileSync, existsSync, mkdirSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath } from 'url'

const mapsDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'public', 'maps')
if (!existsSync(mapsDir)) mkdirSync(mapsDir, { recursive: true })

const m = {}
for (const f of readdirSync(mapsDir)) {
  if (!f.toLowerCase().endsWith('.txt')) continue
  const mt = f.match(/^(.*)_(\d+)\.txt$/i)
  if (!mt) continue
  const base = mt[1]
  const layer = parseInt(mt[2], 10)
  const key = base.toLowerCase()
  if (!m[key]) m[key] = { base, layers: [] }
  if (!m[key].layers.includes(layer)) m[key].layers.push(layer)
}
for (const k in m) m[k].layers.sort((a, b) => a - b)

writeFileSync(join(mapsDir, 'manifest.json'), JSON.stringify(m))
console.log(`map manifest: ${Object.keys(m).length} zones`)
