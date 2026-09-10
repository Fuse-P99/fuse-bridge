import { writable } from 'svelte/store'

// The currently selected tab id. Shared so deep components (e.g. the Timers
// tab's "link your account" prompt) can navigate the user to another tab.
export const activeTab = writable('general')

// The Search tab's active sub-tab (logs | bots | parses | raids | items).
// Shared so the batphone bot-alert bar can deep-link straight to Bots.
export const searchView = writable('logs')

// The Characters tab's last selection, so leaving the tab and coming back
// lands on the same character and sub-tab. The tab component is destroyed on
// every switch, so this lives here rather than in it. `char` empty means
// "nothing chosen yet" — the tab then opens the top character's Magelo.
export const charactersView = writable({ char: '', tab: 'magelo', viewMode: 'detail' })

// A request to open one character's quest (Characters → Quests, card
// expanded), from the quest nudge bar or a quest flag on the map. `seq` bumps
// on every request so the same quest can be re-opened; the tab acts once per
// seq. Set by App.svelte from the Go "open-character-quest" event, which is
// how the overlay map (a separate webview) reaches the main window.
export const questDeepLink = writable({ char: '', questId: 0, seq: 0 })
