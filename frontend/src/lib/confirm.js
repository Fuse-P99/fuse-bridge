import { writable } from "svelte/store";

// Promise-based replacement for window.confirm. The browser dialog is an
// unstyled OS box that reads as "wails.app says…" in a frameless Wails window,
// which looks like something went wrong rather than a deliberate prompt.
//
// One <ConfirmDialog /> is mounted at the app root and renders whatever is in
// this store, so any component can ask without owning modal markup:
//
//   if (!(await confirmDialog({ title: "Delete trigger", message: … }))) return;

export const confirmState = writable(null);

let resolveCurrent = null;

/**
 * Show a modal confirmation and resolve to true (confirmed) or false.
 *
 * @param {object} opts
 * @param {string} opts.title        Small uppercase heading.
 * @param {string} opts.message      Body text.
 * @param {string} [opts.detail]     Secondary line, muted — consequences.
 * @param {string} [opts.confirmLabel="Confirm"]
 * @param {string} [opts.cancelLabel="Cancel"]
 * @param {boolean} [opts.danger]    Style the confirm button as destructive.
 * @returns {Promise<boolean>}
 */
export function confirmDialog(opts) {
  // A second request while one is open cancels the first rather than orphaning
  // its promise — nothing in the app stacks confirmations deliberately.
  if (resolveCurrent) resolveCurrent(false);
  return new Promise((resolve) => {
    resolveCurrent = resolve;
    confirmState.set({
      title: opts.title || "Are you sure?",
      message: opts.message || "",
      detail: opts.detail || "",
      confirmLabel: opts.confirmLabel || "Confirm",
      cancelLabel: opts.cancelLabel || "Cancel",
      danger: !!opts.danger,
    });
  });
}

/** Settle the open confirmation. Used by ConfirmDialog only. */
export function settleConfirm(ok) {
  const r = resolveCurrent;
  resolveCurrent = null;
  confirmState.set(null);
  if (r) r(ok);
}
