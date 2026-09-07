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
  result: '最終回答です',
  children: [
    { id: 'c1', kind: 'agent', status: 'done', agentId: 'researcher', text: '経過の説明' },
    { id: 'c2', kind: 'tool', status: 'done', tool: 'read_file', args: { path: 'a.txt' }, result: '中身' },
    { id: 'c3', kind: 'agent', status: 'done', agentId: 'researcher', text: '最終回答です' },
  ],
  time: '2026-09-04T09:00:00Z',
})

describe('委譲の中の最終回答', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  // 委譲そのものは畳まない。渡した先の仕事は経過ではなく中身である。
  it('委譲は開いたまま', async () => {
    const { target, app } = await render(delegated('done'))
    expect(target.textContent).toContain('read_file')
    expect(target.textContent).toContain('researcher')
    unmount(app)
  })

  it('書いている間は最終回答が見える', async () => {
    const item = delegated('running')
    item.children[2].status = 'streaming'
    const { target, app } = await render(item)
    expect(target.textContent).toContain('最終回答です')
    unmount(app)
  })

  // 書き終われば、同じ文章が受け取った成果として続く。2 度並べない。
  it('書き終わったら最終回答を畳む', async () => {
    const { target, app } = await render(delegated('done'))
    expect(target.textContent).not.toContain('最終回答です')
    expect(target.textContent).toContain('回答')
    // 途中の発言は畳まない。畳むのは最後の 1 つだけ。
    expect(target.textContent).toContain('経過の説明')
    unmount(app)
  })

  it('押せば最終回答を開ける', async () => {
    const { target, app } = await render(delegated('done'))
    target.querySelector('.answer .peek').click()
    await settle()
    expect(target.textContent).toContain('最終回答です')
    unmount(app)
  })

  // 開いたあと畳めないと、2 度並んだ文章を片付ける手立てが無くなる。
  it('開いたあとも畳み直せる', async () => {
    const { target, app } = await render(delegated('done'))
    const toggle = () => target.querySelector('.answer .peek')

    toggle().click()
    await settle()
    expect(target.textContent).toContain('最終回答です')

    toggle().click()
    await settle()
    expect(target.textContent).not.toContain('最終回答です')
    unmount(app)
  })

  // 書いている途中で畳めることにも意味がある。長い回答が場所を占め続ける。
  it('書いている最中でも畳める', async () => {
    const item = delegated('running')
    item.children[2].status = 'streaming'
    const { target, app } = await render(item)
    expect(target.textContent).toContain('最終回答です')

    target.querySelector('.answer .peek').click()
    await settle()
    expect(target.textContent).not.toContain('最終回答です')
    unmount(app)
  })

  // 受け取った成果は渡した先の言葉である。呼び出し元が言ったように読ませない。
  it('受け取った成果に渡した先の名を添える', async () => {
    const item = delegated('done')
    item.result = '別の言い回しの結論'
    const { target, app } = await render(item)
    expect(target.textContent).toContain('researcher の回答')
    expect(target.textContent).toContain('別の言い回しの結論')
    unmount(app)
  })

  it('同じ内容なら本文を繰り返さない', async () => {
    const { target, app } = await render(delegated('done'))
    expect(target.textContent).toContain('上と同じ内容です')
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

// モデルが道具の引数 (write_file の中身など) を書いている間、提供元は何も
// 送ってこない。空の吹き出しだけが残ると、止まったのか書いている途中なのかを
// 見分けられない。
describe('走っている道具の経過', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  // 終わってから所要時間を出すだけだと、走っている間は進んでいるのかどうかが
  // 分からない。
  it('走っている間は数える', async () => {
    const { target, app } = await render({
      id: 't1', kind: 'tool', status: 'running', tool: 'write_file',
      args: { path: 'a.md' }, result: '', startedAt: Date.now() - 3000,
    })
    expect(target.querySelector('.ms.running')?.textContent).toBe('3s')
    unmount(app)
  })

  // 承認や回答を待った分は所要時間に混ぜない。startedAt を落としてあるので、
  // そこを数え始めてはならない。
  it('承認待ちの間は数えない', async () => {
    const { target, app } = await render({
      id: 't2', kind: 'tool', status: 'awaiting', tool: 'run_command',
      args: { command: 'ls' }, result: '', startedAt: 0, approvalId: 'a1',
    })
    expect(target.querySelector('.ms')).toBe(null)
    unmount(app)
  })

  // 数えるのは道具だけ。発言にも付けると、待つ場面すべてに数字が並ぶ。
  it('発言には付けない', async () => {
    const { target, app } = await render({
      id: 'm1', kind: 'agent', status: 'streaming', agentId: 'general',
      thinking: '', text: '', error: '', time: new Date().toISOString(),
    })
    expect(target.querySelector('.tnum.running')).toBe(null)
    expect(target.textContent).not.toContain('生成しています')
    unmount(app)
  })
})
