// The font families the app offers wherever a surface lets the user dress its
// text. The list is deliberately short and made of families Windows always
// has: a picked font has to render on every install, and a missing one would
// silently fall back to something else.
//
// Popout.svelte (the overlay gear panels) and TriggersTab.svelte (the trigger
// category editor) each carry their own copy of this list, written before
// there was a module to share. This is the shared one — new surfaces import
// it, and those two keep their copies until someone has reason to touch them.
//
// Each entry is { v, label }: `v` is the CSS font-family value, written into a
// style attribute as-is, and the empty one means "inherit whatever this
// surface's own font is".
export const FONTS = [
  { v: "", label: "Default (app font)" },
  { v: "Segoe UI, sans-serif", label: "Segoe UI" },
  { v: "Arial, sans-serif", label: "Arial" },
  { v: "Verdana, sans-serif", label: "Verdana" },
  { v: "Tahoma, sans-serif", label: "Tahoma" },
  { v: "Georgia, serif", label: "Georgia" },
  { v: "Times New Roman, serif", label: "Times New Roman" },
  { v: "Consolas, monospace", label: "Consolas" },
  { v: "Courier New, monospace", label: "Courier New" },
  { v: "Impact, sans-serif", label: "Impact" },
];
