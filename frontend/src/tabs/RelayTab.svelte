<script>
  import { onMount } from 'svelte'
  import { GetSettings, SaveSettings } from '../../wailsjs/go/main/App'

  let settings = {}
  let loaded   = false

  const options = [
    { key: 'guild_chat',          label: 'Guild chat'                     },
    { key: 'guild_motd',          label: 'Guild MOTD'                     },
    { key: 'broadcasts',          label: 'GM Broadcasts'                  },
    { key: 'server_messages',     label: 'Server Messages'                },
    { key: 'quake_messages',      label: 'Quake messages'                 },
    { key: 'engage_messages',     label: 'Engage messages'                },
    { key: 'who_output',          label: '/who output'                    },
    { key: 'character_locations', label: 'Character locations'            },
    { key: 'slain_messages',      label: 'Slain messages (raid mobs)'     },
  ]

  onMount(async () => {
    settings = await GetSettings()
    loaded   = true
  })

  async function onChange(key, val) {
    settings = { ...settings, [key]: val }
    await SaveSettings(settings)
  }
</script>

<div class="relay">
  <div class="section-title">Forwarded Message Types</div>
  {#if loaded}
    <div class="list">
      {#each options as opt}
        <label class="row">
          <input
            type="checkbox"
            checked={settings[opt.key]}
            on:change={e => onChange(opt.key, e.target.checked)}
          />
          <span class="row-label" class:checked={settings[opt.key]}>{opt.label}</span>
        </label>
      {/each}
    </div>
  {/if}
</div>

<style>
  .relay {
    padding: 20px;
  }

  .section-title {
    color: var(--accent);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    margin-bottom: 10px;
  }

  .list {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px 16px;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 5px 4px;
    border-radius: 4px;
    cursor: pointer;
    transition: background 0.1s;
  }
  .row:hover { background: rgba(255,255,255,0.03); }

  .row input[type="checkbox"] {
    accent-color: var(--accent);
    width: 14px;
    height: 14px;
    flex-shrink: 0;
  }

  .row-label {
    font-size: 13px;
    color: var(--text-secondary);
    transition: color 0.15s;
  }
  .row-label.checked { color: var(--text-primary); }
</style>
