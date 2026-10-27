<template>
  <div class="min-h-screen">
    <header class="sticky top-0 z-10 border-b border-slate-800 bg-slate-900/80 backdrop">
      <div
        class="mx-auto flex max-w-7xl flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5 sm:gap-x-6 sm:px-4 sm:py-3"
      >
        <h1 class="text-base font-semibold tracking-wide text-emerald-400 sm:text-lg">{{ siteTitle }}</h1>
        <!-- 手机端导航独占一行且可横向滚动；桌面恢复为内联 -->
        <nav class="order-3 -mx-1 flex w-full gap-1 overflow-x-auto px-1 text-sm sm:order-none sm:mx-0 sm:w-auto sm:gap-4 sm:overflow-visible sm:px-0">
          <router-link
            v-for="item in visibleNavs"
            :key="item.path"
            :to="item.path"
            class="shrink-0 rounded px-2.5 py-1.5 text-slate-300 hover:bg-slate-800 sm:px-3"
            active-class="!bg-slate-800 !text-emerald-400"
          >
            {{ item.title }}
          </router-link>
        </nav>
        <div class="ml-auto flex items-center gap-2 text-xs sm:gap-3">
          <span v-if="auth.authed" class="rounded-full bg-slate-800 px-2 py-1 text-slate-400 sm:px-2.5">
            {{ auth.isAdmin ? '管理员' : '访客' }}
          </span>
          <button
            v-if="auth.authed"
            class="rounded bg-slate-800 px-2 py-1 text-slate-300 hover:bg-slate-700 sm:px-2.5"
            @click="onLogout"
          >
            退出
          </button>
          <router-link
            v-else-if="auth.status.admin_set && $route.name !== 'login'"
            :to="{ path: '/login', query: { next: $route.fullPath } }"
            class="text-slate-400 hover:text-slate-200"
          >
            登录
          </router-link>
        </div>
      </div>
    </header>
    <main class="mx-auto max-w-7xl px-3 pb-[env(safe-area-inset-bottom)] pt-4 sm:px-4 sm:py-6">
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
  { path: '/alerts', title: '告警' },
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
