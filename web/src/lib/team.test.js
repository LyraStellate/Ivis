// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import { Transcript } from './conversation.js'
import { leads, speaker } from './group.js'
import Composer from './Composer.svelte'
import Item from './Item.svelte'
import ChatView from './ChatView.svelte'
import TeamPanel from './TeamPanel.svelte'

// 末尾へ追従する仕掛けが要求する。jsdom には無い。
globalThis.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const msg = (o) => ({ parent_id: '', tool_calls: null, tool_name: '', error: '', ...o })

describe('チームのメッセージ', () => {
  it('確定履歴から、宛先と内訳を持つ項目になる', () => {
    const t = new Transcript([])
    t.loadHistory([
      msg({ id: 'm1', role: 'user', content: '調べて', to_agent_id: 'lead' }),
      msg({
        id: 'm2',
        role: 'team',
        agent_id: 'lead',
        to_agent_id: 'hand',
        content: '中を見てほしい',
        why: '利用者に頼まれた',
        did: '範囲を決めた',
      }),
      msg({
        id: 'm3',
        role: 'team',
        agent_id: 'hand',
        to_agent_id: 'lead',
        content: '終わりました',
        decision: '受諾',
      }),
    ])

    expect(t.items.map((i) => i.kind)).toEqual(['user', 'team', 'team'])
    expect(t.items[1].to).toBe('hand')
    expect(t.items[1].why).toBe('利用者に頼まれた')
    expect(t.items[1].did).toBe('範囲を決めた')
    expect(t.items[2].decision).toBe('受諾')
  })

  it('ストリームの team_message を末尾へ置く', () => {
    const t = new Transcript([])
    t.apply({
      type: 'team_message',
      message_id: 'x1',
      agent_id: 'lead',
      to: 'hand',
      relation: '指示',
      text: 'やって',
      why: 'なぜ',
      did: 'やったこと',
    })
    expect(t.items).toHaveLength(1)
    expect(t.items[0]).toMatchObject({ kind: 'team', agentId: 'lead', to: 'hand', relation: '指示' })
  })

  // 手番の入れ替わりで行を足すと、送ったメッセージと二重になる。誰が動いて
  // いるかは末尾の待っている行が出す。
  it('手番の始まりと終わりは項目を増やさない', () => {
    const t = new Transcript([])
    t.apply({ type: 'turn_start', agent_id: 'hand', queued: 2 })
    t.apply({ type: 'turn_end', agent_id: 'hand', queued: 1 })
    expect(t.items).toHaveLength(0)
  })

  // 同じ人が続けて別の相手へ送ることがある。そこで名前を出し直さないと、
  // 誰宛ての話がどこで切り替わったのか読めない。
  it('送り手と宛先の組ごとに見出しを出す', () => {
    const items = [
      { kind: 'team', agentId: 'lead', to: 'hand' },
      { kind: 'team', agentId: 'lead', to: 'hand' },
      { kind: 'team', agentId: 'lead', to: 'scout' },
    ]
    expect(speaker(items[0])).toBe('team:lead>hand')
    expect(leads(items)).toEqual([true, false, true])
  })
})

