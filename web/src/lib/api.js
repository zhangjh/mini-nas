const BASE = '/api'

let token = localStorage.getItem('token')

export function setToken(t) {
  token = t
  if (t) localStorage.setItem('token', t)
  else localStorage.removeItem('token')
}

export function getToken() {
  return token
}

export function isAuthenticated() {
  return !!token
}

async function request(method, path, opts = {}) {
  const headers = { ...opts.headers }
  if (token) headers['Authorization'] = `Bearer ${token}`
  if (opts.body && typeof opts.body === 'object' && !(opts.body instanceof FormData)) {
    headers['Content-Type'] = 'application/json'
  }

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: opts.body,
    signal: opts.signal
  })

  const ct = res.headers.get('content-type') || ''
  if (ct.includes('application/json')) {
    const data = await res.json()
    if (!data.ok) throw new Error(data.error || 'Request failed')
    return data.data
  }

  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }

  return res
}

export function login(password) {
  return request('POST', '/auth/login', {
    body: JSON.stringify({ password })
  })
}

export function listFiles(path, opts = {}) {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  if (opts.page) params.set('page', opts.page)
  if (opts.pageSize) params.set('page_size', opts.pageSize)
  if (opts.sortBy) params.set('sort_by', opts.sortBy)
  if (opts.sortDesc) params.set('sort_desc', opts.sortDesc)
  return request('GET', `/files?${params}`)
}

export function getFileInfo(path) {
  return request('GET', `/files/info?path=${encodeURIComponent(path)}`)
}

export function searchFiles(q, path) {
  const params = new URLSearchParams({ q })
  if (path) params.set('path', path)
  return request('GET', `/files/search?${params}`)
}

export function uploadFile(path, file, onProgress) {
  return new Promise((resolve, reject) => {
    const form = new FormData()
    form.append('file', file)

    const xhr = new XMLHttpRequest()
    xhr.open('POST', `${BASE}/files/upload?path=${encodeURIComponent(path || '')}`)

    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)

    xhr.upload.onprogress = (e) => {
      if (onProgress && e.lengthComputable) {
        onProgress(e.loaded / e.total)
      }
    }

    xhr.onload = () => {
      try {
        const data = JSON.parse(xhr.responseText)
        if (data.ok) resolve(data.data)
        else reject(new Error(data.error))
      } catch (e) {
        reject(new Error('Upload failed'))
      }
    }

    xhr.onerror = () => reject(new Error('Network error'))
    xhr.send(form)
  })
}

export function downloadFile(path) {
  const url = `${BASE}/files/download?path=${encodeURIComponent(path)}`
  const a = document.createElement('a')
  a.href = url
  if (token) {
    const xhr = new XMLHttpRequest()
    xhr.open('GET', url, true)
    xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.responseType = 'blob'
    xhr.onload = () => {
      const blob = xhr.response
      const blobUrl = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = blobUrl
      link.download = path.split('/').pop()
      link.click()
      URL.revokeObjectURL(blobUrl)
    }
    xhr.send()
  }
}

export function previewUrl(path) {
  return `${BASE}/files/preview?path=${encodeURIComponent(path)}&t=${Date.now()}&token=${getToken()}`
}

export function thumbUrl(path) {
  return `${BASE}/thumb?path=${encodeURIComponent(path)}&token=${getToken()}`
}

export function mkdir(path) {
  return request('POST', '/mkdir', {
    body: JSON.stringify({ path })
  })
}

export function deleteFile(path) {
  return request('DELETE', '/file', {
    body: JSON.stringify({ path })
  })
}

export function renameFile(path, newName) {
  return request('PUT', '/file/rename', {
    body: JSON.stringify({ path, new_name: newName })
  })
}

export function moveFiles(paths, dest) {
  return request('POST', '/file/move', {
    body: JSON.stringify({ paths, dest })
  })
}

export function copyFiles(paths, dest) {
  return request('POST', '/file/copy', {
    body: JSON.stringify({ paths, dest })
  })
}

export function healthCheck() {
  return request('GET', '/health')
}

export function listMounts() {
  return request('GET', '/mounts')
}

export function createMount(name, path) {
  return request('POST', '/mounts', {
    body: JSON.stringify({ name, path })
  })
}

export function updateMount(name, path) {
  return request('PUT', `/mounts?name=${encodeURIComponent(name)}`, {
    body: JSON.stringify({ path })
  })
}

export function deleteMount(name) {
  return request('DELETE', `/mounts?name=${encodeURIComponent(name)}`)
}

export function listSystemDirs(path) {
  return request('GET', `/system/dirs?path=${encodeURIComponent(path || '/')}`)
}
