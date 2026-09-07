// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, unmount, flushSync, tick } from 'svelte'

// jsdom には ResizeObserver が無い。追従の仕掛けが使うだけなので空で埋める。
globalThis.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const sessions = [
  { id: 's1', title: '会話 1', agent_id: 'general', workspace: '/w/s1',
    context_tokens: 0, context_limit: 0,
    created_at: '2026-09-04T09:00:00Z', updated_at: '2026-09-04T09:00:00Z' },
  { id: 's2', title: '会話 2', agent_id: 'general', workspace: '/w/s2',
    context_tokens: 0, context_limit: 0,
    created_at: '2026-09-04T08:00:00Z', updated_at: '2026-09-04T08:00:00Z' },
]

const messages = {
  s1: [{ id: 'm1', session_id: 's1', parent_id: '', seq: 1, role: 'user',
         content: 'いちの発言', agent_id: 'general', created_at: '2026-09-04T09:00:00Z' }],
  s2: [{ id: 'm2', session_id: 's2', parent_id: '', seq: 1, role: 'user',
         content: 'にの発言', agent_id: 'general', created_at: '2026-09-04T08:00:00Z' }],
}

vi.mock('./lib/api.js', () => ({
  getStatus: vi.fn(async () => ({
    provider: 'ollama', provider_ok: true, default_agent: 'general',
    agent_errors: [], skill_errors: [], skill_conflicts: [], colors: [],
    tools: [], search_backends: [], shell: 'sh',
  })),
  listAgents: vi.fn(async () => [{ id: 'general', name: 'General', model: 'm', tier: 0 }]),
  listSessions: vi.fn(async () => sessions),
  getSession: vi.fn(async (id) => sessions.find((s) => s.id === id)),
  listMessages: vi.fn(async (id) => messages[id] ?? []),
  createSession: vi.fn(async () => ({
    id: 's3', title: '新しい会話', agent_id: 'general', workspace: '/w/s3',
    context_tokens: 0, context_limit: 0,
    created_at: '2026-09-04T10:00:00Z', updated_at: '2026-09-04T10:00:00Z',
  })),
  deleteSession: vi.fn(async () => null),
  patchSession: vi.fn(async () => null),
  rewindSession: vi.fn(async () => null),
  cancelRun: vi.fn(async () => null),
  respondApproval: vi.fn(async () => null),
  reloadDefs: vi.fn(async () => null),
  listSkills: vi.fn(async () => []),
  listCommands: vi.fn(async () => [
    { name: 'compact', desc: 'これまでのやり取りをまとめる' },
    { name: 'help', desc: '使えるコマンドを出す' },
  ]),
  listTools: vi.fn(async () => []),
  listModels: vi.fn(async () => []),
  getConfig: vi.fn(async () => ({})),
  putConfig: vi.fn(async () => ({})),
  createAgent: vi.fn(async () => null),
  updateAgent: vi.fn(async () => null),
  deleteAgent: vi.fn(async () => null),
  // 生成は、こちらが止めるまで終わらない流れとして返す。
  send: vi.fn(async function* () {
    yield { type: 'message_start', message_id: 'live', agent_id: 'general', depth: 0 }
    yield { type: 'delta', message_id: 'live', text: '生成中', depth: 0 }
    await new Promise((r) => setTimeout(r, 5000))
  }),
}))

const { default: App } = await import('./App.svelte')

async function settle() {
  for (let i = 0; i < 12; i++) {
    await tick()
    await Promise.resolve()
  }
  flushSync()
}

describe('会話の切り替え', () => {
  let target
  beforeEach(() => {
    document.body.innerHTML = ''
    target = document.createElement('div')
    document.body.appendChild(target)
  })

  it('選んだ会話の履歴が出る', async () => {
    const app = mount(App, { target })
    await settle()

    const rows = [...target.querySelectorAll('button.open')]
    expect(rows.length).toBe(2)

    rows[0].click()
    await settle()
    expect(target.textContent).toContain('いちの発言')

    // 切り替えたら、前の会話の発言は残らない。
    rows[1].click()
    await settle()
    expect(target.textContent).toContain('にの発言')
    expect(target.textContent).not.toContain('いちの発言')

    unmount(app)
  })
})

// 生成が走っている間もほかの会話を開ける。ツールが増えて 1 ターンが数分
// かかるようになった以上、待っている間に何も見られないのは通らない。
describe('生成中の操作', () => {
  let target
  beforeEach(() => {
    document.body.innerHTML = ''
    target = document.createElement('div')
    document.body.appendChild(target)
  })

  it('生成中でも会話を切り替えられる', async () => {
    const app = mount(App, { target })
    await settle()

    target.querySelectorAll('button.open')[0].click()
    await settle()

    const area = target.querySelector('textarea')
    area.value = 'やってみて'
    area.dispatchEvent(new Event('input', { bubbles: true }))
    await settle()
    target.querySelector('form').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await settle()
    expect(target.textContent).toContain('生成中')

    // ここで別の会話を開く。止まっていると、返事を待つ間ずっと動けない。
    target.querySelectorAll('button.open')[1].click()
    await settle()
    expect(target.textContent).toContain('にの発言')
    expect(target.textContent).not.toContain('生成中')

    unmount(app)
  })

  it('生成中でも新しい会話へ移れる', async () => {
    const app = mount(App, { target })
    await settle()
    target.querySelectorAll('button.open')[0].click()
    await settle()

    const area = target.querySelector('textarea')
    area.value = 'やってみて'
    area.dispatchEvent(new Event('input', { bubbles: true }))
    await settle()
    target.querySelector('form').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await settle()

    const newBtn = [...target.querySelectorAll('button')].find((b) => b.textContent.includes('新しい会話'))
    newBtn.click()
    await settle()

    // 作っただけで開けないと、会話だけが増えていく。
    expect(target.querySelectorAll('button.open').length).toBe(3)
    expect(target.textContent).not.toContain('いちの発言')
    expect(target.textContent).not.toContain('生成中')

    unmount(app)
  })
})
