const BASE = '/api/v1'

async function request(path, options = {}) {
  const res = await fetch(BASE + path, {
    headers: { 'Content-Type': 'application/json', ...options.headers },
    ...options,
  })
  if (!res.ok) {
    let msg = ''
    try {
      const j = await res.json()
      msg = j.message || ''
    } catch {
      /* 非 JSON 错误体 */
    }
    // 会话过期 / 未登录：广播给 App 跳转登录页
    if (res.status === 401) {
      window.dispatchEvent(new CustomEvent('cc-unauthorized'))
    }
    const err = new Error(msg || `HTTP ${res.status}`)
    err.status = res.status
    throw err
  }
  return res.status === 204 ? null : res.json()
}

export function getJSON(path) {
  return request(path)
}

export function postJSON(path, body) {
  return request(path, { method: 'POST', body: JSON.stringify(body ?? {}) })
}

export function putJSON(path, body) {
  return request(path, { method: 'PUT', body: JSON.stringify(body ?? {}) })
}

export function delJSON(path) {
  return request(path, { method: 'DELETE' })
}
