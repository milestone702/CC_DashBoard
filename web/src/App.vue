<template>
  <div class="min-h-screen">
    <header class="sticky top-0 z-10 border-b border-slate-800 bg-slate-900/80 backdrop">
      <div class="mx-auto flex max-w-7xl items-center gap-6 px-4 py-3">
        <h1 class="text-lg font-semibold tracking-wide text-emerald-400">{{ siteTitle }}</h1>
        <nav class="flex gap-4 text-sm">
          <router-link
            v-for="item in visibleNavs"
            :key="item.path"
            :to="item.path"
            class="rounded px-3 py-1.5 text-slate-300 hover:bg-slate-800"
            active-class="!bg-slate-800 !text-emerald-400"
          >
            {{ item.title }}
          </router-link>
        </nav>
        <div class="ml-auto flex items-center gap-3 text-xs">
          <span v-if="auth.authed" class="rounded-full bg-slate-800 px-2.5 py-1 text-slate-400">
            {{ auth.isAdmin ? '管理员' : '访客' }}
          </span>
          <button
            v-if="auth.authed"
            class="rounded bg-slate-800 px-2.5 py-1 text-slate-300 hover:bg-slate-700"
            @click="onLogout"
          >
            退出
          </button>
          <router-link v-else-if="auth.status.admin_set" to="/login" class="text-slate-400 hover:text-slate-200">
            登录
          </router-link>
        </div>
      </div>
    </header>
    <main class="mx-auto max-w-7xl px-4 py-6">
      <router-view />
    </main>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useNodesStore } from './stores/nodes'
import { useAuthStore } from './stores/auth'
import { siteTitle, loadSiteTitle } from './site'

const allNavs = [
  { path: '/', title: '全局概览' },
  { path: '/admin', title: '节点管理', admin: true },
]

const router = useRouter()
const store = useNodesStore()
const auth = useAuthStore()

const visibleNavs = computed(() => allNavs.filter((n) => !n.admin || auth.isAdmin))

function onLogout() {
  auth.logout().then(() => router.push('/'))
}

// 会话过期（任意接口 401）→ 清状态并跳登录页，带 return 地址
function onUnauthorized() {
  auth.scope = ''
  const r = router.currentRoute.value
  if (r.name !== 'login') {
    router.push({ path: '/login', query: { next: r.fullPath } })
  }
}

onMounted(async () => {
  window.addEventListener('cc-unauthorized', onUnauthorized)
  loadSiteTitle() // 站点标题来自服务端 settings，先于认证状态加载
  // 先等认证状态：开启访问密码时避免未登录拉取被误判为演示模式
  await auth.ensureReady()
  store.init()
})
// 登录成功（含从登录页返回）后重新初始化数据通道
watch(() => auth.authed, (v) => {
  if (v) store.init()
})
onBeforeUnmount(() => {
  window.removeEventListener('cc-unauthorized', onUnauthorized)
  store.dispose()
})
</script>
