import { writable } from 'svelte/store'

// Whether this client has a linked (token-authenticated) Fuse account.
// Shared so the tab bar (App.svelte) and the linking UI (AccountLink.svelte)
// stay in sync the moment linking/unlinking happens.
export const linked = writable(false)
