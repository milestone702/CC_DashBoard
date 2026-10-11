<template>
  <div class="flex min-h-[70vh] items-center justify-center">
    <div class="w-full max-w-sm rounded-2xl border border-slate-800 bg-slate-900 p-6">
      <h1 class="mb-1 text-lg font-semibold text-emerald-400">{{ siteTitle }}</h1>
      <p class="mb-5 text-sm text-slate-400">
        {{ isSetup ? '首次使用：设置管理员密码' : scope === 'admin' ? '登录管理后台' : '输入访问密码' }}
      </p>

      <form @submit.prevent="submit">
        <label class="mb-1.5 block text-xs text-slate-500">密码（至少 6 位）</label>
        <input
          v-model="password"
          type="password"
          autocomplete="current-password"
          class="mb-3 w-full rounded-lg bg-slate-950 px-3 py-2.5 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          placeholder="••••••"
          autofocus
        />
        <template v-if="isSetup">
          <label class="mb-1.5 block text-xs text-slate-500">确认密码</label>
          <input
            v-model="confirm"
            type="password"
            class="mb-3 w-full rounded-lg bg-slate-950 px-3 py-2.5 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
            placeholder="••••••"
          />
        </template>
        <p v-if="error" class="mb-3 text-xs text-danger">{{ error }}</p>
        <button
          type="submit"
          class="w-full rounded-lg bg-emerald-700 py-2.5 text-sm font-medium text-white hover:bg-emerald-600 disabled:opacity-50"
          :disabled="loading || !password"
        >
          {{ loading ? '请稍候…' : isSetup ? '设置并进入' : '登录' }}
        </button>
      </form>

      <p class="mt-4 text-center text-[11px] text-slate-600">
        {{ scope === 'admin' ? '管理后台需管理员密码' : '前台访问密码（可在后台设置中开启）' }}
      </p>
      <router-link v-if="!isSetup" to="/" class="mt-2 block text-center text-xs text-slate-500 hover:text-slate-300">
        ← 返回概览
      </router-link>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { siteTitle } from '../site'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const password = ref('')
const confirm = ref('')
const error = ref('')
const loading = ref(false)

const isSetup = computed(() => !auth.status.admin_set)
const scope = computed(() => String(route.query.next || '').startsWith('/admin') ? 'admin' : 'view')

async function submit() {
  error.value = ''
  if (isSetup.value && password.value !== confirm.value) {
    error.value = '两次输入的密码不一致'
    return
  }
  loading.value = true
  try {
    if (isSetup.value) {
      await auth.setup(password.value)
    } else {
      await auth.login(password.value, scope.value)
    }
    const next = String(route.query.next || '/')
    router.push(next.startsWith('/') ? next : '/')
  } catch (e) {
    error.value = e.message || '登录失败'
  } finally {
    loading.value = false
  }
}

onMounted(() => auth.ensureReady())
</script>
