<script>
  import { createEventDispatcher, onMount } from 'svelte'
  import { listMounts, createMount, updateMount, deleteMount } from '../lib/api.js'
  import { notify } from '../lib/stores.js'
  import { showSettings } from '../lib/stores.js'
  import DirTree from './DirTree.svelte'

  const dispatch = createEventDispatcher()

  let mounts = []
  let loading = true
  let showForm = false
  let formName = ''
  let formPath = ''
  let editingIdx = -1
  let showDirTree = false

  onMount(load)

  async function load() {
    loading = true
    try {
      mounts = await listMounts()
    } catch (e) {
      notify('加载存储挂载失败', 'error')
    }
    loading = false
  }

  function openAdd() {
    formName = ''
    formPath = ''
    editingIdx = -1
    showForm = true
  }

  function startEdit(idx) {
    const m = mounts[idx]
    formName = m.name
    formPath = m.path
    editingIdx = idx
    showForm = true
  }

  async function save() {
    if (!formName || !formPath) return
    try {
      if (editingIdx >= 0) {
        await updateMount(formName, formPath)
        notify('已更新', 'success')
      } else {
        await createMount(formName, formPath)
        notify('已添加', 'success')
      }
      showForm = false
      dispatch('changed')
      load()
    } catch (e) {
      notify(e.message, 'error')
    }
  }

  async function remove(name) {
    if (!confirm(`确认移除挂载点 "${name}"？`)) return
    try {
      await deleteMount(name)
      dispatch('changed')
      notify('已移除', 'success')
      load()
    } catch (e) {
      notify(e.message, 'error')
    }
  }
</script>

<div class="overlay" on:click|self={() => showSettings.set(false)}>
  <div class="dialog settings-dialog">
    <div class="dialog-header">
      <h3>存储管理</h3>
      <button class="btn btn-ghost btn-sm" on:click={() => showSettings.set(false)}>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </button>
    </div>

    <div class="settings-body">
      {#if loading}
        <div class="loading"><div class="spinner"></div></div>
      {:else if showForm}
        <div class="form">
          <div class="field">
            <label>挂载名称</label>
            <input type="text" bind:value={formName} placeholder="例如: 照片" disabled={editingIdx >= 0} />
          </div>
          <div class="field">
            <label>服务器路径</label>
            <div class="path-input-row">
              <input type="text" bind:value={formPath} placeholder="例如: /mnt/photos" readonly />
              <button class="btn btn-ghost btn-sm" on:click={() => showDirTree = !showDirTree}>
                {showDirTree ? '收起' : '浏览'}
              </button>
            </div>
            {#if showDirTree}
              <DirTree value={formPath} on:select={(e) => { formPath = e.detail.path }} />
            {/if}
          </div>
          <div class="form-actions">
            <button class="btn btn-ghost" on:click={() => showForm = false}>取消</button>
            <button class="btn btn-primary" on:click={save} disabled={!formName || !formPath}>
              {editingIdx >= 0 ? '更新' : '添加'}
            </button>
          </div>
        </div>
      {:else}
        <div class="mount-list">
          {#each mounts as mount, i}
            <div class="mount-item">
              <div class="mount-info">
                <div class="mount-name">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#eab308" stroke-width="2">
                    <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
                  </svg>
                  {mount.name}
                </div>
                <div class="mount-path">{mount.path}</div>
              </div>
              <div class="mount-actions">
                <button class="btn btn-ghost btn-sm" on:click={() => startEdit(i)} title="编辑">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
                </button>
                <button class="btn btn-ghost btn-sm danger" on:click={() => remove(mount.name)} title="移除">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
                </button>
              </div>
            </div>
          {:else}
            <div class="empty">暂无自定义挂载点</div>
          {/each}
        </div>
        <div class="add-btn-wrap">
          <button class="btn btn-primary btn-sm" on:click={openAdd}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            添加挂载点
          </button>
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.4);
    z-index: 100;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
  }

  .settings-dialog {
    max-width: 520px;
  }

  .dialog {
    background: var(--surface);
    border-radius: 12px;
    width: 100%;
    display: flex;
    flex-direction: column;
    box-shadow: var(--shadow-lg);
    max-height: 80vh;
  }

  .dialog-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .dialog-header h3 { font-size: 1.1rem; }

  .settings-body {
    padding: 8px 0;
    overflow-y: auto;
    flex: 1;
  }

  .mount-list {
    padding: 0 4px;
  }

  .mount-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .mount-item:last-child { border-bottom: none; }

  .mount-info {
    flex: 1;
    min-width: 0;
  }

  .mount-name {
    display: flex;
    align-items: center;
    gap: 6px;
    font-weight: 500;
    font-size: 0.9rem;
    margin-bottom: 2px;
  }

  .mount-path {
    font-size: 0.8rem;
    color: var(--text2);
    font-family: 'SF Mono', 'Fira Code', monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mount-actions {
    display: flex;
    gap: 2px;
    flex-shrink: 0;
  }
  .mount-actions .danger { color: var(--danger); }

  .add-btn-wrap {
    padding: 12px 16px;
    border-top: 1px solid var(--border);
  }

  .form {
    padding: 16px;
  }

  .field {
    margin-bottom: 14px;
  }
  .field label {
    display: block;
    font-size: 0.85rem;
    font-weight: 500;
    margin-bottom: 6px;
    color: var(--text2);
  }

  .path-input-row {
    display: flex;
    gap: 6px;
  }
  .path-input-row input { flex: 1; cursor: pointer; background: var(--bg2); }
  .path-input-row input:read-only { color: var(--text); }

  .form-actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    margin-top: 20px;
  }

  .loading { display: flex; justify-content: center; padding: 40px; }
  .empty { text-align: center; padding: 32px; color: var(--text2); font-size: 0.9rem; }
</style>
