<script>
  // Headline numbers in the Clients tab's own KPI style (value first, label
  // after). items: [{ val, label, tip }].
  export let items = [];
</script>

<div class="kpi">
  {#each items as it, i (it.label)}
    {#if i > 0}<div class="kpi-sep"></div>{/if}
    <div class="stat" title={it.tip || ""}>
      <span class="stat-val">{it.val}</span>
      <span class="stat-label">{it.label}</span>
    </div>
  {/each}
</div>

<style>
  .kpi {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 14px;
    margin-bottom: 8px;
  }
  .stat {
    display: flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;
  }
  .stat-val {
    color: var(--text-primary);
    font-size: 15px;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
  }
  .stat-label {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }
  .kpi-sep {
    width: 1px;
    height: 18px;
    background: var(--border);
  }
</style>
