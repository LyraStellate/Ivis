// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { stickToBottom, isAtBottom } from './stick.js'

// jsdom は配置を計算しないので、寸法は自分で持たせる。
function makeScroller(view = 100) {
  const node = document.createElement('div')
  const content = document.createElement('div')
  node.appendChild(content)
  document.body.appendChild(node)

  let height = view
  let top = 0
  Object.defineProperty(node, 'clientHeight', { get: () => view })
  Object.defineProperty(node, 'scrollHeight', { get: () => height })
  Object.defineProperty(node, 'scrollTop', {
    get: () => top,
    set: (v) => {
      top = Math.max(0, Math.min(v, height - view))
      node.dispatchEvent(new Event('scroll'))
    },
  })
  return {
    node,
    // 中身が伸びる。実際は ResizeObserver が拾うので、その分は手で呼ぶ。
    //
    // ブラウザは、表示中の位置より上に内容が入ると見え方を保とうとして
    // scrollTop を自分で調整し、その拍子に scroll を発します。末尾からは
    // 離れているのに利用者は何もしていない、という状況がここで起きる。
    grow(by, { anchor = false } = {}) {
      height += by
      if (anchor) node.dispatchEvent(new Event('scroll'))
    },
    get top() {
      return top
    },
    scrollTo(v) {
      node.scrollTop = v
    },
  }
}

describe('末尾への追従', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    globalThis.ResizeObserver = class {
      constructor(fn) {
        this.fn = fn
        observers.push(fn)
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  })
  let observers = []
  beforeEach(() => {
    observers = []
  })

  const resized = () => observers.forEach((fn) => fn())

  // 中身が伸びただけで追従が外れると、生成が速いほど画面が置いていかれる。
  // 委譲の中で行が次々に増えるときに実際に起きていた。
  it('中身が伸びても追従を続ける', () => {
    const s = makeScroller()
    let pinned = null
    stickToBottom(s.node, { onPinned: (p) => (pinned = p) })

    // 1 度に増える量は、末尾とみなす余白 (64px) より大きく取る。委譲の中で
    // 行がまとめて増えるときは、この幅で伸びる。
    for (let i = 0; i < 20; i++) {
      s.grow(200, { anchor: true })
      resized()
    }
    expect(pinned).not.toBe(false)
    expect(isAtBottom(s.node)).toBe(true)
  })

  it('遡ったら追従をやめる', () => {
    const s = makeScroller()
    let pinned = null
    stickToBottom(s.node, { onPinned: (p) => (pinned = p) })

    s.grow(1000)
    resized()
    s.scrollTo(100)
    expect(pinned).toBe(false)
  })

  it('末尾へ戻れば追従を再開する', () => {
    const s = makeScroller()
    let pinned = null
    stickToBottom(s.node, { onPinned: (p) => (pinned = p) })

    s.grow(1000)
    resized()
    s.scrollTo(100)
    expect(pinned).toBe(false)

    s.scrollTo(1100)
    expect(pinned).toBe(true)

    // 追従が戻っていれば、以降の伸びにも付いていく。
    s.grow(500)
    resized()
    expect(isAtBottom(s.node)).toBe(true)
  })

  it('遡っている間は伸びても動かさない', () => {
    const s = makeScroller()
    stickToBottom(s.node, {})
    s.grow(1000)
    resized()
    s.scrollTo(100)

    s.grow(500)
    resized()
    expect(s.top).toBe(100)
  })
})
