// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import Item from './Item.svelte'

// Markdown は生成中の描き直しを間引くため、少し待たないと文字が出ない。
async function settle() {
  await new Promise((r) => setTimeout(r, 160))
  flushSync()
}

async function render(item) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  const app = mount(Item, {
    target,
    props: { item, onApprove: () => {}, colorOf: () => '', lead: true, owner: null },
  })
  await settle()
  return { target, app }
}

const delegated = (status) => ({
  id: 'd1',
  kind: 'delegate',
  status,
  agentId: 'researcher',
  task: '調べてきて',
  result: '結論はこうです',
  children: [
    { id: 'c1', kind: 'agent', status: 'done', agentId: 'researcher', text: '経過の説明' },
    { id: 'c2', kind: 'tool', status: 'done', tool: 'read_file', args: { path: 'a.txt' }, result: '中身' },
  ],
  time: '2026-09-04T09:00:00Z',
})

describe('委譲の開閉', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('走っている間は経過を出す', async () => {
    const { target, app } = await render(delegated('running'))
    expect(target.textContent).toContain('経過の説明')
    expect(target.textContent).toContain('researcher')
    unmount(app)
  })

  // 終わったあとの読み手の関心は、経過ではなく親が受け取った成果である。
  it('終わったら経過を畳み、受け取った成果は残す', async () => {
    const { target, app } = await render(delegated('done'))
    expect(target.textContent).not.toContain('経過の説明')
    expect(target.textContent).toContain('結論はこうです')
    // 誰へ渡したかは畳んでいても読める。
    expect(target.textContent).toContain('researcher')
    unmount(app)
  })

  it('押せば開き直せる', async () => {
    const { target, app } = await render(delegated('done'))
    target.querySelector('.dg .head').click()
    await settle()
    expect(target.textContent).toContain('経過の説明')
    unmount(app)
  })

  // 経過を開いているときは、子の最後の発言と成果が同じ文章になる。
  it('開いているときは同じ文章を 2 度出さない', async () => {
    const item = delegated('done')
    item.children[0].text = item.result
    const { target, app } = await render(item)
    target.querySelector('.dg .head').click()
    await settle()
    const count = target.textContent.split(item.result).length - 1
    expect(count).toBe(1)
    unmount(app)
  })
})

describe('推論の開閉', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  const thinking = (status, text) => ({
    id: 'm1', kind: 'agent', status, agentId: 'general',
    thinking: '考えている途中', text, error: '', time: '2026-09-04T09:00:00Z',
  })

  it('考えている間は過程を出す', async () => {
    const { target, app } = await render(thinking('streaming', ''))
    expect(target.textContent).toContain('考えている途中')
    expect(target.textContent).toContain('考えています')
    unmount(app)
  })

  // 本文が出はじめたら役目が終わる。結論が過程に押し下げられないようにする。
  it('本文が出たら畳む', async () => {
    const { target, app } = await render(thinking('streaming', '答えです'))
    expect(target.textContent).not.toContain('考えている途中')
    expect(target.textContent).toContain('答えです')
    unmount(app)
  })

  it('終わった発言でも畳んだまま', async () => {
    const { target, app } = await render(thinking('done', '答えです'))
    expect(target.textContent).not.toContain('考えている途中')
    unmount(app)
  })
})
