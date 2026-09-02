// API 呼び出しをまとめる。失敗は kind を保った Error として投げ、
// 画面側が「Ollama を起動する」「モデルを pull する」を出し分けられるようにする。

async function unwrap(res) {
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    const err = new Error(body.error || '要求に失敗しました')
    err.kind = body.kind
    throw err
  }
  if (res.status === 204) return null
  return res.json()
}

const json = (method, body) => ({
  method,
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify(body ?? {}),
})

export const getStatus = () => fetch('/api/status').then(unwrap)
export const listAgents = () => fetch('/api/agents').then(unwrap)
export const listSkills = () => fetch('/api/skills').then(unwrap)
export const reloadDefs = () => fetch('/api/reload', json('POST')).then(unwrap)
export const getConfig = () => fetch('/api/config').then(unwrap)
export const putConfig = (c) => fetch('/api/config', json('PUT', c)).then(unwrap)
export const listModels = () => fetch('/api/models').then(unwrap)

export const listSessions = () => fetch('/api/sessions').then(unwrap)
export const getSession = (id) => fetch(`/api/sessions/${id}`).then(unwrap)
export const createSession = (agent_id) => fetch('/api/sessions', json('POST', { agent_id })).then(unwrap)
export const patchSession = (id, patch) => fetch(`/api/sessions/${id}`, json('PATCH', patch)).then(unwrap)
export const deleteSession = (id) => fetch(`/api/sessions/${id}`, { method: 'DELETE' }).then(unwrap)
export const listMessages = (id) => fetch(`/api/sessions/${id}/messages`).then(unwrap)
export const cancelRun = (id) => fetch(`/api/sessions/${id}/cancel`, json('POST')).then(unwrap)
export const respondApproval = (id, approved) =>
  fetch(`/api/approvals/${id}`, json('POST', { approved })).then(unwrap)

// send は 1 ターンを実行し、サーバーから届くイベントを順に返す。
// 通信は一方向で足りるので SSE を読むだけでよい。
export async function* send(sessionId, text, signal) {
  const res = await fetch(`/api/sessions/${sessionId}/messages`, {
    ...json('POST', { text }),
    signal,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    const err = new Error(body.error || '送信に失敗しました')
    err.kind = body.kind
    throw err
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })

    let idx
    while ((idx = buf.indexOf('\n\n')) >= 0) {
      const chunk = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      for (const line of chunk.split('\n')) {
        if (!line.startsWith('data:')) continue
        try {
          yield JSON.parse(line.slice(5).trim())
        } catch {
          // 壊れた行は捨てる。ここで例外を投げると会話全体が止まる。
        }
      }
    }
  }
}
