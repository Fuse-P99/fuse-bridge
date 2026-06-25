<script>
  import { onMount } from 'svelte'
  import { IsAdminMode } from '../wailsjs/go/main/App'
  import GeneralTab    from './tabs/GeneralTab.svelte'
  import RelayTab      from './tabs/RelayTab.svelte'
  import CharactersTab from './tabs/CharactersTab.svelte'
  import ZonesTab      from './tabs/ZonesTab.svelte'
  import ClientsTab    from './tabs/ClientsTab.svelte'

  let activeTab = 'general'
  let isAdmin   = false

  const baseTabs = [
    { id: 'general',    label: 'General'    },
    { id: 'relay',      label: 'Relay'      },
    { id: 'characters', label: 'Characters' },
    { id: 'zones',      label: 'Zones'      },
  ]

  onMount(async () => {
    isAdmin = await IsAdminMode()
  })

  $: tabs = isAdmin ? [...baseTabs, { id: 'clients', label: 'Clients' }] : baseTabs
</script>

<div class="shell">
  <nav class="tab-bar">
    {#each tabs as t}
      <button
        class="tab-btn"
        class:active={activeTab === t.id}
        on:click={() => activeTab = t.id}
      >{t.label}</button>
    {/each}
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
    {:else if activeTab === 'clients'}
      <ClientsTab />
    {/if}
  </main>
</div>

<style>
  .shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    font-size: 40px;
  }

  .tab-bar {
    display: flex;
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

  .tab-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
</style>
