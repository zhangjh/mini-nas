<script>
  import { createEventDispatcher } from 'svelte'
  import { thumbUrl, previewUrl } from '../lib/api.js'

  export let files = []
  export let selected = new Set()
  const dispatch = createEventDispatcher()

  let contextMenu = null
  let longPressTimer

  function formatSize(bytes) {
    if (bytes === 0) return '-'
    const units = ['B', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
  }

  function formatTime(ts) {
    const d = new Date(ts)
    const pad = n => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  }

  function isImage(name) {
    return /\.(jpg|jpeg|png|gif|webp|bmp)$/i.test(name)
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
    contextMenu = { x: e.clientX || e.touches?.[0]?.clientX || 0, y: e.clientY || e.touches?.[0]?.clientY || 0, item }
  }

  function handleTouchStart(item) {
    longPressTimer = setTimeout(() => {
      dispatch('select', item)
    }, 500)
  }
  function handleTouchEnd() {
    clearTimeout(longPressTimer)
  }
</script>

<div class="file-list">
  {#each files as item}
    <div
      class="file-row"
      class:selected={selected.has(item.path)}
      on:click={() => handleClick(item)}
      on:contextmenu={e => handleContextMenu(e, item)}
      on:touchstart={() => handleTouchStart(item)}
      on:touchend={handleTouchEnd}
      on:touchmove={handleTouchEnd}
      role="button"
      tabindex="0"
    >
      <div class="cell cell-select" on:click|stopPropagation={() => dispatch('select', item)}>
        <input type="checkbox" checked={selected.has(item.path)} readonly />
      </div>
      <div class="cell cell-icon">
        {#if item.is_dir}
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#eab308" stroke-width="2">
            <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
          </svg>
        {:else if isImage(item.name)}
          <img class="thumb" src={thumbUrl(item.path)} alt={item.name} loading="lazy" />
        {:else}
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--text2)" stroke-width="2">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
            <polyline points="14 2 14 8 20 8"/>
          </svg>
        {/if}
      </div>
      <div class="cell cell-name">{item.name}</div>
      <div class="cell cell-size">{item.is_dir ? '-' : formatSize(item.size)}</div>
      <div class="cell cell-time">{formatTime(item.mod_time)}</div>
      <div class="cell cell-actions">
        {#if !item.is_dir}
          <button class="btn-icon-sm" on:click|stopPropagation={() => dispatch('download', item.path)} title="下载">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
          </button>
        {/if}
        <button class="btn-icon-sm" on:click|stopPropagation={() => dispatch('rename', item.path)} title="重命名">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
        </button>
        <button class="btn-icon-sm danger" on:click|stopPropagation={() => dispatch('delete', item.path)} title="删除">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
        </button>
      </div>
    </div>
  {/each}
</div>

{#if contextMenu}
  <div class="context-menu" style="left: {contextMenu.x}px; top: {contextMenu.y}px;" on:click={() => contextMenu = null}>
    {#if !contextMenu.item.is_dir}
      <button on:click={() => { dispatch('preview', contextMenu.item); contextMenu = null }}>预览</button>
    {/if}
    <button on:click={() => { dispatch('download', contextMenu.item.path); contextMenu = null }}>下载</button>
    <button on:click={() => { dispatch('rename', contextMenu.item.path); contextMenu = null }}>重命名</button>
    <button class="danger" on:click={() => { if (confirm('确认删除?')) { dispatch('delete', contextMenu.item.path); } contextMenu = null }}>删除</button>
  </div>
  <div class="context-overlay" on:click={() => contextMenu = null} />
{/if}

<style>
  .file-list {
    user-select: none;
  }

  .file-row {
    display: flex;
    align-items: center;
    padding: 10px 16px;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    transition: background 0.1s;
    gap: 8px;
  }
  .file-row:hover { background: var(--surface2); }
  .file-row.selected { background: rgba(59,130,246,0.08); }

  .cell { display: flex; align-items: center; }

  .cell-select { width: 32px; flex-shrink: 0; }
  .cell-select input { cursor: pointer; width: 16px; height: 16px; }

  .cell-icon { width: 28px; flex-shrink: 0; justify-content: center; }
  .thumb {
    width: 24px;
    height: 24px;
    border-radius: 4px;
    object-fit: cover;
  }

  .cell-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.9rem;
  }

  .cell-size {
    width: 80px;
    flex-shrink: 0;
    color: var(--text2);
    font-size: 0.85rem;
    justify-content: flex-end;
  }

  .cell-time {
    width: 140px;
    flex-shrink: 0;
    color: var(--text2);
    font-size: 0.85rem;
  }

  .cell-actions {
    display: flex;
    gap: 2px;
    opacity: 0;
    transition: opacity 0.15s;
  }
  .file-row:hover .cell-actions { opacity: 1; }

  .btn-icon-sm {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    border-radius: 6px;
    color: var(--text2);
    background: none;
    border: none;
    cursor: pointer;
    transition: all 0.1s;
  }
  .btn-icon-sm:hover { background: rgba(0,0,0,0.08); color: var(--text); }
  .btn-icon-sm.danger:hover { background: rgba(239,68,68,0.1); color: var(--danger); }

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

  @media (max-width: 768px) {
    .cell-time { display: none; }
    .cell-size { width: 60px; font-size: 0.8rem; }
    .file-row { padding: 12px 12px; }
    .cell-actions { opacity: 1; }
  }

  @media (max-width: 480px) {
    .cell-select { width: 28px; }
    .cell-name { font-size: 0.85rem; }
  }
</style>
