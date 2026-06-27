# Zone map files

The Map tab loads zone maps from this folder at runtime: `/maps/<base>_<layer>.txt`,
plus `manifest.json` (base zone name → available layer numbers).

Drop the zone map `.txt` files directly in this folder as `<base>_<layer>.txt`
(e.g. `befallen_1.txt`). `manifest.json` is **generated automatically** at build
time (`npm run build` / `wails build` runs `scripts/genMapManifest.mjs`), so you
never edit it by hand — just add/remove map files and rebuild.

Map file format (standard EQ maps):
- `L x1,y1,z1,x2,y2,z2,r,g,b` — a line segment
- `P x,y,z,r,g,b,size,label` — a labeled point

To add genuine multi-floor layers (EqTool's set is single-layer), drop extra
`<base>_2.txt` / `<base>_3.txt` files (Brewall/Good's maps, same format) here and
re-run the script to refresh the manifest.
