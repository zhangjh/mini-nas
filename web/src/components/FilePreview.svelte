<script>
  import { createEventDispatcher, onMount } from 'svelte'
  import { previewUrl, thumbUrl } from '../lib/api.js'

  export let file
  export let files = []
  const dispatch = createEventDispatcher()

  let type = 'unknown'

  $: {
    if (file.is_dir) type = 'dir'
    else if (/\.(jpg|jpeg|png|gif|webp|bmp)$/i.test(file.name)) type = 'image'
    else if (/\.(mp4|mkv|avi|mov|webm)$/i.test(file.name)) type = 'video'
    else if (/\.(mp3|wav|flac|ogg|aac)$/i.test(file.name)) type = 'audio'
    else if (/\.pdf$/i.test(file.name)) type = 'pdf'
    else if (/\.(txt|md|log|csv|json|xml|yaml|yml|go|py|js|ts|html|css|java|rs)$/i.test(file.name)) type = 'text'
    else type = 'unknown'
  }

  // Sibling image files for prev/next navigation.
  const imageExt = /\.(jpg|jpeg|png|gif|webp|bmp)$/i
  $: imageFiles = files.filter(f => !f.is_dir && imageExt.test(f.name))
  $: currentIndex = imageFiles.findIndex(f => f.path === file.path)
  $: canNavigate = type === 'image' && imageFiles.length > 1

  function prev() {
    if (currentIndex > 0) dispatch('navigate', imageFiles[currentIndex - 1])
  }
  function next() {
    if (currentIndex >= 0 && currentIndex < imageFiles.length - 1) dispatch('navigate', imageFiles[currentIndex + 1])
  }

  let textContent = ''
  let loadingText = false
  let mediaError = false

  // Reset error state when file changes (e.g. navigating between images).
  $: file, mediaError = false

  function handleMediaError() {
    mediaError = true
  }

  onMount(() => {
    if (type === 'text') {
      loadingText = true
      fetch(previewUrl(file.path))
        .then(r => {
          if (!r.ok) throw new Error('HTTP ' + r.status)
          return r.text()
        })
        .then(t => { textContent = t; loadingText = false })
        .catch(() => { textContent = '加载失败'; loadingText = false })
    }
  })

  function close(e) {
    if (e.target === e.currentTarget) dispatch('close')
  }

  function handleKeydown(e) {
    if (e.key === 'Escape') dispatch('close')
    if (canNavigate) {
      if (e.key === 'ArrowLeft') prev()
      if (e.key === 'ArrowRight') next()
    }
  }
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="overlay" on:click={close} role="dialog">
  <div class="preview-container">
    <div class="preview-header">
      <span class="preview-name">{file.name}</span>
      {#if canNavigate}
        <span class="preview-counter">{currentIndex + 1} / {imageFiles.length}</span>
      {/if}
      <button class="btn btn-ghost btn-sm" on:click={() => dispatch('close')}>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </button>
    </div>
    <div class="preview-body">
      {#if canNavigate}
        <button class="nav-btn nav-prev" on:click={prev} disabled={currentIndex <= 0} title="上一张 (←)">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="15 18 9 12 15 6"/></svg>
        </button>
      {/if}
      {#if type === 'image'}
        {#if mediaError}
          <div class="preview-error">图片加载失败</div>
        {:else}
          <img src={previewUrl(file.path)} alt={file.name} class="preview-image" on:error={handleMediaError} />
        {/if}
      {:else if type === 'video'}
        {#if mediaError}
          <div class="preview-error">视频加载失败</div>
        {:else}
          <video class="preview-video" src={previewUrl(file.path)} controls autoplay on:error={handleMediaError}></video>
        {/if}
      {:else if type === 'audio'}
        <div class="audio-player">
          {#if mediaError}
            <div class="preview-error">音频加载失败</div>
          {:else}
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--primary)" stroke-width="1.5">
              <path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>
            </svg>
            <audio src={previewUrl(file.path)} controls autoplay on:error={handleMediaError}></audio>
          {/if}
        </div>
      {:else if type === 'pdf'}
        <iframe class="preview-pdf" src={previewUrl(file.path)} on:error={handleMediaError}></iframe>
      {:else if type === 'text'}
        {#if loadingText}
          <div class="preview-text-loading"><div class="spinner"></div></div>
        {:else}
          <pre class="preview-text">{textContent}</pre>
        {/if}
      {:else}
        <div class="preview-unknown">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text2)" stroke-width="1.5">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
            <polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/>
          </svg>
          <p>无法预览此文件格式</p>
          <button class="btn btn-primary" on:click={() => { const a = document.createElement('a'); a.href = previewUrl(file.path); a.download = file.name; a.click() }}>下载文件</button>
        </div>
      {/if}
      {#if canNavigate}
        <button class="nav-btn nav-next" on:click={next} disabled={currentIndex >= imageFiles.length - 1} title="下一张 (→)">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.6);
    z-index: 100;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
  }

  .preview-container {
    background: var(--surface);
    border-radius: 12px;
    width: 100%;
    max-width: 900px;
    max-height: 90vh;
    display: flex;
    flex-direction: column;
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }

  .preview-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .preview-name {
    font-weight: 600;
    font-size: 0.95rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 80%;
  }

  .preview-body {
    flex: 1;
    overflow: auto;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 200px;
    background: #000;
    position: relative;
  }

  .nav-btn {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    z-index: 10;
    background: rgba(0,0,0,0.5);
    color: white;
    border: none;
    border-radius: 50%;
    width: 44px;
    height: 44px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    transition: background 0.15s;
    flex-shrink: 0;
  }
  .nav-btn:hover:not(:disabled) { background: rgba(0,0,0,0.8); }
  .nav-btn:disabled { opacity: 0.3; cursor: default; }
  .nav-prev { left: 12px; }
  .nav-next { right: 12px; }

  .preview-counter {
    font-size: 0.8rem;
    color: var(--text2);
    margin-right: 8px;
    flex-shrink: 0;
  }

  .preview-image {
    max-width: 100%;
    max-height: 80vh;
    object-fit: contain;
  }

  .preview-video {
    width: 100%;
    max-height: 80vh;
  }

  .audio-player {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 20px;
    padding: 40px;
    background: var(--surface);
    width: 100%;
  }
  .audio-player audio { width: 100%; max-width: 400px; }

  .preview-pdf {
    width: 100%;
    height: 80vh;
    border: none;
  }

  .preview-text {
    width: 100%;
    max-height: 80vh;
    overflow: auto;
    padding: 20px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 0.85rem;
    line-height: 1.6;
    white-space: pre-wrap;
    word-break: break-all;
    background: #1e1e1e;
    color: #d4d4d4;
  }

  .preview-text-loading {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 64px;
  }

  .preview-error {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 64px 20px;
    color: var(--text2);
    font-size: 0.9rem;
    background: var(--surface);
    width: 100%;
  }

  .preview-unknown {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 16px;
    padding: 64px 20px;
    color: var(--text2);
    background: var(--surface);
  }

  @media (max-width: 640px) {
    .overlay { padding: 0; align-items: flex-end; }
    .preview-container {
      max-height: 95vh;
      border-radius: 12px 12px 0 0;
    }
  }
</style>
