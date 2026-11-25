<script>
  // The bell beside a server-timer board entry. Outline when nothing is set,
  // filled and gold once a reminder exists — the same read as the rest of the
  // board's accents, so "armed" is visible without hovering.
  export let active = false;
  export let title = "Set a reminder";
</script>

<button
  class="ab"
  class:on={active}
  type="button"
  title={active ? "Reminder set — click to edit or remove" : title}
  aria-label={title}
  on:click|stopPropagation
>
  <svg
    viewBox="0 0 24 24"
    width="12"
    height="12"
    fill={active ? "currentColor" : "none"}
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
  >
    <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
    <path d="M13.73 21a2 2 0 0 1-3.46 0" />
  </svg>
</button>

<style>
  .ab {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    background: transparent;
    border: none;
    border-radius: 4px;
    /* Dim until hovered: a column of bells shouldn't compete with the timers
       they belong to. An armed one opts out below. */
    color: var(--text-muted);
    cursor: pointer;
    opacity: 0.55;
  }
  .ab:hover {
    background: rgba(255, 255, 255, 0.08);
    color: var(--text-primary);
    opacity: 1;
  }
  .ab.on {
    color: #e3a008;
    opacity: 1;
  }
</style>
