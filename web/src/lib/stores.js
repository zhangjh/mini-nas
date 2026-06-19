import { writable, derived } from 'svelte/store'

export const currentPath = writable('')
export const files = writable([])
export const totalFiles = writable(0)
export const isLoading = writable(false)
export const viewMode = writable('list')
export const sortBy = writable('name')
export const sortDesc = writable(false)
export const selectedFiles = writable(new Set())
export const previewFile = writable(null)
export const showUpload = writable(false)
export const showNewFolder = writable(false)
export const showSettings = writable(false)
export const searchQuery = writable('')
export const searchResults = writable([])
export const notification = writable(null)

export function notify(msg, type = 'info') {
  notification.set({ msg, type })
  setTimeout(() => notification.set(null), 3000)
}

export const isAuthenticated = writable(!!localStorage.getItem('token'))

export const currentDirName = derived(currentPath, $p => {
  if (!$p || $p === '.') return '根目录'
  const parts = $p.split('/')
  return parts[parts.length - 1]
})
