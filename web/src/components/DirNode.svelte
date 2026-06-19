<script>
  import { createEventDispatcher } from 'svelte'

  export let child
  export let expanded = {}
  export let children = {}
  export let loading = {}
  export let value = '/'

  const dispatch = createEventDispatcher()

  function toggle(path) {
    dispatch('toggle', path)
  }

  function select(path) {
    dispatch('select', path)
  }
</script>

<div class="tree-node">
  <span
    class="node-row"
    class:selected={value === child.path}
    on:click={() => { select(child.path); toggle(child.path) }}
  >
    <span class="arrow-wrap">
      {#if loading[child.path]}
        <span class="spinner-sm"></span>
      {:else if children[child.path]}
        <span class="arrow" class:open={expanded[child.path]}>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="9 18 15 12 9 6"/></svg>
        </span>
      {:else}
        <span class="arrow placeholder"></span>
      {/if}
    </span>
    <svg class="folder-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#eab308" stroke-width="2">
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
    </svg>
    <span class="node-label">{child.name}</span>
  </span>
  {#if expanded[child.path] && children[child.path]}
    <div class="children">
      {#each children[child.path] as grandchild}
        <svelte:self
          child={grandchild}
          {expanded}
          {children}
          {loading}
          {value}
          on:toggle
          on:select
        />
      {/each}
    </div>
  {/if}
</div>

<style>
  .tree-node { user-select: none; }
  .node-row {
    display: flex; align-items: center; gap: 4px;
    padding: 4px 8px; border-radius: 4px;
    cursor: pointer; font-size: 0.85rem;
  }
  .node-row:hover { background: var(--bg2); }
  .node-row.selected { background: var(--primary-light, rgba(59,130,246,0.15)); color: var(--primary); }
  .arrow-wrap { width: 16px; flex-shrink: 0; display: flex; align-items: center; justify-content: center; }
  .arrow { display: flex; align-items: center; transition: transform 0.15s; }
  .arrow.open { transform: rotate(90deg); }
  .arrow.placeholder { visibility: hidden; }
  .spinner-sm { width: 10px; height: 10px; border: 2px solid var(--border); border-top-color: var(--primary); border-radius: 50%; animation: spin 0.6s linear infinite; }
  .folder-icon { flex-shrink: 0; }
  .node-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .children { padding-left: 18px; }
</style>
