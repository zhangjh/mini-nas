<script>
  export let onLogin = async () => {}

  let password = ''
  let loading = false
  let error = ''

  async function submit() {
    if (!password) return
    loading = true
    error = ''
    try {
      await onLogin(password)
    } catch (e) {
      error = e.message || '登录失败'
    }
    loading = false
  }
</script>

<div class="login-page">
  <div class="login-card">
    <div class="login-icon">
      <svg width="40" height="40" viewBox="0 0 64 64" fill="none">
        <rect width="64" height="64" rx="12" fill="#3b82f6"/>
        <path d="M16 22h14l6 6h12v20H16V22z" fill="#fff" opacity="0.9"/>
        <circle cx="32" cy="36" r="5" fill="#3b82f6" stroke="#fff" stroke-width="2"/>
      </svg>
    </div>
    <h2>mini-nas</h2>
    <p class="subtitle">轻量级文件同步系统</p>
    <form on:submit|preventDefault={submit}>
      <div class="field">
        <input
          type="password"
          placeholder="请输入密码"
          bind:value={password}
          disabled={loading}
        />
      </div>
      {#if error}
        <div class="error">{error}</div>
      {/if}
      <button type="submit" class="btn btn-primary login-btn" disabled={loading}>
        {loading ? '登录中...' : '登录'}
      </button>
    </form>
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

  h2 {
    font-size: 1.5rem;
    margin-bottom: 4px;
  }

  .subtitle {
    color: var(--text2);
    font-size: 0.9rem;
    margin-bottom: 24px;
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

  .error {
    color: var(--danger);
    font-size: 0.875rem;
    margin-bottom: 12px;
  }
</style>
