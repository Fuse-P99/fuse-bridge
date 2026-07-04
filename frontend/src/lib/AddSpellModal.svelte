<script>
  import { createEventDispatcher } from 'svelte'
  import { ScrapeSpellPreview, AddSpell } from '../../wailsjs/go/main/App'

  const dispatch = createEventDispatcher()

  // Caster classes the P99 wiki has spell pages for.
  const CLASSES = ['Bard', 'Cleric', 'Druid', 'Enchanter', 'Magician',
                   'Necromancer', 'Paladin', 'Ranger', 'Shadow Knight', 'Shaman', 'Wizard']

  let url        = ''
  let desc       = ''       // admin-entered concise description (wins over scraped)
  let spell      = null     // SpellPayload once scraped/being edited
  let scraping   = false
  let saving     = false
  let error      = ''
  let done       = ''

  async function scrape() {
    error = ''; done = ''
    if (!url.trim()) { error = 'Enter a spell page URL first.'; return }
    scraping = true
    try {
      const s = await ScrapeSpellPreview(url.trim())
      // Admin description takes precedence; only fall back to scraped text if
      // the admin left the box empty.
      if (desc.trim()) s.description = desc.trim()
      else desc = s.description || ''
      if (!s.classes || !s.classes.length) s.classes = [{ class: '', level: 0 }]
      spell = s
    } catch (e) {
      error = String(e && e.message ? e.message : e)
    } finally {
      scraping = false
    }
  }

  function addClassRow()   { spell.classes = [...spell.classes, { class: '', level: 0 }] }
  function removeClassRow(i){ spell.classes = spell.classes.filter((_, k) => k !== i) }

  async function save() {
    error = ''
    if (!spell.name.trim()) { error = 'Spell name is required.'; return }
    const classes = spell.classes.filter(c => c.class && c.level > 0)
    if (!classes.length) { error = 'Add at least one class with a level, or the spell won\'t appear anywhere.'; return }
    saving = true
    try {
      await AddSpell({ ...spell, description: desc.trim(), classes })
      done = `Added "${spell.name}".`
      dispatch('added', spell.name)
    } catch (e) {
      error = String(e && e.message ? e.message : e)
    } finally {
      saving = false
    }
  }

  function close() { dispatch('close') }
</script>

