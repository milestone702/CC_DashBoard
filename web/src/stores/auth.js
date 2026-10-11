import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getJSON, postJSON } from '../api'

// 用户侧认证状态（管理员密码 / 前台访问密码 / 会话）
export const useAuthStore = defineStore('auth', () => {
  const status = ref({ admin_set: false, view_enabled: false })
  const scope = ref('') // '' | 'view' | 'admin'
  let readyP = null

  const isAdmin = computed(() => scope.value === 'admin')
  const viewEnabled = computed(() => status.value.view_enabled)
  const authed = computed(() => scope.value !== '')

  async function refresh() {
    try {
      status.value = await getJSON('/auth/status')
    } catch {
      /* 后端未连：保持默认 */
    }
    try {
      const w = await getJSON('/auth/whoami')
      scope.value = w.scope || ''
    } catch {
      scope.value = ''
    }
  }

  function ensureReady() {
    if (!readyP) {
      readyP = refresh().finally(() => {
        readyP = null // 失败后允许重试
      })
    }
    return readyP
  }

  async function setup(password) {
    await postJSON('/auth/setup', { password })
    await refresh()
  }

  async function login(password, s) {
    await postJSON('/auth/login', { password, scope: s })
    await refresh()
  }

  async function logout() {
    try {
      await postJSON('/auth/logout', {})
    } finally {
      scope.value = ''
    }
  }

  return { status, scope, isAdmin, viewEnabled, authed, refresh, ensureReady, setup, login, logout }
})
