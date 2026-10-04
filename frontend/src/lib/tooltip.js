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

import { iconElement } from "./icons.js";

const SHOW_DELAY_MS = 350;

// Set by installTooltips; the programmatic API below no-ops before that.
let ctl = null;

// tipShow renders `text` (same markup as a title: **gold**, |label|value,
// {icon:name}) at the cursor position (x, y) at once — no hover delay, the
// caller already knows the pointer is on something. tipMove keeps it beside
// a moving cursor; tipHide removes it; tipVisible says whether it's up (a
// scroll or click hides it out from under the caller).
export function tipShow(text, x, y) {
  if (ctl) ctl.showText(text, x, y);
}
export function tipMove(x, y) {
  if (ctl) ctl.moveTo(x, y);
}
export function tipHide() {
  if (ctl) ctl.hide();
}
export function tipVisible() {
  return !!(ctl && ctl.visible());
}

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
    }
    .fb-tooltip .fb-tip-gold {
      color: var(--accent, #c8a951);
      font-weight: 700;
    }
    .fb-tooltip .fb-tip-grid {
      display: grid;
      grid-template-columns: auto 1fr;
      gap: 1px 10px;
      margin-top: 3px;
    }
    .fb-tooltip .fb-tip-label {
      color: var(--text-muted, #8a93a5);
      font-weight: 600;
    }`;
  document.head.appendChild(style);

  // Tip text supports three bits of structure, all built strictly from text
  // nodes, spans and DOM-built SVGs — never innerHTML — so a title carrying
  // user data (character names, log text) can't inject markup:
  //   **segment**     renders gold;
  //   {icon:name}     renders that icon from icons.js inline; {icon:name:tone}
  //                   colors it with a named tone (ok = green, bad = red …).
  //                   An unknown name stays literal text, so the worst a token
  //                   planted in user data can do is draw one of the app's own
  //                   icons;
  //   |Label|value    (line starting with "|") renders as one row of a
  //                   two-column details grid; consecutive rows share a grid.
  const ICON_TOKEN = /\{icon:([a-z-]+)(?::([a-z]+))?\}/g;
  const render = (el, text) => {
    el.textContent = "";
    // Plain text with its {icon:…} tokens swapped for SVG elements.
    const emit = (target, s) => {
      let last = 0;
      for (const m of s.matchAll(ICON_TOKEN)) {
        if (m.index > last) {
          target.appendChild(document.createTextNode(s.slice(last, m.index)));
        }
        target.appendChild(
          iconElement(m[1], m[2]) || document.createTextNode(m[0]),
        );
        last = m.index + m[0].length;
      }
      if (last < s.length) {
        target.appendChild(document.createTextNode(s.slice(last)));
      }
    };
    const inline = (target, s) => {
      String(s)
        .split("**")
        .forEach((part, i) => {
          if (!part) return;
          if (i % 2 === 1) {
            const g = document.createElement("span");
            g.className = "fb-tip-gold";
            emit(g, part);
            target.appendChild(g);
          } else {
            emit(target, part);
          }
        });
    };
    let grid = null;
    for (const line of String(text).split("\n")) {
      if (line.startsWith("|")) {
        if (!grid) {
          grid = document.createElement("div");
          grid.className = "fb-tip-grid";
          el.appendChild(grid);
        }
        const cut = line.indexOf("|", 1);
        const l = document.createElement("span");
        l.className = "fb-tip-label";
        l.textContent = cut > 0 ? line.slice(1, cut) : line.slice(1);
        const v = document.createElement("span");
        inline(v, cut > 0 ? line.slice(cut + 1) : "");
        grid.appendChild(l);
        grid.appendChild(v);
      } else {
        grid = null;
        const d = document.createElement("div");
        inline(d, line);
        el.appendChild(d);
      }
    }
  };

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

  // Below-right of the cursor, clamped to the viewport; flip above the
  // cursor when there's no room underneath.
  const place = (x0, y0) => {
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    let x = x0 + 12;
    let y = y0 + 18;
    const w = tip.offsetWidth;
    const h = tip.offsetHeight;
    if (x + w > vw - 6) x = Math.max(6, vw - w - 6);
    if (y + h > vh - 6) y = Math.max(6, y0 - h - 10);
    tip.style.left = x + "px";
    tip.style.top = y + "px";
  };

  const show = () => {
    if (!target || !document.contains(target)) return;
    const text = target.getAttribute("data-fbtip");
    if (!text) return;
    if (!tip) {
      tip = document.createElement("div");
      tip.className = "fb-tooltip";
    }
    render(tip, text);
    document.body.appendChild(tip);
    place(anchorX, anchorY);
  };

  // Programmatic use (tipShow/tipMove/tipHide below): the same tip, driven by
  // code that knows what's under the cursor when no element carries a title —
  // the dashboard's charts, whose library tooltip lived inside the chart box
  // and was clipped by the panel and the scroll container at the edges.
  ctl = {
    showText(text, x, y) {
      clearTimeout(timer);
      timer = 0;
      target = null;
      if (!text) {
        hide();
        return;
      }
      if (!tip) {
        tip = document.createElement("div");
        tip.className = "fb-tooltip";
      }
      render(tip, text);
      if (!tip.isConnected) document.body.appendChild(tip);
      place(x, y);
    },
    moveTo(x, y) {
      if (tip && tip.isConnected) place(x, y);
    },
    hide,
    visible: () => !!(tip && tip.isConnected),
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
