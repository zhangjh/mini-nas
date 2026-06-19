<script>
  import { createEventDispatcher } from 'svelte'

  export let currentPath = ''
  const dispatch = createEventDispatcher()

  $: segments = currentPath ? currentPath.split('/').filter(Boolean) : []
  $: crumbs = segments.map((seg, i) => ({
    name: seg,
    path: segments.slice(0, i + 1).join('/')
  }))

  function navigate(path) {
    dispatch('navigate', path)
  }
</script>

<nav class="breadcrumb">
  <button class="crumb" on:click={() => navigate('')}>
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>
    </svg>
  </button>
  {#each crumbs as crumb}
    <span class="sep">/</span>
    <button class="crumb" on:click={() => navigate(crumb.path)}>
      {crumb.name}
    </button>
  {/each}
</nav>

<style>
  .breadcrumb {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
    overflow: hidden;
  }

  .crumb {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 0.9rem;
    color: var(--primary);
    max-width: 160px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    cursor: pointer;
    background: none;
    border: none;
    transition: background 0.1s;
  }
  .crumb:hover { background: var(--surface2); }

  .sep {
    color: var(--text2);
    font-size: 0.8rem;
  }

  @media (max-width: 640px) {
    .crumb { font-size: 0.8rem; max-width: 100px; padding: 4px 6px; }
  }
</style>