describe('チームのメッセージの描画', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  // Markdown は生成中の描き直しを間引くため、少し待たないと文字が出ない。
  async function render(item) {
    const target = document.createElement('div')
    document.body.appendChild(target)
    const app = mount(Item, {
      target,
      props: {
        item,
        lead: true,
        owner: { isUser: false, agentId: item.agentId },
        onApprove: () => {},
        colorOf: () => '',
      },
    })
    await new Promise((r) => setTimeout(r, 160))
    flushSync()
    return { target, app }
  }

  it('宛先と種別を見出しに出し、本文を主に置く', async () => {
    const { target, app } = await render({
      id: 't1',
      kind: 'team',
      status: 'done',
      agentId: 'lead',
      to: 'hand',
      relation: '指示',
      text: '中を見てほしい',
      why: '利用者に頼まれた',
      did: '範囲を決めた',
    })
    const head = target.querySelector('.who').textContent
    expect(head).toContain('lead')
    expect(head).toContain('hand')
    expect(head).toContain('指示')
    expect(target.textContent).toContain('中を見てほしい')
    unmount(app)
  })

  // 経緯とやったことは受け手のための情報で、読んでいる利用者は前の手番を
  // 既に見ている。畳んでおくが、1 行目は見える。
  it('経緯とやったことは畳むが、1 行は見せる', async () => {
    const { target, app } = await render({
      id: 't2',
      kind: 'team',
      status: 'done',
      agentId: 'hand',
      to: 'lead',
      text: '終わりました',
      why: '指示を受けた',
      did: '3 つのファイルを直した',
    })
    expect(target.querySelector('.peekline').textContent).toContain('3 つのファイルを直した')
    expect(target.textContent).not.toContain('指示を受けた')

    target.querySelector('.ctx .peek').click()
    flushSync()
    expect(target.textContent).toContain('指示を受けた')
    unmount(app)
  })

  it('却下は目を引く形で出す', async () => {
    const { target, app } = await render({
      id: 't3',
      kind: 'team',
      status: 'done',
      agentId: 'scout',
      to: 'hand',
      relation: '依頼',
      decision: '却下',
      text: '手が空いていません',
    })
    const chip = target.querySelector('.decide')
    expect(chip.textContent).toBe('却下')
    expect(chip.classList.contains('no')).toBe(true)
    unmount(app)
  })
})

describe('宛先の候補', () => {
  const members = [
    { id: 'lead', name: 'Lead', tier: 1, scope: 'common' },
    { id: 'hand', name: 'Hand', tier: 2, scope: 'team' },
  ]

  function render(over = {}) {
    const target = document.createElement('div')
    document.body.appendChild(target)
    const sent = []
    const app = mount(Composer, {
      target,
      props: {
        commands: [{ name: 'compact', desc: 'まとめる' }],
        members,
        showAgents: false,
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

  function type(area, text) {
    area.value = text
    area.dispatchEvent(new Event('input', { bubbles: true }))
    flushSync()
  }

  beforeEach(() => {
    document.body.innerHTML = ''
  })

  // 判断を委ねる "*" は無い。誰に頼むかを決めるのは利用者である (#640275)。
  it('@ で名簿を出す。委ねる先は候補に無い', () => {
    const { target, area, app } = render()
    type(area, '@')
    const names = [...target.querySelectorAll('.cname')].map((e) => e.textContent)
    expect(names).toEqual(['@lead', '@hand'])
    unmount(app)
  })

  it('打った分で絞る', () => {
    const { target, area, app } = render()
    type(area, '@h')
    const names = [...target.querySelectorAll('.cname')].map((e) => e.textContent)
    expect(names).toEqual(['@hand'])
    unmount(app)
  })

  // サーバーは本文の先頭しか宛先として読まない。効かない場所で候補を出せば、
  // 選んだのに届かないことが起きる。
  it('本文を打ち始めたら引っ込める', () => {
    const { target, area, app } = render()
    type(area, '@hand これを頼む')
    expect(target.querySelector('.cmds')).toBe(null)
    unmount(app)
  })

  it('選ぶと宛先まで入り、送信はしない', () => {
    const { target, area, sent, app } = render()
    type(area, '@ha')
    target.querySelector('.cmds button').dispatchEvent(
      new MouseEvent('mousedown', { bubbles: true, cancelable: true }),
    )
    flushSync()
    expect(area.value).toBe('@hand ')
    expect(sent).toEqual([])
    unmount(app)
  })

  // 直列の会話には名簿が無い。@ は宛先ではなく、ただの文字である。
  it('名簿が無ければ @ で何も出さない', () => {
    const { target, area, app } = render({ members: [] })
    type(area, '@')
    expect(target.querySelector('.cmds')).toBe(null)
    unmount(app)
  })

  it('コマンドの候補は今までどおり出る', () => {
    const { target, area, app } = render()
    type(area, '/c')
    const names = [...target.querySelectorAll('.cname')].map((e) => e.textContent)
    expect(names).toEqual(['/compact'])
    unmount(app)
  })

  // 宛先の無い発言は届かない。送ってから断られると、画面が先に置いた
  // 吹き出しだけが残って再読込で消えるので、押させない。
  it('宛先を書かないうちは送れない', () => {
    const { target, area, sent, app } = render()
    type(area, '調べて')
    expect(target.querySelector('.hint').textContent).toContain('@ で宛先')
    expect(target.querySelector('.go').disabled).toBe(true)
    area.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }),
    )
    flushSync()
    expect(sent).toEqual([])
    unmount(app)
  })

  it('@ を打てば送れる', () => {
    const { target, area, sent, app } = render()
    type(area, '@hand 調べて')
    expect(target.querySelector('.go').disabled).toBe(false)
    area.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }),
    )
    flushSync()
    expect(sent).toEqual(['@hand 調べて'])
    unmount(app)
  })

  // 1 人しか居なければ迷う余地が無い。そこで宛先を強いるのは、答えの
  // 決まっている問いを毎回出すのと同じである。
  it('名簿が 1 人なら宛先を書かなくても送れる', () => {
    const { target, area, sent, app } = render({ members: [members[1]] })
    type(area, '調べて')
    expect(target.querySelector('.hint').textContent).toContain('hand')
    expect(target.querySelector('.go').disabled).toBe(false)
    area.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }),
    )
    flushSync()
    expect(sent).toEqual(['調べて'])
    unmount(app)
  })

  // コマンドは宛先を持たない。ここで止めると /compact が打てなくなる。
  it('コマンドは宛先が無くても送れる', () => {
    const { target, area, app } = render()
    type(area, '/compact')
    expect(target.querySelector('.go').disabled).toBe(false)
    unmount(app)
  })
})

