import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

// /admin 入口归一：#/admin（哈希旧写法）、大小写变体统一改写为规范 /admin，
// 且必须在 createWebHistory 读取地址之前执行；保证后续守卫一律识别为后台入口，
// 转管理员密码认证（?next=/admin），认证通过后回跳 /admin。
;(function normalizeAdminPath() {
  const hashMatch = /^#(\/.*)$/.exec(window.location.hash)
  const raw = hashMatch ? hashMatch[1] : window.location.pathname
  const m = /^\/admin/i.exec(raw)
  if (!m) return
  const rest = raw.slice(m[0].length)
  if (rest && !/^[/?]/.test(rest)) return // /administrator 等不处理
  const canonical = '/admin' + rest
  if (canonical === raw && !hashMatch) return
  window.history.replaceState(null, '', canonical)
})()

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'overview', component: () => import('../views/Overview.vue') },
    { path: '/alerts', name: 'alerts', component: () => import('../views/Alerts.vue') },
    {
      path: '/device/:id',
      name: 'device',
      component: () => import('../views/DeviceDetail.vue'),
      props: true,
    },
    {
      path: '/admin',
      name: 'admin',
      component: () => import('../views/Admin.vue'),
      meta: { admin: true },
    },
    { path: '/login', name: 'login', component: () => import('../views/Login.vue') },
    // 兼容直接访问登录路径上的旧写法
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// 运行中把地址改成 #/admin（同页哈希变化不重新加载文档，上面的归一不会执行）
// 也要转成历史路由 /admin，随后由守卫走管理员认证并回跳
window.addEventListener('hashchange', () => {
  const m = /^#(\/.*)$/.exec(window.location.hash)
  if (!m) return
  const path = m[1]
  window.history.replaceState(null, '', path)
  if (router.currentRoute.value.fullPath !== path) router.replace(path)
})

// 路由守卫：后台必须管理员会话；开启访问密码时前台需登录
// 访问 /admin（未持管理员会话，含仅访客会话）→ /login?next=/admin 走管理员密码认证，
// 登录成功后 Login.vue 按 next 回跳 /admin。
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.ensureReady()
  if (to.meta.admin && !auth.isAdmin) {
    return { path: '/login', query: { next: to.fullPath } }
  }
  if (auth.viewEnabled && !auth.authed && to.name !== 'login') {
    return { path: '/login', query: { next: to.fullPath } }
  }
  return true
})

export default router
