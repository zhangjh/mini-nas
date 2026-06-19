<script>
  import { createEventDispatcher } from 'svelte'
  import { uploadFile } from '../lib/api.js'

  export let currentPath = ''
  const dispatch = createEventDispatcher()

  let files = []
  let uploading = false
  let uploads = []
  let dragOver = false

  function onFileSelect(e) {
    addFiles(Array.from(e.target.files || []))
  }

  function onDrop(e) {
    e.preventDefault()
    dragOver = false
    addFiles(Array.from(e.dataTransfer.files || []))
  }

  function addFiles(newFiles) {
    files = [...files, ...newFiles].filter((f, i, arr) => arr.indexOf(f) === i)
  }

  function removeFile(index) {
    files = files.filter((_, i) => i !== index)
  }

  async function startUpload() {
    if (!files.length) return
    uploading = true
    uploads = files.map(f => ({ name: f.name, progress: 0, status: 'pending', error: '' }))

    for (let i = 0; i < files.length; i++) {
      const file = files[i]
      uploads[i].status = 'uploading'
      try {
        await new Promise((resolve, reject) => {
          uploadFile(currentPath, file, (p) => {
            uploads[i].progress = Math.round(p * 100)
          }).then(resolve).catch(reject)
        })
        uploads[i].status = 'done'
        uploads[i].progress = 100
      } catch (e) {
        uploads[i].status = 'error'
        uploads[i].error = e.message
      }
    }

    uploading = false
    dispatch('close')
  }

  function close() {
    if (!uploading) dispatch('close')
  }
</script>

<div class="overlay" on:click|self={close}>
  <div class="dialog">
    <div class="dialog-header">
      <h3>上传文件</h3>
      <button class="btn btn-ghost btn-sm" on:click={close}>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </button>
    </div>

    {#if !uploads.length}
      <div
        class="dropzone"
        class:drag-over={dragOver}
        on:dragover|preventDefault={() => dragOver = true}
        on:dragleave={() => dragOver = false}
        on:drop={onDrop}
        on:click={() => document.getElementById('file-input').click()}
        role="button"
      >
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="var(--text2)" stroke-width="1.5">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
          <polyline points="17 8 12 3 7 8"/>
          <line x1="12" y1="3" x2="12" y2="15"/>
        </svg>
        <p>拖拽文件到此处，或点击选择</p>
        <p class="hint">目标目录: {currentPath || '根目录'}</p>
        <input id="file-input" type="file" multiple on:change={onFileSelect} hidden />
      </div>
    {/if}

    {#if files.length > 0}
      <div class="file-list">
        {#each files as file, i}
          <div class="file-item">
            <span class="file-name">{file.name}</span>
            <span class="file-size">{(file.size / 1024 / 1024).toFixed(1)} MB</span>
            {#if !uploads.length}
              <button class="btn btn-ghost btn-sm" on:click={() => removeFile(i)}>移除</button>
            {:else if uploads[i]}
              <div class="upload-status">
                {#if uploads[i].status === 'pending'}
                  <span class="pending">等待</span>
                {:else if uploads[i].status === 'uploading'}
                  <div class="progress-bar">
                    <div class="progress-fill" style="width: {uploads[i].progress}%"></div>
                  </div>
                  <span class="progress-text">{uploads[i].progress}%</span>
                {:else if uploads[i].status === 'done'}
                  <span class="done">✓</span>
                {:else}
                  <span class="error-text" title={uploads[i].error}>✗</span>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}

    {#if !uploads.length}
      <div class="dialog-actions">
        <button class="btn btn-ghost" on:click={close}>取消</button>
        <button class="btn btn-primary" on:click={startUpload} disabled={!files.length}>
          上传 ({files.length} 个文件)
        </button>
      </div>
    {:else if uploading}
      <div class="dialog-actions">
        <span class="uploading-text">上传中...</span>
      </div>
    {/if}
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

  .dialog {
    background: var(--surface);
    border-radius: 12px;
    width: 100%;
    max-width: 480px;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: var(--shadow-lg);
  }

  .dialog-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
  }
  .dialog-header h3 { font-size: 1.1rem; }

  .dropzone {
    margin: 16px;
    padding: 40px 20px;
    border: 2px dashed var(--border);
    border-radius: 8px;
    text-align: center;
    cursor: pointer;
    transition: all 0.15s;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
  }
  .dropzone:hover, .dropzone.drag-over {
    border-color: var(--primary);
    background: rgba(59,130,246,0.04);
  }
  .dropzone p { color: var(--text2); font-size: 0.9rem; }
  .dropzone .hint { font-size: 0.8rem; color: var(--text2); }

  .file-list {
    padding: 8px 16px;
    max-height: 240px;
    overflow-y: auto;
  }

  .file-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.9rem;
  }
  .file-item:last-child { border-bottom: none; }

  .file-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .file-size { color: var(--text2); font-size: 0.8rem; flex-shrink: 0; }

  .upload-status {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }

  .progress-bar {
    width: 80px;
    height: 6px;
    background: var(--surface2);
    border-radius: 3px;
    overflow: hidden;
  }
  .progress-fill {
    height: 100%;
    background: var(--primary);
    border-radius: 3px;
    transition: width 0.2s;
  }
  .progress-text { font-size: 0.8rem; color: var(--primary); min-width: 32px; }
  .done { color: var(--success); font-weight: 700; }
  .error-text { color: var(--danger); cursor: help; }

  .dialog-actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    padding: 12px 20px;
    border-top: 1px solid var(--border);
  }

  .uploading-text {
    color: var(--text2);
    font-size: 0.9rem;
  }

  @media (max-width: 640px) {
    .dialog { max-height: 90vh; }
  }
</style>
