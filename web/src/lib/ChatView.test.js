// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import ChatView from './ChatView.svelte'

// 末尾へ追従する仕掛けが要求する。jsdom には無い。
globalThis.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// 無音になる場所は決まっていない。道具を続けて呼ぶ間も、次の生成が始まるまでも、
// 画面には該当する項目が無い。だから発言や道具の行ではなく、届いたかどうかで
// 測って末尾に 1 行だけ出す。
function render(over = {}) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  const app = mount(ChatView, {
    target,
    props: {
      session: { id: 's1', title: '会話', agent_id: 'general' },
      agents: [{ id: 'general', name: 'General', color: 'blue' }],
      items: [],
      busy: true,
      moved: Date.now(),
      stage: { label: '返答を作っています' },
      commands: [],
      notice: null,
      status: { provider_ok: true, default_agent: 'general' },
      railHidden: false,
      usage: null,
      draftBack: null,
      colorOf: () => '',
      onToggleRail: () => {},
      onSend: () => {},
      onCancel: () => {},
      onApprove: () => {},
      onAnswer: () => {},
      onAgentChange: () => {},
      onDismiss: () => {},
      onRewind: () => {},
      ...over,
    },
  })
  flushSync()
  return { target, app }
}

describe('何も届かない時間', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  // ふつうの生成は 1 秒も途切れない。動いている間に出すと、常に出ていることに
  // なって何も伝えない。
  it('動いている間は出さない', () => {
    const { target, app } = render()
    vi.advanceTimersByTime(1000)
    flushSync()
    expect(target.querySelector('.stalled')).toBe(null)
    unmount(app)
  })

  it('止まったら、何を待っているかと時間を出す', () => {
    const { target, app } = render()
    vi.advanceTimersByTime(5000)
    flushSync()
    const line = target.querySelector('.stalled')?.textContent ?? ''
    expect(line).toContain('返答を作っています')
    expect(line).toContain('5s')
    unmount(app)
  })

  // 分母を持てないものに割合は出せない。できた量をそのまま出す。
  it('進んだ量が分かるなら添える', () => {
    const { target, app } = render({
      stage: { label: 'やり取りをまとめています', done: 1200, unit: '字' },
    })
    vi.advanceTimersByTime(5000)
    flushSync()
    const line = target.querySelector('.stalled')?.textContent ?? ''
    expect(line).toContain('やり取りをまとめています')
    expect(line).toContain('1,200字')
    unmount(app)
  })

  // 承認や問いを待っている間は数えない。止まっているのではなく利用者の番で
  // あり、そこで秒数を出しても急かしているだけになる。
  it('利用者の番のときは出さない', () => {
    const { target, app } = render({ stage: null })
    vi.advanceTimersByTime(5000)
    flushSync()
    expect(target.querySelector('.stalled')).toBe(null)
    unmount(app)
  })

  // 項目が 1 つも無い場面 — 道具を続けて呼ぶ間や、最初の一片を待っている間 —
  // でも出る必要がある。ここが出ないと、いちばん長い無音に何も出ない。
  it('画面に項目が無くても出る', () => {
    const { target, app } = render({ items: [] })
    vi.advanceTimersByTime(5000)
    flushSync()
    expect(target.querySelector('.stalled')).not.toBe(null)
    unmount(app)
  })

  it('生成していないときは出さない', () => {
    const { target, app } = render({ busy: false })
    vi.advanceTimersByTime(5000)
    flushSync()
    expect(target.querySelector('.stalled')).toBe(null)
    unmount(app)
  })
})
