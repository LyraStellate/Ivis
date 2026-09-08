// API 呼び出しをまとめる。失敗は kind を保った Error として投げ、
// 画面側が「Ollama を起動する」「モデルを pull する」を出し分けられるようにする。

async function unwrap(res) {
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    const err = new Error(body.error || '要求に失敗しました')
    err.kind = body.kind
    // どの欄を直せばよいかを画面へ渡す。まとめて「保存できません」に
    // すると、利用者は直せる誤りを直せない。
    err.field = body.field
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
export const createAgent = (a) => fetch('/api/agents', json('POST', a)).then(unwrap)
export const updateAgent = (id, a) => fetch(`/api/agents/${id}`, json('PUT', a)).then(unwrap)
export const deleteAgent = (id) => fetch(`/api/agents/${id}`, { method: 'DELETE' }).then(unwrap)
export const listTools = () => fetch('/api/tools').then(unwrap)
export const listSkills = () => fetch('/api/skills').then(unwrap)
// 使えるコマンドの一覧。画面に書き写すと、増やしたときに片方だけ古くなる。
export const listCommands = () => fetch('/api/commands').then(unwrap)
export const reloadDefs = () => fetch('/api/reload', json('POST')).then(unwrap)
export const getConfig = () => fetch('/api/config').then(unwrap)
export const putConfig = (c) => fetch('/api/config', json('PUT', c)).then(unwrap)
export const listModels = () => fetch('/api/models').then(unwrap)

export const listSessions = () => fetch('/api/sessions').then(unwrap)
export const getSession = (id) => fetch(`/api/sessions/${id}`).then(unwrap)
export const createSession = (agent_id, kind) =>
  fetch('/api/sessions', json('POST', { agent_id, kind })).then(unwrap)
export const patchSession = (id, patch) => fetch(`/api/sessions/${id}`, json('PATCH', patch)).then(unwrap)
export const deleteSession = (id) => fetch(`/api/sessions/${id}`, { method: 'DELETE' }).then(unwrap)
export const listMessages = (id) => fetch(`/api/sessions/${id}/messages`).then(unwrap)
export const rewindSession = (id, message_id) =>
  fetch(`/api/sessions/${id}/rewind`, json('POST', { message_id })).then(unwrap)
export const cancelRun = (id) => fetch(`/api/sessions/${id}/cancel`, json('POST')).then(unwrap)

// チームの名簿 (#731906)。参加中のメンバーと、参加していない共通エージェントを
// 1 度で返す。2 回に分けると、片方だけ古い一覧を描く瞬間が生まれる。
export const getRoster = (id) => fetch(`/api/sessions/${id}/agents`).then(unwrap)
export const joinMember = (id, agent_id, join) =>
  fetch(`/api/sessions/${id}/members`, json('POST', { agent_id, join })).then(unwrap)
export const createSessionAgent = (id, a) =>
  fetch(`/api/sessions/${id}/agents`, json('POST', a)).then(unwrap)
export const updateSessionAgent = (id, aid, a) =>
  fetch(`/api/sessions/${id}/agents/${aid}`, json('PUT', a)).then(unwrap)
export const deleteSessionAgent = (id, aid) =>
  fetch(`/api/sessions/${id}/agents/${aid}`, { method: 'DELETE' }).then(unwrap)
export const copyAgent = (id, agent_id, newId) =>
  fetch(`/api/sessions/${id}/agents/copy`, json('POST', { agent_id, id: newId })).then(unwrap)

// チケット (#189542)。チームの唯一の共有状態なので、画面からも直せる。
export const listTickets = (id, q = {}) => {
  const p = new URLSearchParams()
  if (q.assignee) p.set('assignee', q.assignee)
  if (q.status) p.set('status', q.status)
  if (q.closed) p.set('closed', '1')
  const qs = p.toString()
  return fetch(`/api/sessions/${id}/tickets` + (qs ? '?' + qs : '')).then(unwrap)
}
export const getTicket = (id, n) => fetch(`/api/sessions/${id}/tickets/${n}`).then(unwrap)
export const createTicket = (id, t) =>
  fetch(`/api/sessions/${id}/tickets`, json('POST', t)).then(unwrap)
export const patchTicket = (id, n, patch) =>
  fetch(`/api/sessions/${id}/tickets/${n}`, json('PATCH', patch)).then(unwrap)
export const addTicketNote = (id, n, body) =>
  fetch(`/api/sessions/${id}/tickets/${n}/notes`, json('POST', { body })).then(unwrap)
export const deleteTicket = (id, n) =>
  fetch(`/api/sessions/${id}/tickets/${n}`, { method: 'DELETE' }).then(unwrap)
export const respondApproval = (id, approved) =>
  fetch(`/api/approvals/${id}`, json('POST', { approved })).then(unwrap)
export const respondQuestion = (id, answer) =>
  fetch(`/api/questions/${id}`, json('POST', { answer })).then(unwrap)

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
  yield* events(res)
}

// attach は既に走っている実行へ繋ぎ直す。走っていなければ何も返さない。
//
// 実行はブラウザの都合と切り離して走っているので、更新しても止まらない。
// 画面はここから続きを受け取る。それまでの経過も流れてくるので、開き直した
// 直後の並びは、離れる前と同じところまで戻る。
export async function* attach(sessionId, signal) {
  const res = await fetch(`/api/sessions/${sessionId}/stream`, { signal })
  if (res.status === 204) return
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    const err = new Error(body.error || '繋ぎ直せませんでした')
    err.kind = body.kind
    throw err
  }
  yield* events(res)
}

async function* events(res) {
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
