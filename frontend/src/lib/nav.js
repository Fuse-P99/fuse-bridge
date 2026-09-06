import { writable } from 'svelte/store'

// The currently selected tab id. Shared so deep components (e.g. the Timers
// tab's "link your account" prompt) can navigate the user to another tab.
export const activeTab = writable('general')

// The Search tab's active sub-tab (logs | bots | parses | raids | items).
// Shared so the batphone bot-alert bar can deep-link straight to Bots.
export const searchView = writable('logs')
