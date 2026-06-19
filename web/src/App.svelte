<script>
  import { onMount } from 'svelte'
  import { isAuthenticated, notification, currentPath, files, totalFiles, isLoading,
    sortBy, sortDesc, viewMode, selectedFiles, showUpload, showNewFolder,
    previewFile, searchQuery, searchResults, showSettings, notify } from './lib/stores.js'
  import { setToken, getToken, listFiles, login as apiLogin,
    mkdir, deleteFile, renameFile, uploadFile, downloadFile,
    searchFiles, healthCheck } from './lib/api.js'
  import LoginForm from './components/LoginForm.svelte'
  import Breadcrumb from './components/Breadcrumb.svelte'
  import FileList from './components/FileList.svelte'
  import FileGrid from './components/FileGrid.svelte'
  import FilePreview from './components/FilePreview.svelte'
  import UploadDialog from './components/UploadDialog.svelte'
  import StorageSettings from './components/StorageSettings.svelte'

  let authed = false
  let loading = true
  let searchActive = false
  let searchTimer

  isAuthenticated.subscribe(v => authed = v)

  onMount(async () => {
    if (getToken()) {
      try {
        await healthCheck()
        isAuthenticated.set(true)
      } catch {
        setToken(null)
        isAuthenticated.set(false)
      }
    }
    loading = false
  })

  async function loadDir(dir) {
    if (dir === undefined) dir = $currentPath
    $isLoading = true
    $selectedFiles = new Set()
    try {
      const data = await listFiles(dir, {
        sortBy: $sortBy,
        sortDesc: $sortDesc
      })
      files.set(data.items || [])
      totalFiles.set(data.total || 0)
      currentPath.set(data.path || dir)
    } catch (e) {
      notify(e.message, 'error')
    }
    $isLoading = false
  }

  async function handleLogin(password) {
    try {
      const data = await apiLogin(password)
      setToken(data.token)
      isAuthenticated.set(true)
      notify('登录成功', 'success')
      loadDir('')
    } catch (e) {
      throw e
    }
  }

  function handleLogout() {
    setToken(null)
    isAuthenticated.set(false)
    files.set([])
    currentPath.set('')
  }

  function handleNavigate(path) {
    currentPath.set(path)
    loadDir(path)
  }

  function handleSort(by) {
    if ($sortBy === by) {
      sortDesc.update(v => !v)
    } else {
      sortBy.set(by)
      sortDesc.set(false)
    }
    loadDir()
  }

  async function handleNewFolder(name) {
    try {
      const path = $currentPath ? `${$currentPath}/${name}` : name
      await mkdir(path)
      notify('目录已创建', 'success')
      loadDir()
    } catch (e) {
      notify(e.message, 'error')
    }
  }

  async function handleDelete(path) {
    try {
      await deleteFile(path)
      notify('已删除', 'success')
      loadDir()
    } catch (e) {
      notify(e.message, 'error')
    }
  }

  async function handleRename(path, newName) {
    try {
      await renameFile(path, newName)
      notify('已重命名', 'success')
      loadDir()
    } catch (e) {
      notify(e.message, 'error')
    }
  }

  async function handleUpload(path, file) {
    try {
      await uploadFile(path, file)
      notify('上传成功', 'success')
      loadDir()
    } catch (e) {
      notify(e.message, 'error')
    }
  }

  function handlePreview(file) {
    $previewFile = file
  }

  function handleSearch(q) {
    if (!q.trim()) {
      searchActive = false
      searchResults.set([])
      return
    }
    searchActive = true
    clearTimeout(searchTimer)
    searchTimer = setTimeout(async () => {
      try {
        const results = await searchFiles(q, $currentPath)
        searchResults.set(results || [])
      } catch {
        searchResults.set([])
      }
    }, 300)
  }

  function handleSelect(item) {
    $selectedFiles = (() => {
      const s = new Set($selectedFiles)
      if (s.has(item.path)) s.delete(item.path)
      else s.add(item.path)
      return s
    })()
  }

  function handleMountsChanged() {
    loadDir()
  }

  function handleSelectAll() {
    $selectedFiles = (() => {
      const s = new Set($selectedFiles)
      const all = $files.map(f => f.path)
      const allSelected = all.every(p => s.has(p))
      if (allSelected) return new Set()
      return new Set(all)
    })()
  }
