// 站点标题：服务端 settings 持久化（换浏览器/清缓存不丢），顶栏与登录页共用。
import { ref } from 'vue'
import { getJSON } from './api'

export const siteTitle = ref('CC_DashBoard')

export async function loadSiteTitle() {
  try {
    const r = await getJSON('/site')
    if (r.title) siteTitle.value = r.title
  } catch {
    /* 未启动/网络异常：保持默认 */
  }
  document.title = siteTitle.value
}
