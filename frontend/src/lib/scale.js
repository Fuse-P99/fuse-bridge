import { writable } from 'svelte/store'

// Global UI zoom factor, applied as CSS `zoom` on the .shell in App.svelte.
// Anything positioned from raw mouse coordinates (e.g. right-click menus) must
// divide clientX/clientY by this, since `zoom` scales the inner coordinate space.
export const scale = writable(1.2)
