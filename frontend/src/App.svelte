<script>
  import { onMount } from 'svelte'
  import { IsAdminMode, IsLinked } from '../wailsjs/go/main/App'
  import { linked } from './lib/linkState.js'
  import GeneralTab    from './tabs/GeneralTab.svelte'
  import RelayTab      from './tabs/RelayTab.svelte'
  import CharactersTab from './tabs/CharactersTab.svelte'
  import ZonesTab      from './tabs/ZonesTab.svelte'
  import MapTab        from './tabs/MapTab.svelte'
  import TimersTab     from './tabs/TimersTab.svelte'
  import ClientsTab    from './tabs/ClientsTab.svelte'
  import { scale }     from './lib/scale.js'

  let activeTab = 'general'
  let isAdmin   = false

  const baseTabs = [
    { id: 'general',    label: 'General'    },
    { id: 'relay',      label: 'Relay'      },
    { id: 'characters', label: 'Characters' },
    { id: 'zones',      label: 'Zones'      },
    { id: 'map',        label: 'Map'        },
  ]

  onMount(async () => {
    isAdmin = await IsAdminMode()
    linked.set(await IsLinked())
  })

  // Timers requires a linked account; Clients is admin-only.
  $: tabs = [
    ...baseTabs,
    ...($linked ? [{ id: 'timers', label: 'Timers' }] : []),
    ...(isAdmin ? [{ id: 'clients', label: 'Clients' }] : []),
  ]

  // If the current tab disappears (e.g. after unlinking), fall back to General.
  $: if (!tabs.find(t => t.id === activeTab)) activeTab = 'general'
</script>

<div class="shell" style="zoom:{$scale}; height:calc(100vh / {$scale})">
  <nav class="tab-bar">
    {#each tabs as t}
      <button
        class="tab-btn"
        class:active={activeTab === t.id}
        on:click={() => activeTab = t.id}
      >{t.label}</button>
    {/each}

    <div class="size-btns">
      <button class="size-btn size-s" class:active={$scale === 1.0} on:click={() => $scale = 1.0} title="Small">A</button>
      <button class="size-btn size-m" class:active={$scale === 1.2} on:click={() => $scale = 1.2} title="Medium">A</button>
      <button class="size-btn size-l" class:active={$scale === 1.4} on:click={() => $scale = 1.4} title="Large">A</button>
    </div>
  </nav>

  <main class="tab-content">
    {#if activeTab === 'general'}
      <GeneralTab />
    {:else if activeTab === 'relay'}
      <RelayTab />
    {:else if activeTab === 'characters'}
      <CharactersTab />
    {:else if activeTab === 'zones'}
      <ZonesTab />
    {:else if activeTab === 'map'}
      <MapTab />
    {:else if activeTab === 'timers'}
      <TimersTab />
    {:else if activeTab === 'clients'}
      <ClientsTab />
    {/if}
  </main>
</div>

<style>
  .shell {
    display: flex;
    flex-direction: column;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    font-size: 13px;
  }

  .tab-bar {
    display: flex;
    align-items: center;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
    -webkit-app-region: drag;
  }

  .tab-btn {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 13px;
    padding: 10px 20px;
    transition: color 0.15s, border-color 0.15s;
    -webkit-app-region: no-drag;
  }

  .tab-btn:hover       { color: var(--text-primary); }
  .tab-btn.active      { color: var(--accent); border-bottom-color: var(--accent); }

  .size-btns {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 10px;
    -webkit-app-region: no-drag;
  }

  .size-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-family: inherit;
    font-weight: 600;
    line-height: 1;
    padding: 2px 4px;
    border-radius: 3px;
    transition: color 0.15s;
  }
  .size-btn:hover  { color: var(--text-primary); }
  .size-btn.active { color: var(--accent); }

  .size-s { font-size: 10px; }
  .size-m { font-size: 13px; }
  .size-l { font-size: 17px; }

  .tab-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
</style>
