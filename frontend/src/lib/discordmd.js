// Minimal Discord-flavored markdown parser for in-app display of guide posts.
// Produces plain data (blocks of styled segments) that DiscordMessage.svelte
// renders through normal Svelte text nodes — no {@html}, so nothing a post
// author writes can inject markup. Supports the constructs guild posts
// actually use: headers, quotes, bullets, code fences, inline code, bold /
// italic / underline / strikethrough, masked links and bare URLs. Nesting
// styles inside each other is not supported (single-level, like the rest of
// the app's needs).

// One combined scanner; order matters (longer markers first).
const INLINE_RX =
  /(`[^`\n]+`)|(\*\*[^*\n]+\*\*)|(__[^_\n]+__)|(~~[^~\n]+~~)|(\*[^*\n]+\*)|(_[^_\n]+_)|\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)|<(https?:\/\/[^\s<>]+)>|(https?:\/\/[^\s<>]+)/g;

/** Parse one line into segments: {t, txt, url?} where t is one of
 *  text|code|bold|italic|under|strike|link. */
export function parseInline(line) {
  const segs = [];
  let last = 0;
  for (const m of line.matchAll(INLINE_RX)) {
    if (m.index > last) segs.push({ t: "text", txt: line.slice(last, m.index) });
    if (m[1]) segs.push({ t: "code", txt: m[1].slice(1, -1) });
    else if (m[2]) segs.push({ t: "bold", txt: m[2].slice(2, -2) });
    else if (m[3]) segs.push({ t: "under", txt: m[3].slice(2, -2) });
    else if (m[4]) segs.push({ t: "strike", txt: m[4].slice(2, -2) });
    else if (m[5]) segs.push({ t: "italic", txt: m[5].slice(1, -1) });
    else if (m[6]) segs.push({ t: "italic", txt: m[6].slice(1, -1) });
    else if (m[7]) segs.push({ t: "link", txt: m[7], url: m[8] });
    else if (m[9]) segs.push({ t: "link", txt: m[9], url: m[9] });
    else if (m[10]) segs.push({ t: "link", txt: m[10], url: m[10] });
    last = m.index + m[0].length;
  }
  if (last < line.length) segs.push({ t: "text", txt: line.slice(last) });
  return segs;
}

/** Parse a message into blocks: {type: p|h1|h2|h3|quote|li|code|gap, segs?, text?}.
 *  Discord renders single newlines literally, so each line is its own block;
 *  blank lines become small gaps. */
export function parseBlocks(text) {
  const blocks = [];
  const lines = String(text || "")
    .replace(/\r/g, "")
    .split("\n");
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = line.match(/^```(\w*)\s*$/) || (line.startsWith("```") && [line]);
    if (fence) {
      // Code fence: capture until the closing ``` (or end of message).
      const buf = [];
      const first = line.replace(/^```\w*\s*/, "");
      if (first && !line.match(/^```(\w*)\s*$/)) buf.push(first.replace(/```$/, ""));
      if (!line.endsWith("```") || line === "```" || line.match(/^```\w+$/)) {
        i++;
        for (; i < lines.length; i++) {
          if (lines[i].trimEnd().endsWith("```")) {
            const tail = lines[i].trimEnd().slice(0, -3);
            if (tail) buf.push(tail);
            break;
          }
          buf.push(lines[i]);
        }
      }
      blocks.push({ type: "code", text: buf.join("\n") });
      continue;
    }
    if (!line.trim()) {
      blocks.push({ type: "gap" });
      continue;
    }
    let m;
    if ((m = line.match(/^(#{1,3})\s+(.*)$/))) {
      blocks.push({ type: "h" + m[1].length, segs: parseInline(m[2]) });
    } else if ((m = line.match(/^>\s?(.*)$/))) {
      blocks.push({ type: "quote", segs: parseInline(m[1]) });
    } else if ((m = line.match(/^\s*[-*]\s+(.*)$/))) {
      blocks.push({ type: "li", segs: parseInline(m[1]) });
    } else if ((m = line.match(/^\s*(\d+)\.\s+(.*)$/))) {
      blocks.push({ type: "li", num: m[1], segs: parseInline(m[2]) });
    } else {
      blocks.push({ type: "p", segs: parseInline(line) });
    }
  }
  // Collapse runs of gaps and drop leading/trailing ones.
  const out = [];
  for (const b of blocks) {
    if (b.type === "gap" && (!out.length || out[out.length - 1].type === "gap"))
      continue;
    out.push(b);
  }
  while (out.length && out[out.length - 1].type === "gap") out.pop();
  return out;
}