</script>

{#if loading}
  <div class="loading-screen">
    <div class="spinner"></div>
  </div>
{:else if !authed}
  <LoginForm onLogin={handleLogin} />
{:else}
  <div class="app">
    <header class="header">
      <div class="header-left">
        <span class="logo">mini-nas</span>
      </div>
      <div class="header-center">
        <div class="search-bar">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/></svg>
          <input
            type="text"
            placeholder="搜索文件..."
            bind:value={$searchQuery}
            on:input={() => handleSearch($searchQuery)}
            on:blur={() => setTimeout(() => { if (!$searchQuery) searchActive = false }, 200)}
          />
        </div>
      </div>
      <div class="header-right">
        <button class="btn btn-ghost btn-sm" on:click={() => $showUpload = true}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
          上传
        </button>
        <button class="btn btn-ghost btn-sm" on:click={() => $showSettings = true}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
          存储
        </button>
        <button class="btn btn-ghost btn-sm" on:click={handleLogout}>退出</button>
      </div>
    </header>

    <div class="toolbar">
      <Breadcrumb currentPath={$currentPath} on:navigate={e => handleNavigate(e.detail)} />
      <div class="toolbar-actions">
        <button class="btn btn-ghost btn-sm" on:click={() => handleNewFolder(prompt('文件夹名称：') || '')}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><line x1="12" y1="11" x2="12" y2="17"/><line x1="9" y1="14" x2="15" y2="14"/></svg>
          新建文件夹
        </button>
        <div class="view-toggle">
          <button class="btn btn-ghost btn-sm" class:active={$viewMode === 'list'} on:click={() => viewMode.set('list')} title="列表视图">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
          </button>
          <button class="btn btn-ghost btn-sm" class:active={$viewMode === 'grid'} on:click={() => viewMode.set('grid')} title="网格视图">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></svg>
          </button>
        </div>
      </div>
    </div>

    <div class="sort-bar">
      <button class="sort-btn" class:active={$sortBy === 'name'} on:click={() => handleSort('name')}>
        名称 {$sortBy === 'name' ? ($sortDesc ? '↑' : '↓') : ''}
      </button>
      <button class="sort-btn" class:active={$sortBy === 'size'} on:click={() => handleSort('size')}>
        大小 {$sortBy === 'size' ? ($sortDesc ? '↑' : '↓') : ''}
      </button>
      <button class="sort-btn" class:active={$sortBy === 'time'} on:click={() => handleSort('time')}>
        修改时间 {$sortBy === 'time' ? ($sortDesc ? '↑' : '↓') : ''}
      </button>
      <span class="file-count">{$totalFiles} 个项目</span>
    </div>

    <main class="content">
      {#if $isLoading}
        <div class="loading"><div class="spinner"></div></div>
      {:else if searchActive && $searchQuery}
        <div class="search-results">
          <div class="search-header">
            搜索结果: "{$searchQuery}"
            <button class="btn btn-ghost btn-sm" on:click={() => { searchQuery.set(''); searchActive = false; searchResults.set([]) }}>清除</button>
          </div>
          <FileGrid
            files={$searchResults}
            on:navigate={e => handleNavigate(e.detail)}
            on:preview={e => handlePreview(e.detail)}
            on:download={e => downloadFile(e.detail)}
            on:delete={e => handleDelete(e.detail)}
            on:rename={e => { const n = prompt('新名称:', e.detail.split('/').pop()); if (n) handleRename(e.detail, n) }}
          />
        </div>
      {:else if $files.length === 0}
        <div class="empty-state">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" style="color: var(--text2);">
            <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
          </svg>
          <p>此目录为空</p>
          <button class="btn btn-primary btn-sm" on:click={() => $showUpload = true}>上传文件</button>
        </div>
      {:else if $viewMode === 'grid'}
        <FileGrid
          files={$files}
          on:navigate={e => handleNavigate(e.detail)}
          on:preview={e => handlePreview(e.detail)}
          on:download={e => downloadFile(e.detail)}
          on:delete={e => handleDelete(e.detail)}
          on:rename={e => { const n = prompt('新名称:', e.detail.split('/').pop()); if (n) handleRename(e.detail, n) }}
        />
      {:else}
        <FileList
          files={$files}
          selected={$selectedFiles}
          on:navigate={e => handleNavigate(e.detail)}
          on:preview={e => handlePreview(e.detail)}
          on:download={e => downloadFile(e.detail)}
          on:delete={e => handleDelete(e.detail)}
          on:rename={e => { const n = prompt('新名称:', e.detail.split('/').pop()); if (n) handleRename(e.detail, n) }}
          on:select={e => handleSelect(e.detail)}
          on:selectall={handleSelectAll}
        />
      {/if}
    </main>
  </div>

  {#if $previewFile}
    <FilePreview
      file={$previewFile}
      on:close={() => $previewFile = null}
    />
  {/if}

  {#if $showUpload}
    <UploadDialog
      currentPath={$currentPath}
      on:close={() => $showUpload = false}
      on:upload={e => handleUpload(e.detail.path, e.detail.file)}
    />
  {/if}

  {#if $showSettings}
    <StorageSettings on:changed={handleMountsChanged} on:close={() => $showSettings = false} />
  {/if}
{/if}

{#if $notification}
  <div class="notification {$notification.type}" on:click={() => notification.set(null)}>
    {$notification.msg}
  </div>
{/if}

<style>
  .loading-screen {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100vh;
  }

  .app {
    display: flex;
    flex-direction: column;
    height: 100vh;
  }

  .header {
    display: flex;
    align-items: center;
    padding: 0 16px;
    height: var(--header-h);
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    gap: 16px;
    flex-shrink: 0;
  }

  .logo {
    font-weight: 700;
    font-size: 1.1rem;
    color: var(--primary);
    white-space: nowrap;
  }

  .header-center {
    flex: 1;
    max-width: 400px;
  }

  .search-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--surface2);
    border-radius: 24px;
    padding: 6px 14px;
    border: 1px solid transparent;
    transition: border-color 0.15s;
  }
  .search-bar:focus-within {
    border-color: var(--primary);
    background: var(--surface);
  }
  .search-bar input {
    border: none;
    background: none;
    padding: 0;
    width: 100%;
    outline: none;
  }
  .search-bar svg {
    color: var(--text2);
    flex-shrink: 0;
  }

  .header-right {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 16px;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
    gap: 12px;
    flex-wrap: wrap;
  }

  .toolbar-actions {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .view-toggle {
    display: flex;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }
  .view-toggle .btn {
    border-radius: 0;
  }
  .view-toggle .active {
    background: var(--primary);
    color: white;
  }

  .sort-bar {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 16px;
    background: var(--surface2);
    border-bottom: 1px solid var(--border);
    font-size: 0.8rem;
    flex-shrink: 0;
  }

  .sort-btn {
    padding: 4px 10px;
    border-radius: 4px;
    color: var(--text2);
    font-size: 0.8rem;
    cursor: pointer;
    background: none;
    border: none;
    transition: all 0.1s;
  }
  .sort-btn:hover { color: var(--text); background: rgba(0,0,0,0.05); }
  .sort-btn.active { color: var(--primary); font-weight: 600; }

  .file-count {
    margin-left: auto;
    color: var(--text2);
    font-size: 0.8rem;
  }

  .content {
    flex: 1;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
  }

  .loading {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 64px;
  }

  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 80px 20px;
    gap: 16px;
    color: var(--text2);
  }

  .search-results {
    padding: 8px;
  }
  .search-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 12px;
    font-size: 0.9rem;
    color: var(--text2);
  }

  @media (max-width: 640px) {
    .header { padding: 0 12px; gap: 8px; }
    .header-center { max-width: none; }
    .logo { font-size: 1rem; }
    .toolbar { padding: 6px 12px; }
    .sort-bar { padding: 4px 8px; gap: 2px; }
    .sort-btn { padding: 4px 6px; font-size: 0.75rem; }
  }
</style>
