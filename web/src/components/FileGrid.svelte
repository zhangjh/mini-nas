<script>
  import { createEventDispatcher } from 'svelte'
  import { thumbUrl } from '../lib/api.js'

  export let files = []
  const dispatch = createEventDispatcher()
  let contextMenu = null

  function isImage(name) {
    return /\.(jpg|jpeg|png|gif|webp|bmp)$/i.test(name)
  }

  function formatSize(bytes) {
    if (bytes === 0) return '-'
    const units = ['B', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
  }

  function handleClick(item) {
    if (item.is_dir) {
      dispatch('navigate', item.path)
    } else {
      dispatch('preview', item)
    }
  }

  function handleContextMenu(e, item) {
    e.preventDefault()
    contextMenu = { x: e.clientX, y: e.clientY, item }
  }
</script>

<div class="grid">
  {#each files as item}
    <div
      class="grid-item"
      on:click={() => handleClick(item)}
      on:contextmenu={e => handleContextMenu(e, item)}
      role="button"
      tabindex="0"
    >
      <div class="grid-thumb">
        {#if item.is_dir}
          <div class="dir-icon">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="#eab308" stroke-width="1.5">
              <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
            </svg>
          </div>
        {:else if isImage(item.name)}
          <img src={thumbUrl(item.path)} alt={item.name} loading="lazy" />
        {:else}
          <div class="file-icon">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--text2)" stroke-width="1.5">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
              <polyline points="14 2 14 8 20 8"/>
            </svg>
          </div>
        {/if}
      </div>
      <div class="grid-info">
        <div class="grid-name">{item.name}</div>
        <div class="grid-meta">{formatSize(item.size)}</div>
      </div>
    </div>
  {/each}
</div>

{#if contextMenu}
  <div class="context-menu" style="left: {contextMenu.x}px; top: {contextMenu.y}px;">
    {#if !contextMenu.item.is_dir}
      <button on:click={() => { dispatch('preview', contextMenu.item); contextMenu = null }}>预览</button>
      {#if isImage(contextMenu.item.name)}
        <button on:click={() => { dispatch('similar', contextMenu.item.path); contextMenu = null }}>找相似图片</button>
      {/if}
    {/if}
    <button on:click={() => { dispatch('download', contextMenu.item.path); contextMenu = null }}>下载</button>
    <button on:click={() => { dispatch('rename', contextMenu.item.path); contextMenu = null }}>重命名</button>
    <button class="danger" on:click={() => { if (confirm('确认删除?')) { dispatch('delete', contextMenu.item.path); } contextMenu = null }}>删除</button>
  </div>
  <div class="context-overlay" on:click={() => contextMenu = null} />
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 8px;
    padding: 8px;
  }

  .grid-item {
    border-radius: 8px;
    overflow: hidden;
    cursor: pointer;
    transition: transform 0.1s, box-shadow 0.15s;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  .grid-item:hover {
    transform: translateY(-2px);
    box-shadow: var(--shadow);
  }

  .grid-thumb {
    aspect-ratio: 1;
    overflow: hidden;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--surface2);
  }
  .grid-thumb img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .dir-icon, .file-icon {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
  }

  .grid-info {
    padding: 8px 10px;
  }
  .grid-name {
    font-size: 0.8rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    margin-bottom: 2px;
  }
  .grid-meta {
    font-size: 0.75rem;
    color: var(--text2);
  }

  .context-menu {
    position: fixed;
    z-index: 300;
    background: var(--surface);
    border-radius: 8px;
    box-shadow: var(--shadow-lg);
    padding: 4px;
    min-width: 140px;
  }
  .context-menu button {
    display: block;
    width: 100%;
    text-align: left;
    padding: 8px 16px;
    border-radius: 4px;
    color: var(--text);
    background: none;
    border: none;
    cursor: pointer;
  }
  .context-menu button:hover { background: var(--surface2); }
  .context-menu .danger { color: var(--danger); }

  .context-overlay {
    position: fixed;
    inset: 0;
    z-index: 299;
  }

  @media (max-width: 640px) {
    .grid {
      grid-template-columns: repeat(auto-fill, minmax(100px, 1fr));
      gap: 6px;
      padding: 6px;
    }
  }
</style>
