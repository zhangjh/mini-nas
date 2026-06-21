<script>
  import { onMount } from 'svelte'
  import { authStatus } from '../lib/api.js'

  export let onLogin = async (username, password) => {}
  export let onRegister = async (username, password) => {}

  let username = ''
  let password = ''
  let loading = false
  let error = ''
  let mode = 'login' // 'login' | 'register'
  let needsSetup = false
  let checking = true

  onMount(async () => {
    try {
      const status = await authStatus()
      needsSetup = !status.has_users
      if (needsSetup) mode = 'register'
    } catch {
      // ignore, default to login
    }
    checking = false
  })

  async function submit() {
    if (!username || !password) return
    loading = true
    error = ''
    try {
      if (mode === 'register') {
        await onRegister(username, password)
      } else {
        await onLogin(username, password)
      }
    } catch (e) {
      error = e.message || (mode === 'register' ? '注册失败' : '登录失败')
    }
    loading = false
  }

  function switchMode() {
    mode = mode === 'login' ? 'register' : 'login'
    error = ''
  }
</script>

<div class="login-page">
  <div class="login-card">
    <div class="login-icon">
      <img src="/logo.png" alt="mini-nas" class="logo-img" />
    </div>
    <h2>mini-nas</h2>
    <p class="subtitle">
      {#if checking}
        正在加载...
      {:else if needsSetup && mode === 'register'}
        首次使用，请创建管理员账号
      {:else if mode === 'register'}
        注册新账号
      {:else}
        轻量级文件同步系统
      {/if}
    </p>
    {#if !checking}
      <form on:submit|preventDefault={submit}>
        <div class="field">
          <input
            type="text"
            placeholder="用户名"
            bind:value={username}
            disabled={loading}
            autocomplete="username"
          />
        </div>
        <div class="field">
          <input
            type="password"
            placeholder="密码"
            bind:value={password}
            disabled={loading}
            autocomplete={mode === 'register' ? 'new-password' : 'current-password'}
          />
        </div>
        {#if error}
          <div class="error">{error}</div>
        {/if}
        <button type="submit" class="btn btn-primary login-btn" disabled={loading || !username || !password}>
          {loading ? (mode === 'register' ? '注册中...' : '登录中...') : (mode === 'register' ? '注册' : '登录')}
        </button>
      </form>
      {#if !needsSetup}
        <button class="switch-mode" on:click={switchMode} disabled={loading}>
          {mode === 'login' ? '没有账号？去注册' : '已有账号？去登录'}
        </button>
      {/if}
    {/if}
  </div>
</div>

<style>
  .login-page {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: 20px;
    background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  }

  .login-card {
    background: var(--surface);
    border-radius: 16px;
    padding: 40px 32px;
    width: 100%;
    max-width: 360px;
    text-align: center;
    box-shadow: var(--shadow-lg);
  }

  .login-icon {
    margin-bottom: 16px;
    display: inline-block;
  }

  .login-icon .logo-img {
    width: 56px;
    height: 56px;
    border-radius: 14px;
  }

  h2 {
    font-size: 1.5rem;
    margin-bottom: 4px;
  }

  .subtitle {
    color: var(--text2);
    font-size: 0.9rem;
    margin-bottom: 24px;
    min-height: 1.2em;
  }

  .field {
    margin-bottom: 16px;
  }

  .login-btn {
    width: 100%;
    justify-content: center;
    padding: 12px;
    font-size: 1rem;
  }

  .switch-mode {
    margin-top: 16px;
    background: none;
    border: none;
    color: var(--primary);
    font-size: 0.85rem;
    cursor: pointer;
    padding: 4px;
  }
  .switch-mode:hover {
    text-decoration: underline;
  }

  .error {
    color: var(--danger);
    font-size: 0.875rem;
    margin-bottom: 12px;
  }
</style>