describe('チームでの入力欄の相手', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  function render(over = {}) {
    const target = document.createElement('div')
    document.body.appendChild(target)
    const app = mount(ChatView, {
      target,
      props: {
        session: { id: 's1', title: 'チーム', agent_id: 'boss', kind: 'team' },
        agents: [{ id: 'general', name: 'General' }],
        members: [
          { id: 'boss', name: 'Boss', tier: 0, scope: 'team' },
          { id: 'hand', name: 'Hand', tier: 2, scope: 'team' },
        ],
        roster: { members: [], available: [], errors: [] },
        isTeam: true,
        items: [],
        busy: false,
        moved: 0,
        stage: null,
        commands: [],
        notice: null,
        status: { provider_ok: true, default_agent: 'general' },
        railHidden: false,
        panelHidden: false,
        usage: null,
        draftBack: null,
        colorOf: () => '',
        onToggleRail: () => {},
        onTogglePanel: () => {},
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

  // チームで答え手を選ぶ欄は無い。宛先はメンションで決まるので、選ばせる
  // 欄があること自体が誤りになる (#640275)。
  it('相手を選ぶ欄を出さない', () => {
    const { target, app } = render()
    expect(target.querySelector('.who')).toBe(null)
    unmount(app)
  })

  // session.agent_id は会話を作ったときの記録で、実行では読まない。見ると
  // 名簿に居ないことを理由に、いつでも送信が止まる。
  it('agent_id が名簿に無くても止めない', () => {
    const { target, app } = render({
      session: { id: 's1', title: 'チーム', agent_id: 'gone', kind: 'team' },
    })
    expect(target.querySelector('.banner')).toBe(null)
    expect(target.querySelector('textarea').disabled).toBe(false)
    unmount(app)
  })

  // 名簿が届く前に「居ない」と言わない。開くたびに帯が明滅する。
  it('名簿が届く前は騒がない', () => {
    const { target, app } = render({ members: [], roster: null })
    expect(target.querySelector('.banner')).toBe(null)
    unmount(app)
  })

  // 空になったら止める。宛先が 1 つも無いので、送っても届く先が無い。
  it('名簿が空なら帯を出して止める', () => {
    const { target, app } = render({ members: [] })
    expect(target.querySelector('.banner').textContent).toContain('メンバーが 1 人も居ません')
    expect(target.querySelector('textarea').disabled).toBe(true)
    unmount(app)
  })
})

// 右パネルの名簿は 4 つの欄に分かれる。有効になっているものを 1 つの欄に
// まとめるのは、「@ を打つと誰が出るのか」に画面の 1 か所が答えられるように
// するためである (#731906)。
describe('右パネルの名簿', () => {
  const roster = {
    members: [
      { id: 'boss', name: 'Boss', tier: 0, scope: 'team' },
      { id: 'general', name: 'General', tier: 1, scope: 'common' },
    ],
    available: [
      { id: 'writer', name: 'Writer', tier: 2, scope: 'common' },
      { id: 'scout', name: 'Scout', tier: 3, scope: 'team' },
    ],
    errors: [],
  }

  function render(over = {}) {
    const target = document.createElement('div')
    document.body.appendChild(target)
    const app = mount(TeamPanel, {
      target,
      props: {
        sessionId: 's1',
        roster,
        tickets: [],
        models: [],
        colorOf: () => '',
        onRoster: () => {},
        onTickets: async () => {},
        ...over,
      },
    })
    flushSync()
    return { target, app }
  }

  const heads = (t) =>
    [...t.querySelectorAll('.head')].map((e) => e.textContent.replace(/\s*\d+\s*$/, '').trim())

  const rows = (t, i) =>
    [...t.querySelectorAll('section')[i].querySelectorAll('.id')].map((e) => e.textContent)

  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('欄は メンバー / 共通 / チーム / チケット / 流れ図 の 5 つ', () => {
    const { target, app } = render()
    expect(heads(target)).toEqual([
      'この会話のメンバー',
      '共通エージェント',
      'チームエージェント',
      'チケット',
      '流れ図',
    ])
    unmount(app)
  })

  // 由来で割ると、@ の候補を知るのに 2 か所を見て頭の中で合成することになる。
  it('有効なものは由来を問わず 1 つの欄に並ぶ', () => {
    const { target, app } = render()
    expect(rows(target, 0)).toEqual(['boss', 'general'])
    unmount(app)
  })

  it('メンバーには由来の印が付く', () => {
    const { target, app } = render()
    const from = [...target.querySelectorAll('section')[0].querySelectorAll('.from')]
    expect(from.map((e) => e.textContent)).toEqual(['チーム', '共通'])
    unmount(app)
  })

  // 有効になっているチームエージェントも、ここから直せる。共有物なので、
  // 有効かどうかで編集できたりできなかったりするのは筋が通らない。
  it('チームの欄には有効なものも未有効なものも並ぶ', () => {
    const { target, app } = render()
    expect(rows(target, 2)).toEqual(['boss', 'scout'])
    unmount(app)
  })

  it('共通の欄には未有効なものだけ並ぶ', () => {
    const { target, app } = render()
    expect(rows(target, 1)).toEqual(['writer'])
    unmount(app)
  })

  // 窓口は無い。宛先はメンションで決まるので、指名する場所も要らない。
  it('窓口の印も、窓口にする手も無い', () => {
    const { target, app } = render()
    expect(target.textContent).not.toContain('窓口')
    unmount(app)
  })

  // 名簿が空になることは許す。空だと送るときに止まり、直す場所は目の前にある。
  it('規定のエージェントも外せる', () => {
    const called = []
    const { target, app } = render({
      roster: {
        members: [{ id: 'general', name: 'General', tier: 0, scope: 'common' }],
        available: [],
        errors: [],
      },
      onRoster: (r) => called.push(r),
    })
    const off = [...target.querySelectorAll('section')[0].querySelectorAll('button')].find(
      (b) => b.textContent.trim() === '外す',
    )
    expect(off).not.toBe(undefined)
    expect(off.disabled).toBe(false)
    unmount(app)
  })

  // いままで黙って 1 会話にしか効かなかったものが、黙って全部に効くように
  // なる。そこは黙ってはいけない。
  it('チームの欄は共有であることを言う', () => {
    const { target, app } = render()
    const lede = target.querySelectorAll('section')[2].querySelector('.lede').textContent
    expect(lede).toContain('すべてのチーム会話で共有されます')
    expect(lede).toContain('全ての会話から居なくなります')
    unmount(app)
  })

  it('削除の確認も全ての会話から消えると言う', () => {
    const { target, app } = render()
    const del = [...target.querySelectorAll('section')[2].querySelectorAll('button')].find(
      (b) => b.textContent.trim() === '削除',
    )
    del.click()
    flushSync()
    const box = document.querySelector('[role="alertdialog"]')
    expect(box.textContent).toContain('全ての会話から居なくなります')
    unmount(app)
  })
})

describe('チケットのカード', () => {
  const roster = {
    members: [{ id: 'boss', name: 'Boss', tier: 0, scope: 'common' }],
    available: [],
    errors: [],
  }

  const tk = (o) => ({
    number: 1,
    title: '設計の確認',
    body: '',
    assignee: '',
    due: '',
    status: '新規',
    priority: '中',
    author: '',
    note_count: 0,
    created_at: '2026-09-08T09:00:00Z',
    updated_at: '2026-09-08T09:00:00Z',
    ...o,
  })

  function render(tickets) {
    const target = document.createElement('div')
    document.body.appendChild(target)
    const app = mount(TeamPanel, {
      target,
      props: {
        sessionId: 's1',
        roster,
        tickets,
        models: [],
        colorOf: () => '',
        onRoster: () => {},
        onTickets: async () => {},
      },
    })
    flushSync()
    return { target, app }
  }

  beforeEach(() => {
    document.body.innerHTML = ''
  })

  // 1 行に詰め込むと題が切れて、何の仕事か分からない。題を主に置く。
  it('番号・題・担当・期限・注記の数を出す', () => {
    const { target, app } = render([
      tk({ assignee: 'hand', due: '2026-09-30', note_count: 3, status: '進行中' }),
    ])
    const card = target.querySelector('.card')
    expect(card).not.toBe(null)
    expect(card.querySelector('.ttitle').textContent).toBe('設計の確認')
    expect(card.textContent).toContain('#1')
    expect(card.textContent).toContain('hand')
    expect(card.textContent).toContain('2026-09-30')
    expect(card.textContent).toContain('注記 3')
    // 状態は列が示している。札にも出すと同じことが 2 か所に並ぶ。
    expect(card.textContent).not.toContain('進行中')
    unmount(app)
  })

  it('担当が無ければそう書く', () => {
    const { target, app } = render([tk()])
    expect(target.querySelector('.who').textContent.trim()).toBe('未割り当て')
    unmount(app)
  })

  // 目立たせるのは緊急と高だけ。全部に印を付けると、どれも目立たない。
  it('優先度は緊急と高だけ出す', () => {
    const { target: a, app: A } = render([tk({ priority: '中' })])
    expect(a.querySelector('.pri')).toBe(null)
    unmount(A)

    const { target: b, app: B } = render([tk({ priority: '緊急' })])
    expect(b.querySelector('.pri').classList.contains('urgent')).toBe(true)
    unmount(B)
  })

  // 並びは優先度の高い順、同じなら期限の近い順。番号順だと、いま効いている
  // 仕事が古い番号の下に沈む。
  it('列の中を優先度と期限で並べ替える', () => {
    const { target, app } = render([
      tk({ number: 1, title: '低い', priority: '低' }),
      tk({ number: 2, title: '遅い期限', priority: '高', due: '2026-12-01' }),
      tk({ number: 3, title: '急ぎ', priority: '緊急' }),
      tk({ number: 4, title: '近い期限', priority: '高', due: '2026-09-10' }),
    ])
    const titles = [...target.querySelectorAll('.ttitle')].map((e) => e.textContent)
    expect(titles).toEqual(['急ぎ', '近い期限', '遅い期限', '低い'])
    unmount(app)
  })

  it('終了の列は既定で出さない', () => {
    const { target, app } = render([tk({ status: '終了' })])
    expect(target.querySelector('.card')).toBe(null)
    expect([...target.querySelectorAll('.colhead')].map((e) => e.textContent)).toEqual([])
    target.querySelector('.closed input[type="checkbox"]').click()
    flushSync()
    expect(target.querySelector('.card.closed')).not.toBe(null)
    unmount(app)
  })

  // 状態ごとの列に分け、左から新規 → 終了 で並べる。1 本の並びだと、どこで
  // 止まっているのかを読み取るのに全部を見ることになる。
  it('状態ごとの列を左から順に並べる', () => {
    const { target, app } = render([
      tk({ number: 1, title: 'a', status: 'レビュー' }),
      tk({ number: 2, title: 'b', status: '新規' }),
    ])
    const heads = [...target.querySelectorAll('.colhead')].map((e) =>
      e.textContent.replace(/\d+$/, ''),
    )
    expect(heads).toEqual(['新規', '進行中', '解決', 'レビュー'])
    unmount(app)
  })

  it('札はその状態の列に入り、列は件数を出す', () => {
    const { target, app } = render([
      tk({ number: 1, title: 'a', status: '進行中' }),
      tk({ number: 2, title: 'b', status: '進行中' }),
      tk({ number: 3, title: 'c', status: '新規' }),
    ])
    const cols = [...target.querySelectorAll('.col')]
    const titles = cols.map((c) => [...c.querySelectorAll('.ttitle')].map((e) => e.textContent))
    expect(titles[0]).toEqual(['c'])
    expect(titles[1]).toEqual(['a', 'b'])
    expect(titles[2]).toEqual([])
    expect(cols[1].querySelector('.cn').textContent).toBe('2')
    unmount(app)
  })

  // 空の列も残す。詰まっている場所は、空いている列があってはじめて形で分かる。
  it('空の列も残す', () => {
    const { target, app } = render([tk({ status: '新規' })])
    expect(target.querySelectorAll('.col')).toHaveLength(4)
    expect(target.querySelectorAll('.colempty')).toHaveLength(3)
    unmount(app)
  })

  // 消すのは取り消せない操作なので、確認を挟む。
  it('消す前に確認する', () => {
    const { target, app } = render([tk()])
    target.querySelector('.kill').click()
    flushSync()
    const box = document.querySelector('[role="alertdialog"]')
    expect(box).not.toBe(null)
    expect(box.textContent).toContain('#1 設計の確認')
    expect(box.textContent).toContain('注記も一緒に消えます')
    unmount(app)
  })
})

describe('右パネルの幅', () => {
  const roster = { members: [], available: [], errors: [] }

  function render(over = {}) {
    const target = document.createElement('div')
    document.body.appendChild(target)
    const sized = []
    const app = mount(TeamPanel, {
      target,
      props: {
        sessionId: 's1',
        roster,
        tickets: [],
        models: [],
        colorOf: () => '',
        onRoster: () => {},
        onTickets: async () => {},
        width: 264,
        bounds: { min: 200, max: 620, base: 264 },
        onResize: (px) => sized.push(px),
        onSizing: () => {},
        ...over,
      },
    })
    flushSync()
    return { target, app, sized, grip: target.querySelector('.grip') }
  }

  function press(el, key, shift = false) {
    el.dispatchEvent(
      new KeyboardEvent('keydown', { key, shiftKey: shift, bubbles: true, cancelable: true }),
    )
    flushSync()
  }

  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('つまみは幅を表す分割線として置かれる', () => {
    const { grip, app } = render()
    expect(grip).not.toBe(null)
    expect(grip.getAttribute('role')).toBe('separator')
    expect(grip.getAttribute('aria-valuenow')).toBe('264')
    expect(grip.getAttribute('aria-valuemin')).toBe('200')
    expect(grip.getAttribute('aria-valuemax')).toBe('620')
    // 掴めない人にも道が要るので、焦点を置ける。
    expect(grip.getAttribute('tabindex')).toBe('0')
    unmount(app)
  })

  // パネルは右にあるので、左へ動かすほど広くなる。
  it('矢印で広げたり狭めたりできる', () => {
    const { grip, sized, app } = render()
    press(grip, 'ArrowLeft')
    press(grip, 'ArrowRight')
    press(grip, 'ArrowLeft', true)
    expect(sized).toEqual([280, 248, 312])
    unmount(app)
  })

  it('Home と End で端まで振れる', () => {
    const { grip, sized, app } = render()
    press(grip, 'Home')
    press(grip, 'End')
    expect(sized).toEqual([620, 200])
    unmount(app)
  })

  it('関係のないキーは素通しする', () => {
    const { grip, sized, app } = render()
    press(grip, 'a')
    expect(sized).toEqual([])
    unmount(app)
  })

  it('ダブルクリックで既定へ戻す', () => {
    const { grip, sized, app } = render({ width: 500 })
    grip.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }))
    flushSync()
    expect(sized).toEqual([264])
    unmount(app)
  })
})
