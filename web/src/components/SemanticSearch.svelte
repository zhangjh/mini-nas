<script>
  import { onMount, onDestroy, createEventDispatcher } from 'svelte'
  import {
    semanticSearch, similarSearch, indexStatus, indexJob,
    startIndex, cancelIndex, thumbUrl
  } from '../lib/api.js'

  // seed 非空时直接以图搜图（由文件网格的「找相似」触发）。
  export let seed = ''
  const dispatch = createEventDispatcher()

  let query = ''
  let minScore = ''
  let items = []
  let loading = false
  let searched = false
  let tookMs = 0
  let candidates = 0
  let error = ''
  let status = null
  let job = null
  let timer = null

  $: running = job && (job.state === 'RUNNING' || job.state === 'PENDING')
  $: percent = running && job.total > 0 ? Math.min(100, (job.done / job.total) * 100) : 0

  onMount(async () => {
    await refreshStatus()
    if (seed) {
      query = ''
      await runSimilar(seed)
    }
  })

  onDestroy(() => stopPolling())

  async function refreshStatus() {
    try {
      status = await indexStatus()
      if (status && status.running) {
        job = status.running
        startPolling()
      }
    } catch (e) {
      status = { available: false, reason: e.message }
    }
  }

  function startPolling() {
    if (timer || !job) return
    timer = setInterval(async () => {
      try {
        job = await indexJob(job.id)
        if (job.state !== 'RUNNING' && job.state !== 'PENDING') {
          stopPolling()
          job = null
          await refreshStatus()
        }
      } catch {
        stopPolling()
      }
    }, 2000)
  }

  function stopPolling() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  async function runSearch() {
    const q = query.trim()
    if (!q || loading) return
    loading = true
    error = ''
    try {
      const r = await semanticSearch(q, 60, parseFloat(minScore) || 0)
      applyResult(r)
    } catch (e) {
      error = e.message
      items = []
    }
    loading = false
  }

  async function runSimilar(path) {
    if (!path || loading) return
    loading = true
    error = ''
    try {
      const r = await similarSearch(path, 60, parseFloat(minScore) || 0)
      applyResult(r)
    } catch (e) {
      error = e.message
      items = []
    }
    loading = false
  }

  function applyResult(r) {
    items = r.items || []
    tookMs = r.took_ms || 0
    candidates = r.total_candidates || 0
    searched = true
  }

  async function handleIndex(full) {
    error = ''
    try {
      job = await startIndex(full)
      startPolling()
      await refreshStatus()
    } catch (e) {
      error = e.message
    }
  }

  async function handleCancel() {
    if (!job) return
    try {
      await cancelIndex(job.id)
    } catch (e) {
      error = e.message
    }
  }

  function open(item) {
    const name = item.path.split('/').pop()
    dispatch('preview', { name, path: item.path, size: item.size || 0, is_dir: false, mod_time: item.mtime || 0 })
  }

  function close(e) {
    if (e && e.target !== e.currentTarget) return
    dispatch('close')
  }

  function fmtScore(v) {
    return (v * 100).toFixed(1)
  }

  function fmtSize(bytes) {
    if (!bytes) return ''
    const units = ['B', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
  }
</script>

<div class="overlay" on:click={close} role="presentation">
  <div class="panel" role="dialog" aria-label="语义检索">
    <div class="panel-head">
      <div class="title">
        智能检索
        {#if status && status.available}
          <span class="pill ok">{status.device || 'CPU'} · {status.indexed}/{status.total}</span>
        {:else}
          <span class="pill off">未启用</span>
        {/if}
      </div>
      <button class="btn btn-ghost btn-sm" on:click={() => dispatch('close')}>关闭</button>
    </div>

    {#if status && !status.available}
      <div class="notice">
        语义检索不可用：{status.reason || '模型未加载或当前构建不含推理能力'}。
        该功能需要 cgo 构建 + onnxruntime，详见部署文档。
      </div>
    {:else}
      <div class="search-row">
        <input
          class="input"
          type="text"
          placeholder="用一句话描述你要找的图片，例如「海边日落」「一只猫在窗台上」"
          bind:value={query}
          on:keydown={e => e.key === 'Enter' && runSearch()}
        />
        <input class="input score" type="number" step="0.01" min="0" max="1"
          placeholder="阈值" bind:value={minScore} title="最低相似度，留空用默认值" />
        <button class="btn btn-primary btn-sm" on:click={runSearch} disabled={loading || !query.trim()}>
          {loading ? '检索中…' : '检索'}
        </button>
      </div>

      <div class="index-row">
        <span class="meta">
          已索引 {status?.indexed ?? 0} / {status?.total ?? 0} 张
          {#if status?.failed}· 失败 {status.failed}{/if}
          {#if status?.pending}· 待索引 {status.pending}{/if}
        </span>
        <div class="index-actions">
          {#if running}
            <button class="btn btn-ghost btn-sm" on:click={handleCancel}>取消索引</button>
          {:else}
            <button class="btn btn-ghost btn-sm" on:click={() => handleIndex(false)}>增量索引</button>
            <button class="btn btn-ghost btn-sm" on:click={() => handleIndex(true)}>全量重建</button>
          {/if}
        </div>
      </div>

      {#if running}
        <div class="progress">
          <div class="bar" style="width: {percent}%"></div>
          <span class="progress-text">
            {job.message || '索引中'} · {job.done}/{job.total}
          </span>
        </div>
      {/if}

      {#if error}
        <div class="error">{error}</div>
      {/if}

      {#if loading}
        <div class="state"><div class="spinner"></div></div>
      {:else if items.length > 0}
        <div class="result-head">
          {items.length} 个结果 · {tookMs} ms · 候选 {candidates}
        </div>
        <div class="results">
          {#each items as item}
            <button class="hit" on:click={() => open(item)} title={item.path}>
              <div class="hit-thumb">
                <img src={thumbUrl(item.path)} alt="" loading="lazy" />
                <span class="score">{fmtScore(item.score)}%</span>
              </div>
              <div class="hit-name">{item.path.split('/').pop()}</div>
              <div class="hit-meta">{fmtSize(item.size)}</div>
            </button>
          {/each}
        </div>
      {:else if searched}
        <div class="state">没有匹配的图片。可以先把索引跑起来，或降低相似度阈值。</div>
      {:else}
        <div class="state">
          支持中文自然语言描述。索引只覆盖图片，视频与文档不在范围内。
        </div>
      {/if}
    {/if}
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.35);
    z-index: 400;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: 48px 16px 16px;
    overflow-y: auto;
  }
  .panel {
    width: min(960px, 100%);
    background: var(--surface);
    border-radius: 12px;
    box-shadow: var(--shadow-lg);
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .panel-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .title {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 600;
  }
  .pill {
    font-size: 0.75rem;
    font-weight: 500;
    padding: 2px 8px;
    border-radius: 999px;
  }
  .pill.ok { background: rgba(34, 197, 94, 0.12); color: var(--success); }
  .pill.off { background: rgba(239, 68, 68, 0.12); color: var(--danger); }

  .search-row { display: flex; gap: 8px; }
  .input {
    flex: 1;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    outline: none;
  }
  .input:focus { border-color: var(--primary); }
  .input.score { flex: 0 0 88px; }

  .index-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    font-size: 0.85rem;
    color: var(--text2);
    flex-wrap: wrap;
  }
  .index-actions { display: flex; gap: 4px; }

  .progress {
    position: relative;
    height: 22px;
    background: var(--surface2);
    border-radius: 999px;
    overflow: hidden;
  }
  .progress .bar {
    height: 100%;
    background: var(--primary);
    transition: width 0.4s;
  }
  .progress-text {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.75rem;
    color: var(--text);
  }

  .notice {
    padding: 12px;
    border-radius: var(--radius);
    background: rgba(245, 158, 11, 0.1);
    color: var(--text);
    font-size: 0.9rem;
  }
  .error {
    padding: 10px 12px;
    border-radius: var(--radius);
    background: rgba(239, 68, 68, 0.1);
    color: var(--danger);
    font-size: 0.9rem;
  }

  .result-head { font-size: 0.8rem; color: var(--text2); }
  .results {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 8px;
    max-height: 60vh;
    overflow-y: auto;
  }
  .hit {
    text-align: left;
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
    background: var(--surface);
    padding: 0;
    transition: transform 0.1s, box-shadow 0.15s;
  }
  .hit:hover { transform: translateY(-2px); box-shadow: var(--shadow); }
  .hit-thumb {
    position: relative;
    aspect-ratio: 1;
    background: var(--surface2);
  }
  .hit-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .score {
    position: absolute;
    right: 6px;
    bottom: 6px;
    background: rgba(0, 0, 0, 0.6);
    color: #fff;
    font-size: 0.7rem;
    padding: 1px 6px;
    border-radius: 999px;
  }
  .hit-name {
    padding: 6px 8px 0;
    font-size: 0.78rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .hit-meta {
    padding: 0 8px 6px;
    font-size: 0.72rem;
    color: var(--text2);
  }

  .state {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 40px 16px;
    color: var(--text2);
    font-size: 0.9rem;
    text-align: center;
  }
</style>
