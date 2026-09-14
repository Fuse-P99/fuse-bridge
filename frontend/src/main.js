import './style.css'
import App from './App.svelte'
import Popout from './Popout.svelte'
import HoverCardWindow from './HoverCardWindow.svelte'

// Overlay popout windows load this same bundle with a hash route (set by the
// Go side when creating the window), e.g. #popout=timers&category=Discs.
// Render just that overlay instead of the full app.
const params = new URLSearchParams(window.location.hash.slice(1))
const popout = params.get('popout')

// Overlays composite transparently — clear the opaque page background before
// first paint so the game shows through (Popout.svelte keeps it transparent).
if (popout) {
  document.documentElement.style.background = 'transparent'
  document.body.style.background = 'transparent'
}

// The hover card is a popout window too, but not an OVERLAY: it has no title
// bar, no settings, no saved geometry and no character of its own — Go places
// it beside the cursor and tells it what to draw (hovercard.go). So it gets
// its own tiny page rather than a branch inside the overlay shell.
const app =
  popout === 'hovercard'
    ? new HoverCardWindow({
        target: document.getElementById('app'),
      })
    : popout
      ? new Popout({
          target: document.getElementById('app'),
          props: {
            kind: popout,
            category: params.get('category') || '',
            // Set by Go for user-initiated pop-outs: flash the title bar on
            // mount so the fresh overlay is easy to locate.
            flash: params.get('flash') === '1',
          },
        })
      : new App({
          target: document.getElementById('app'),
        })

export default app
