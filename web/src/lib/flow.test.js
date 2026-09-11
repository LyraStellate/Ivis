// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'

// jsdom は ResizeObserver を持たない。図は測らないが、同じ木に載る部品が使う。
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const { default: FlowMap } = await import('./FlowMap.svelte')

/** 升。key は "列:ID"。 */
const node = (col, id, o = {}) => ({
  key: col + ':' + id,
  id,
  name: id || 'あなた',
  tier: id ? 2 : 0,
  user: id === '',
  round: 1,
  turn: col,
  col,
  waiting: 0,
  ...o,
})

const arrow = (id, from, to, o = {}) => ({
  id,
  from: from.split(':')[1],
  to: to.split(':')[1],
  from_node: from,
  to_node: to,
  relation: '指示',
  body: '本文',
  open: false,
  seq: 1,
  ...o,
})

const col = (c, turn, round = 1) => ({ col: c, round, turn })

function render(flow) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  const app = mount(FlowMap, { target, props: { flow, colorOf: () => '' } })
  flushSync()
  return { target, app }
}

// 利用者 → lead → (hand, scout) → lead の 1 ラウンド。
function round1() {
  return {
    cols: [col(0, 0), col(1, 1), col(2, 2), col(3, 3)],
    nodes: [
      node(0, '', { turn: 0 }),
      node(1, 'lead', { tier: 1 }),
      node(2, 'hand'),
      node(2, 'scout'),
      node(3, 'lead', { tier: 1, waiting: 0 }),
    ],
    arrows: [
      arrow('a1', '0:', '1:lead', { relation: '' }),
      arrow('a2', '1:lead', '2:hand'),
      arrow('a3', '1:lead', '2:scout'),
      arrow('a4', '2:hand', '3:lead', { relation: '報告' }),
      arrow('a5', '2:scout', '3:lead', { relation: '報告' }),
    ],
  }
}

describe('流れ図', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('行はエージェントで固定され、利用者が一番上に来る', () => {
    const { target, app } = render(round1())
    const names = [...target.querySelectorAll('.who .nm')].map((e) => e.textContent.trim())
    expect(names).toEqual(['あなた', 'lead', 'hand', 'scout'])
    unmount(app)
  })

  it('同じエージェントは、列が違っても同じ高さに出る', () => {
    const { target, app } = render(round1())
    const cy = [...target.querySelectorAll('.node circle')].map((c) => +c.getAttribute('cy'))
    // lead は 1 列めと 3 列めの 2 か所に出る。高さは同じでなければならない。
    const leads = cy.filter((v, i, all) => all.indexOf(v) !== i)
    expect(leads.length).toBeGreaterThan(0)
    unmount(app)
  })

  it('升はターンごとに増える。同じ人でもターンが違えば別の升', () => {
    const { target, app } = render(round1())
    expect(target.querySelectorAll('.node').length).toBe(5)
    unmount(app)
  })

  it('矢印は本数どおりに引かれ、束ねない', () => {
    const { target, app } = render(round1())
    expect(target.querySelectorAll('path.edge').length).toBe(5)
    unmount(app)
  })

  it('返事待ちの矢印だけが濃く描かれる', () => {
    const f = round1()
    f.arrows[1].open = true
    const { target, app } = render(f)
    expect(target.querySelectorAll('path.edge.open').length).toBe(1)
    unmount(app)
  })

  it('列の見出しはターン番号で、ラウンドの切れ目に仕切りが出る', () => {
    const f = round1()
    f.cols.push(col(4, 1, 2))
    f.nodes.push(node(4, 'lead', { tier: 1, round: 2, turn: 1 }))
    const { target, app } = render(f)
    const heads = [...target.querySelectorAll('.head')].map((e) => e.textContent.trim())
    expect(heads).toEqual(['T0', 'T1', 'T2', 'T3', 'T1'])
    // 仕切りは、列の意味が変わるところに 1 本だけ。
    expect(target.querySelectorAll('line.seam').length).toBe(1)
    expect(target.querySelectorAll('.head.seam').length).toBe(1)
    unmount(app)
  })

  it('返していない矢印を持つ升には、その数が出る', () => {
    const f = round1()
    f.nodes[2].waiting = 2
    const { target, app } = render(f)
    const owe = [...target.querySelectorAll('.oweN')].map((e) => e.textContent.trim())
    expect(owe).toEqual(['2'])
    unmount(app)
  })

  it('升を押すと、そのターンのやり取りが出る', () => {
    const f = round1()
    f.arrows[1].body = '実装して'
    f.arrows[1].open = true
    f.arrows[3].body = 'できました'
    const { target, app } = render(f)
    expect(target.querySelector('.detail')).toBe(null)

    // hand の升 (2 列め) を押す。受けたものと出したものが分かれて出る。
    const hand = target.querySelectorAll('.node')[2]
    hand.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0 }))
    flushSync()

    const detail = target.querySelector('.detail')
    expect(detail.textContent).toContain('実装して')
    expect(detail.textContent).toContain('できました')
    expect(detail.textContent).toContain('返していない')
    unmount(app)
  })

  it('誰も動いていなければ、その旨を出す', () => {
    const { target, app } = render({ cols: [], nodes: [], arrows: [] })
    expect(target.querySelector('.empty')).not.toBe(null)
    expect(target.querySelector('svg')).toBe(null)
    unmount(app)
  })

  it('掴むと横へ動き、「最新へ」で右端に戻る', () => {
    const { target, app } = render(round1())
    const shift = () => target.querySelector('.scroll').style.transform
    // 開いた直後は右端に貼り付いている。見たいのはいまのターンだからである。
    const atRight = shift()

    // 右へ引くと、前のターンが見えてくる。
    target
      .querySelector('.grid')
      .dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, clientX: 100 }))
    window.dispatchEvent(new PointerEvent('pointermove', { clientX: 180 }))
    window.dispatchEvent(new PointerEvent('pointerup', {}))
    flushSync()
    expect(shift()).not.toBe(atRight)

    app.latest()
    flushSync()
    expect(shift()).toBe(atRight)
    unmount(app)
  })
})
