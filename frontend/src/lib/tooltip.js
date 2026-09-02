// App-styled tooltips, app-wide. Native `title` bubbles can't be styled, so
// this module intercepts them: one delegated hover listener adopts the title
// (removing the attribute suppresses the browser bubble before its ~1s delay
// fires) into a data attribute and renders it in a single app-styled tip
// element instead. Every existing title= in any component gets the treatment
// with no call-site changes, and new code keeps writing plain title
// attributes.
//
// The tip is anchored to the CURSOR and appended to document.body — OUTSIDE
// the CSS-zoomed .shell — because mouse clientX/Y and an unzoomed fixed
// element share one coordinate space at every zoom level, while element rects
// under CSS zoom differ across Chromium versions.

const SHOW_DELAY_MS = 350;

export function installTooltips() {
  const style = document.createElement("style");
  style.textContent = `
    .fb-tooltip {
      position: fixed;
      z-index: 99999;
      pointer-events: none;
      max-width: 340px;
      background: #0d1930;
      border: 1px solid var(--accent, #c8a951);
      border-radius: 6px;
      color: var(--text-primary, #e6e6e6);
      padding: 6px 9px;
      font-size: 12px;
      line-height: 1.45;
      white-space: pre-line;
      box-shadow: 0 6px 16px rgba(0, 0, 0, 0.5);
    }`;
  document.head.appendChild(style);

  let tip = null;
  let timer = 0;
  let target = null;
  let anchorX = 0;
  let anchorY = 0;

  const hide = () => {
    clearTimeout(timer);
    timer = 0;
    target = null;
    if (tip) tip.remove();
  };

  const show = () => {
    if (!target || !document.contains(target)) return;
    const text = target.getAttribute("data-fbtip");
    if (!text) return;
    if (!tip) {
      tip = document.createElement("div");
      tip.className = "fb-tooltip";
    }
    tip.textContent = text;
    document.body.appendChild(tip);
    // Below-right of the cursor, clamped to the viewport; flip above the
    // cursor when there's no room underneath.
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    let x = anchorX + 12;
    let y = anchorY + 18;
    const w = tip.offsetWidth;
    const h = tip.offsetHeight;
    if (x + w > vw - 6) x = Math.max(6, vw - w - 6);
    if (y + h > vh - 6) y = Math.max(6, anchorY - h - 10);
    tip.style.left = x + "px";
    tip.style.top = y + "px";
  };

  document.addEventListener("mouseover", (e) => {
    const t = e.target?.closest?.("[title], [data-fbtip]");
    if (t === target) return;
    hide();
    if (!t) return;
    // Adopt the native title — refreshed each hover, since a framework
    // re-render can put the attribute back with newer text.
    const raw = t.getAttribute("title");
    if (raw) {
      t.setAttribute("data-fbtip", raw);
      t.removeAttribute("title");
    }
    if (!t.getAttribute("data-fbtip")) return;
    target = t;
    anchorX = e.clientX;
    anchorY = e.clientY;
    timer = setTimeout(show, SHOW_DELAY_MS);
  });
  document.addEventListener("mouseout", (e) => {
    if (!target) return;
    if (e.relatedTarget && target.contains(e.relatedTarget)) return;
    if (target.contains(e.target)) hide();
  });
  // A click usually changes what the tip described; scrolling moves it away.
  document.addEventListener("mousedown", hide, true);
  document.addEventListener("scroll", hide, true);
  window.addEventListener("blur", hide);
}
