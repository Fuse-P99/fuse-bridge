import { writable } from 'svelte/store'

// The currently selected tab id. Shared so deep components (e.g. the Timers
// tab's "link your account" prompt) can navigate the user to another tab.
export const activeTab = writable('general')
