<script>
  import { createEventDispatcher } from 'svelte'
  import { listSystemDirs } from '../lib/api.js'
  import DirNode from './DirNode.svelte'

  export let value = '/'

  const dispatch = createEventDispatcher()

  let expanded = {}
  let children = {}
  let loading = {}
  let errorMsg = ''

  async function toggle(path) {
    if (expanded[path]) {
      expanded = {...expanded, [path]: false}
      return
    }
    if (children[path]) {
      expanded = {...expanded, [path]: true}
      return
    }
    loading = {...loading, [path]: true}
    try {
      const dirs = await listSystemDirs(path)
      children = {...children, [path]: dirs}
      expanded = {...expanded, [path]: true}
    } catch (e) {
      errorMsg = `无法读取 "${path}"：${e.message}（可能是权限不足）`
    }
    loading = {...loading, [path]: false}
  }

  function select(path) {
    value = path
    dispatch('select', { path })
  }
</script>

<div class="dir-tree">
  <div class="tree-hint">浏览服务器宿主机文件系统，选择要挂载的目录</div>
  {#if errorMsg}
    <div class="tree-error">{errorMsg}</div>
  {/if}
  <div class="tree-scroll">
    <div class="tree-node">
      <span class="node-row" class:selected={value === '/'} on:click={() => { select('/'); toggle('/') }}>
        <span class="arrow-wrap">
          {#if loading['/']}
            <span class="spinner-sm"></span>
          {:else if children['/']}
            <span class="arrow" class:open={expanded['/']}>
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="9 18 15 12 9 6"/></svg>
            </span>
          {:else}
            <span class="arrow placeholder"></span>
          {/if}
        </span>
        <svg class="folder-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#eab308" stroke-width="2">
          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
        </svg>
        <span class="node-label">/ (根目录)</span>
      </span>
      {#if expanded['/'] && children['/']}
        <div class="children">
          {#each children['/'] as child}
            <DirNode {child} {expanded} {children} {loading} {value}
              on:toggle={(e) => toggle(e.detail)}
              on:select={(e) => select(e.detail)} />
          {/each}
        </div>
      {/if}
    </div>
  </div>
  <div class="selected-path">
    <span class="path-label">已选路径:</span>
    <span class="path-value">{value}</span>
  </div>
</div>

<style>
  .dir-tree {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
    max-height: 320px;
  }
  .tree-hint {
    padding: 6px 10px;
    font-size: 0.75rem;
    color: var(--text2);
    background: var(--bg2);
    border-bottom: 1px solid var(--border);
  }
  .tree-error {
    padding: 6px 10px;
    font-size: 0.75rem;
    color: var(--danger, #ef4444);
    background: rgba(239, 68, 68, 0.1);
    border-bottom: 1px solid var(--border);
  }
  .tree-scroll { overflow-y: auto; flex: 1; padding: 4px 0; }
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
  .selected-path {
    display: flex; align-items: center; gap: 6px;
    padding: 8px 12px; border-top: 1px solid var(--border);
    background: var(--bg2); font-size: 0.8rem;
    font-family: 'SF Mono', 'Fira Code', monospace;
  }
  .path-label { color: var(--text2); flex-shrink: 0; }
  .path-value { color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
