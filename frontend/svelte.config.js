import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

// Minimal Svelte config. Its main purpose is to give the VS Code Svelte
// extension's language server a config to read directly — without it, the
// extension falls back to introspecting vite.config.js and errors with
// "No Svelte configuration found in vite config." The build reads this too
// (vite-plugin-svelte auto-loads it); vitePreprocess is a no-op for plain
// JS/CSS components, so it doesn't change existing build output.
export default {
  preprocess: vitePreprocess(),
}
