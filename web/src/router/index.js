import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'overview', component: () => import('../views/Overview.vue') },
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

// 路由守卫：后台必须管理员会话；开启访问密码时前台需登录
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
