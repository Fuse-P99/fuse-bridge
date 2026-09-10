<script>
  // One inline SVG from the app icon set (icons.js). It sizes with the text it
  // sits beside (1em) and takes that text's color, unless the icon carries its
  // own (the globe stays blue), the caller names a tone (ok/bad/muted/accent)
  // or passes a color — applied as a style property, not the stroke attribute,
  // so a var(--token) resolves. A title makes it an image with a tooltip;
  // without one it's decoration the wrapper labels.
  import { ICONS, iconColor } from "./icons.js";

  export let name;
  export let title = "";
  export let size = "1em";
  export let tone = "";
  export let color = "";

  $: def = ICONS[name] || ICONS.missing;
  $: stroke = iconColor(def, tone, color);
</script>

<svg
  class="icon"
  viewBox="0 0 24 24"
  fill="none"
  stroke-width="2"
  stroke-linecap="round"
  stroke-linejoin="round"
  style="width:{size};height:{size};stroke:{stroke}"
  role={title ? "img" : "presentation"}
  aria-label={title || undefined}
  aria-hidden={title ? undefined : "true"}
>
  {#if title}<title>{title}</title>{/if}
  {#each def.paths as d}<path {d} />{/each}
</svg>

<style>
  .icon {
    vertical-align: -0.125em;
    flex: none;
  }
</style>
