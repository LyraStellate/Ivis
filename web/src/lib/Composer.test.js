// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import Composer from './Composer.svelte'

const commands = [
  { name: 'stop', desc: '走っている生成を止める' },
  { name: 'compact', desc: 'これまでのやり取りをまとめる' },
  { name: 'clear', desc: '履歴をすべて消す' },
  { name: 'help', desc: '使えるコマンドを出す' },
]

function render(over = {}) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  const sent = []
  const app = mount(Composer, {
    target,
    props: {
      commands,
      agents: [],
      agentId: '',
      usage: null,
      draftBack: null,
      onSend: (t) => sent.push(t),
      onCancel: () => {},
      onAgentChange: () => {},
      ...over,
    },
  })
  flushSync()
  return { target, app, sent, area: target.querySelector('textarea') }
}

// 打った文字を入力欄へ入れる。bind:value は input を見る。
function type(area, text) {
  area.value = text
  area.dispatchEvent(new Event('input', { bubbles: true }))
  flushSync()
}

function press(area, key) {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
  area.dispatchEvent(e)
  flushSync()
  return e
}

describe('コマンドの候補', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('ふつうの文では出さない', () => {
    const { target, area, app } = render()
    type(area, 'こんにちは')
    expect(target.querySelector('.cmds')).toBe(null)
    unmount(app)
  })

  it('スラッシュだけで全部出す', () => {
    const { target, area, app } = render()
    type(area, '/')
    const names = [...target.querySelectorAll('.cname')].map((e) => e.textContent)
    expect(names).toEqual(['/stop', '/compact', '/clear', '/help'])
    unmount(app)
  })

  it('打った分で絞る', () => {
    const { target, area, app } = render()
    type(area, '/c')
    const names = [...target.querySelectorAll('.cname')].map((e) => e.textContent)
    expect(names).toEqual(['/compact', '/clear'])
    unmount(app)
  })

  // 引数を打っている間まで出し続けると、書いている文字の上に一覧が居座る。
  it('引数を打ち始めたら引っ込める', () => {
    const { target, area, app } = render()
    type(area, '/compact ファイル名だけ')
    expect(target.querySelector('.cmds')).toBe(null)
    unmount(app)
  })

  // 引数を取るものがあるので、選んでも送信まではしない。
  it('選ぶと名前まで入り、送信はしない', () => {
    const { target, area, sent, app } = render()
    type(area, '/co')
    target.querySelector('.cmds button').dispatchEvent(
      new MouseEvent('mousedown', { bubbles: true, cancelable: true }),
    )
    flushSync()
    expect(area.value).toBe('/compact ')
    expect(sent).toEqual([])
    unmount(app)
  })

  it('上下で選び、Enter で決める', () => {
    const { target, area, sent, app } = render()
    type(area, '/c')
    press(area, 'ArrowDown')
    press(area, 'Enter')
    expect(area.value).toBe('/clear ')
    expect(sent).toEqual([])
    unmount(app)
  })

  it('Esc で閉じる', () => {
    const { target, area, app } = render()
    type(area, '/c')
    press(area, 'Escape')
    expect(target.querySelector('.cmds')).toBe(null)
    unmount(app)
  })

  // 候補が出ていなければ、Enter はいつもどおり送信である。
  it('候補が無ければ Enter で送る', () => {
    const { area, sent, app } = render()
    type(area, 'こんにちは')
    press(area, 'Enter')
    expect(sent).toEqual(['こんにちは'])
    unmount(app)
  })
})