<div class="overlay" on:click|self={close}>
  <div class="modal">
    <div class="modal-head">
      <span class="modal-title">Add Missing Spell</span>
      <button class="x" on:click={close}>✕</button>
    </div>

    <div class="modal-body">
      <label class="fld">
        <span class="lbl">Spell page URL</span>
        <input class="in" bind:value={url} placeholder="https://wiki.project1999.com/Spirit_of_Wolf" />
      </label>

      <label class="fld">
        <span class="lbl">Description <span class="hint">(concise — shown in the spell list)</span></span>
        <textarea class="in ta" bind:value={desc} rows="2" placeholder="e.g. Increases target's run speed."></textarea>
      </label>

      <div class="row">
        <button class="btn primary" on:click={scrape} disabled={scraping}>
          {scraping ? 'Scraping…' : spell ? 'Re-scrape' : 'Scrape'}
        </button>
      </div>

      {#if spell}
        <div class="sep"></div>
        <div class="review-note">Review the scraped fields and fill any blanks, then Add.</div>

        <div class="grid">
          <label class="fld">
            <span class="lbl">Name</span>
            <input class="in" class:blank={!spell.name} bind:value={spell.name} />
          </label>
          <label class="fld sm">
            <span class="lbl">Mana</span>
            <input class="in" type="number" bind:value={spell.mana} />
          </label>
          <label class="fld sm">
            <span class="lbl">Cast time</span>
            <input class="in" bind:value={spell.cast_time} />
          </label>
          <label class="fld sm">
            <span class="lbl">Spell type</span>
            <input class="in" bind:value={spell.spell_type} placeholder="Alteration…" />
          </label>
        </div>

        <div class="fld">
          <span class="lbl">Classes &amp; levels <span class="hint">(required)</span></span>
          {#each spell.classes as c, i}
            <div class="class-row">
              <select class="in" class:blank={!c.class} bind:value={c.class}>
                <option value="">— class —</option>
                {#each CLASSES as cl}<option value={cl}>{cl}</option>{/each}
              </select>
              <input class="in lvl" type="number" min="1" max="65" placeholder="lvl"
                     class:blank={!c.level} bind:value={c.level} />
              <button class="x sm" on:click={() => removeClassRow(i)} title="Remove">✕</button>
            </div>
          {/each}
          <button class="btn tiny" on:click={addClassRow}>+ Add class</button>
        </div>
      {/if}

      {#if error}<div class="msg err">{error}</div>{/if}
      {#if done}<div class="msg ok">{done}</div>{/if}
    </div>

    <div class="modal-foot">
      <button class="btn" on:click={close}>Close</button>
      {#if spell}
        <button class="btn primary" on:click={save} disabled={saving}>{saving ? 'Adding…' : 'Add Spell'}</button>
      {/if}
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed; inset: 0; background: rgba(0,0,0,0.55);
    display: flex; align-items: center; justify-content: center; z-index: 50;
  }
  .modal {
    width: 560px; max-width: 92vw; max-height: 88vh; display: flex; flex-direction: column;
    background: var(--bg-panel); border: 1px solid var(--border); border-radius: 8px;
    box-shadow: 0 12px 40px rgba(0,0,0,0.5);
  }
  .modal-head, .modal-foot {
    display: flex; align-items: center; padding: 12px 16px; flex-shrink: 0;
  }
  .modal-head { border-bottom: 1px solid var(--border); justify-content: space-between; }
  .modal-foot { border-top: 1px solid var(--border); justify-content: flex-end; gap: 8px; }
  .modal-title { font-size: 14px; font-weight: 700; color: var(--accent); }
  .modal-body { padding: 14px 16px; overflow-y: auto; display: flex; flex-direction: column; gap: 12px; }

  .fld { display: flex; flex-direction: column; gap: 4px; }
  .lbl { font-size: 11px; font-weight: 600; color: var(--text-secondary); text-transform: uppercase; letter-spacing: 0.04em; }
  .hint { text-transform: none; font-weight: 400; color: var(--text-muted); letter-spacing: 0; }
  .in {
    background: var(--bg-input); border: 1px solid var(--border); border-radius: 4px;
    color: var(--text-primary); font-size: 13px; padding: 6px 8px; font-family: inherit;
  }
  .in:focus { outline: none; border-color: var(--accent); }
  .in.blank { border-color: #e3a008; background: rgba(227,160,8,0.08); }
  .ta { resize: vertical; }

  .row { display: flex; gap: 8px; }
  .grid { display: grid; grid-template-columns: 2fr 1fr 1fr 1fr; gap: 8px; }
  .fld.sm { min-width: 0; }

  .sep { height: 1px; background: var(--border); }
  .review-note { font-size: 12px; color: var(--text-muted); }

  .class-row { display: flex; gap: 6px; align-items: center; margin-top: 4px; }
  .class-row .in { flex: 1; }
  .class-row .lvl { flex: 0 0 60px; }

  .btn {
    background: var(--bg-input); border: 1px solid var(--border); border-radius: 4px;
    color: var(--text-primary); cursor: pointer; font-size: 13px; padding: 6px 14px;
  }
  .btn:hover:not(:disabled) { border-color: var(--text-muted); }
  .btn:disabled { opacity: 0.6; cursor: default; }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: #0f1117; font-weight: 600; }
  .btn.tiny { padding: 3px 10px; font-size: 12px; align-self: flex-start; margin-top: 6px; }

  .x { background: none; border: none; color: var(--text-muted); cursor: pointer; font-size: 14px; }
  .x:hover { color: var(--text-primary); }
  .x.sm { font-size: 12px; padding: 0 4px; }

  .msg { font-size: 12px; padding: 8px 10px; border-radius: 4px; }
  .msg.err { color: #ef4444; background: rgba(239,68,68,0.1); }
  .msg.ok  { color: var(--success); background: rgba(34,197,94,0.1); }
</style>
