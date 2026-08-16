<script>
  // Racing Mode: strafe-racing guide lines + speedometers, drawn over the
  // game's 3D viewport. The WINDOW is auto-placed to cover the viewport
  // exactly (racing.go reads eqclient.ini + the character's UI ini), so
  // everything here positions at plain percentages of the window.
  //
  // Per side: a green vertical line marking the direction of travel while
  // strafe-running, and a shorter red line inside it marking the direction
  // while strafe-JUMPING. A speedometer sits above, inside the green line and
  // directly over the red one. Speed is a trailing average built from the
  // /loc fixes the app already parses — racers keep /loc on a spammed hotkey,
  // and the widget nags when no fixes are coming in.
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { GetPlayerPosition } from "../../bindings/FuseBridge/app.js";

  export let sides = "both"; // "both" | "left" | "right"
  export let avgSec = 3; // trailing-average window, 1-10s
  // User calibration: shifts each side's line PAIR as a group, in viewport-
  // width percent (0.01 steps). Positive = outward (left pair further left,
  // right pair further right); negative = inward.
  export let offsetPct = 0;

  // Line anchors as fractions of viewport width — starting values, expected
  // to be tuned against live strafe testing. The red (strafe-jump) inset is
  // a first guess pending that same testing.
  const GREEN_X = { left: 23.3, right: 76.7 }; // percent
  const RED_X = { left: 26.3, right: 73.7 }; // percent

  $: off = Math.max(-20, Math.min(20, +offsetPct || 0));
  $: lineX = {
    left: { green: GREEN_X.left - off, red: RED_X.left - off },
    right: { green: GREEN_X.right + off, red: RED_X.right + off },
  };

  let fixes = []; // [{t, x, y}] recent /loc readings (unix ms)
  let now = Date.now();
  let tick;
  let offLoc = null;

  function addFix(p) {
    if (!p || !p.time || !isFinite(p.x)) return;
    const last = fixes[fixes.length - 1];
    if (last && last.t === p.time) return;
    fixes = [...fixes.filter((f) => p.time - f.t < 15000), { t: p.time, x: p.x, y: p.y }];
  }

  onMount(async () => {
    offLoc = Events.On("player-loc", (ev) => addFix(ev && ev.data));
    try {
      addFix(await GetPlayerPosition());
    } catch {
      /* no fix yet */
    }
    // The ticker re-evaluates the trailing window and staleness; a 1s poll
    // rides on it as the catch-up path in case an event is dropped.
    let n = 0;
    tick = setInterval(async () => {
      now = Date.now();
      if (++n % 4 === 0) {
        try {
          addFix(await GetPlayerPosition());
        } catch {
          /* stale view stands */
        }
      }
    }, 250);
  });
  onDestroy(() => {
    clearInterval(tick);
    if (offLoc) offLoc();
  });

  // Trailing-average speed: total path distance across the fixes inside the
  // window, over the time they actually span. null = not enough data.
  function trailingSpeed(list, t, windowSec) {
    const cutoff = t - Math.max(1, Math.min(10, windowSec)) * 1000;
    const w = list.filter((f) => f.t >= cutoff);
    if (w.length < 2) return null;
    let dist = 0;
    for (let i = 1; i < w.length; i++) {
      dist += Math.hypot(w[i].x - w[i - 1].x, w[i].y - w[i - 1].y);
    }
    const span = (w[w.length - 1].t - w[0].t) / 1000;
    if (span < 0.3) return null;
    return dist / span;
  }
  $: speed = trailingSpeed(fixes, now, avgSec);
  // No fix in a while → the speedo can't work; nudge the user to spam /loc.
  $: lastFix = fixes.length ? fixes[fixes.length - 1].t : 0;
  $: stale = !lastFix || now - lastFix > 2500;

  $: showLeft = sides !== "right";
  $: showRight = sides !== "left";
</script>

<div class="race">
  {#each [showLeft ? "left" : "", showRight ? "right" : ""].filter(Boolean) as side}
    <div class="line green" style="left:{lineX[side].green}%"></div>
    <div class="line red" style="left:{lineX[side].red}%"></div>
    <div class="speedo" class:stale style="left:{lineX[side].red}%">
      <div class="sp-num">{speed == null || stale ? "—" : Math.round(speed)}</div>
      <div class="sp-lbl">loc/s · {avgSec}s avg</div>
      {#if stale}
        <div class="sp-warn">no /loc — spam /loc!</div>
      {/if}
    </div>
  {/each}
</div>

<style>
  /* Fixed to the window viewport so the percentages are true viewport
     percentages regardless of the shell's title bar; never eats the mouse. */
  .race {
    position: fixed;
    inset: 0;
    pointer-events: none;
    z-index: 5;
  }
  .line {
    position: absolute;
    width: 2px;
    transform: translateX(-50%);
  }
  /* Green: strafe-run direction — 20% of viewport height each side of the
     vertical middle, then extended UPWARD by 20% of its own length (bottom
     stays anchored at 70%). */
  .line.green {
    top: 22%;
    height: 48%;
    background: #28d75a;
    box-shadow: 0 0 4px rgba(40, 215, 90, 0.7);
  }
  /* Red: strafe-jump direction — 20% shorter than green, same upward
     extension (bottom anchored at 66%). */
  .line.red {
    top: 27.6%;
    height: 38.4%;
    background: #ff4040;
    box-shadow: 0 0 4px rgba(255, 64, 64, 0.7);
  }
  .speedo {
    position: absolute;
    bottom: calc(78% + 10px); /* rests just above the lines' top edge */
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 1px;
    background: rgba(10, 12, 20, 0.72);
    border: 1px solid rgba(40, 215, 90, 0.5);
    border-radius: 6px;
    padding: 4px 10px;
    min-width: 64px;
  }
  .speedo.stale {
    border-color: rgba(255, 64, 64, 0.6);
  }
  .sp-num {
    font-family: var(--font-mono, monospace);
    font-size: 22px;
    font-weight: 700;
    line-height: 1.1;
    color: #e8ffe8;
    font-variant-numeric: tabular-nums;
  }
  .sp-lbl {
    font-size: 8.5px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: rgba(232, 255, 232, 0.55);
    white-space: nowrap;
  }
  .sp-warn {
    font-size: 10px;
    font-weight: 700;
    color: #ff7a7a;
    white-space: nowrap;
    animation: race-pulse 1.2s ease-in-out infinite;
  }
  @keyframes race-pulse {
    50% {
      opacity: 0.35;
    }
  }
</style>
