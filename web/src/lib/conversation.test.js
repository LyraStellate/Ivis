import { describe, it, expect } from 'vitest'
import { Transcript } from './conversation.js'

const msg = (o) => ({ parent_id: '', tool_calls: null, tool_name: '', error: '', ...o })

describe('確定履歴の読み込み', () => {
  it('ツールの結果を、それを要求した呼び出しと対応付ける', () => {
    const t = new Transcript([])
    t.loadHistory([
      msg({ id: 'm1', role: 'user', content: '中を見て' }),
      msg({
        id: 'm2',
        role: 'assistant',
        content: '',
        tool_calls: [{ id: 'c1', name: 'list_dir', arguments: { path: '.' } }],
      }),
      msg({ id: 'm3', role: 'tool', tool_name: 'list_dir', content: '空です' }),
      msg({ id: 'm4', role: 'assistant', content: '空でした' }),
    ])

    expect(t.items.map((i) => i.kind)).toEqual(['user', 'tool', 'agent'])
    expect(t.items[1].args).toEqual({ path: '.' })
    expect(t.items[1].result).toBe('空です')
    // 本文もエラーも無い発言は残さない
    expect(t.items.find((i) => i.id === 'm2')).toBeUndefined()
  })

  it('委譲は子をまとめ、成果を独立した行にしない', () => {
    const t = new Transcript([])
    t.loadHistory([
      msg({ id: 'p1', role: 'user', content: '頼む' }),
      msg({
        id: 'p2',
        role: 'assistant',
        content: '',
        tool_calls: [{ id: 'c1', name: 'delegate', arguments: { agent: 'child' } }],
      }),
      msg({ id: 'd1', role: 'delegate', content: '数えて', agent_id: 'child' }),
      msg({ id: 'k1', role: 'assistant', content: '0 件でした', parent_id: 'd1', agent_id: 'child' }),
      msg({ id: 'p3', role: 'tool', tool_name: 'delegate', content: '0 件でした' }),
    ])

    expect(t.items.map((i) => i.kind)).toEqual(['user', 'delegate'])
    const d = t.items[1]
    expect(d.task).toBe('数えて')
    expect(d.result).toBe('0 件でした')
    expect(d.children.map((c) => c.text)).toEqual(['0 件でした'])
  })

  it('失敗した発言は本文と失敗の両方を残す', () => {
    const t = new Transcript([])
    t.loadHistory([
      msg({ id: 'm1', role: 'assistant', content: 'ここまでは', error: '接続が切れました' }),
    ])
    expect(t.items[0].text).toBe('ここまでは')
    expect(t.items[0].error).toBe('接続が切れました')
    expect(t.items[0].status).toBe('error')
  })
})

describe('ストリームの反映', () => {
  it('本文を積み上げて確定する', () => {
    const t = new Transcript([])
    t.pushUser('やあ')
    t.apply({ type: 'message_start', message_id: 'a1', agent_id: 'main', depth: 0 })
    t.apply({ type: 'delta', message_id: 'a1', text: 'こん', depth: 0 })
    t.apply({ type: 'delta', message_id: 'a1', text: 'にちは', depth: 0 })
    t.apply({ type: 'message_end', message_id: 'a1', depth: 0 })

    expect(t.items.map((i) => i.kind)).toEqual(['user', 'agent'])
    expect(t.items[1].text).toBe('こんにちは')
    expect(t.items[1].status).toBe('done')
  })

  it('ツールの呼び出しと承認と結果が同じ 1 行に集まる', () => {
    const t = new Transcript([])
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'write_file', args: { path: 'a' }, depth: 0 })
    t.apply({
      type: 'approval_request',
      tool_call_id: 'x.0',
      tool: 'write_file',
      approval: { id: 'ap1' },
      depth: 0,
    })
    expect(t.items).toHaveLength(1)
    expect(t.items[0].status).toBe('awaiting')
    expect(t.items[0].approvalId).toBe('ap1')

    t.apply({ type: 'tool_result', tool_call_id: 'x.0', result: '書きました', depth: 0 })
    expect(t.items).toHaveLength(1)
    expect(t.items[0].status).toBe('done')
    expect(t.items[0].approvalId).toBe('')
  })

  it('問いは呼び出しの行に集まり、答えると消える', () => {
    const t = new Transcript([])
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'ask_user', depth: 0 })
    t.apply({
      type: 'question',
      tool_call_id: 'x.0',
      question: { id: 'q1', text: 'どちらにしますか', choices: ['A', 'B'] },
      depth: 0,
    })
    expect(t.items).toHaveLength(1)
    expect(t.items[0].status).toBe('awaiting')
    expect(t.items[0].questionId).toBe('q1')
    expect(t.items[0].question).toBe('どちらにしますか')
    expect(t.items[0].choices).toEqual(['A', 'B'])

    t.apply({ type: 'tool_result', tool_call_id: 'x.0', result: 'A', depth: 0 })
    expect(t.items[0].questionId).toBe('')
    expect(t.items[0].status).toBe('done')
  })

  // 待っている問いを抱えたまま切れると、答える先の無い入力欄が残る。
  it('切れたときに問いの待ちを畳む', () => {
    const t = new Transcript([])
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'ask_user', depth: 0 })
    t.apply({ type: 'question', tool_call_id: 'x.0', question: { id: 'q1', text: 'ん?' }, depth: 0 })
    t.settle()
    expect(t.items[0].questionId).toBe('')
    expect(t.items[0].status).toBe('stopped')
  })

  it('拒否された実行は失敗ではなく拒否として残る', () => {
    const t = new Transcript([])
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'write_file', depth: 0 })
    t.apply({
      type: 'tool_result',
      tool_call_id: 'x.0',
      result: '利用者がこの実行を拒否しました。別の方法を検討してください。',
      depth: 0,
    })
    expect(t.items[0].status).toBe('denied')
  })

  it('委譲の子は親の中に入り、終わると元の階層へ戻る', () => {
    const t = new Transcript([])
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'delegate', depth: 0 })
    t.apply({ type: 'delegate_start', tool_call_id: 'x.0', agent_id: 'child', text: '数えて', depth: 0 })
    t.apply({ type: 'message_start', message_id: 'k1', agent_id: 'child', depth: 1 })
    t.apply({ type: 'delta', message_id: 'k1', text: '0 件', depth: 1 })
    t.apply({ type: 'message_end', message_id: 'k1', depth: 1 })
    t.apply({ type: 'delegate_end', tool_call_id: 'x.0', result: '0 件', depth: 0 })
    t.apply({ type: 'tool_result', tool_call_id: 'x.0', result: '0 件', depth: 0 })
    t.apply({ type: 'message_start', message_id: 'a2', agent_id: 'main', depth: 0 })
    t.apply({ type: 'delta', message_id: 'a2', text: '子によると 0 件', depth: 0 })
    t.apply({ type: 'message_end', message_id: 'a2', depth: 0 })

    expect(t.items.map((i) => i.kind)).toEqual(['delegate', 'agent'])
    const d = t.items[0]
    expect(d.children.map((c) => c.text)).toEqual(['0 件'])
    expect(d.result).toBe('0 件')
    // 子の発言が最上位へ漏れていない
    expect(t.items.filter((i) => i.id === 'k1')).toHaveLength(0)
  })

  it('断られた委譲はツールの行のまま残る', () => {
    const t = new Transcript([])
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'delegate', depth: 0 })
    t.apply({ type: 'tool_result', tool_call_id: 'x.0', result: 'エラー: 許可されていません', depth: 0 })
    expect(t.items[0].kind).toBe('tool')
    expect(t.items[0].status).toBe('error')
  })

  it('生成が失敗しても、そこまでの本文が残る', () => {
    const t = new Transcript([])
    t.apply({ type: 'message_start', message_id: 'a1', agent_id: 'main', depth: 0 })
    t.apply({ type: 'delta', message_id: 'a1', text: 'ここまでは', depth: 0 })
    t.apply({ type: 'error', message_id: 'a1', error: '接続が切れました', depth: 0 })

    expect(t.items[0].text).toBe('ここまでは')
    expect(t.items[0].status).toBe('error')
    expect(t.items[0].error).toBe('接続が切れました')
  })

  it('発言に結び付かない失敗は独立した通知になる', () => {
    const t = new Transcript([])
    t.apply({ type: 'error', error: 'Ollama に接続できません', depth: 0 })
    expect(t.items[0].kind).toBe('notice')
  })

  it('中断したときは生成中の項目を残したまま確定させる', () => {
    const t = new Transcript([])
    t.apply({ type: 'message_start', message_id: 'a1', agent_id: 'main', depth: 0 })
    t.apply({ type: 'delta', message_id: 'a1', text: '途中', depth: 0 })
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'write_file', depth: 0 })
    t.apply({ type: 'approval_request', tool_call_id: 'x.0', approval: { id: 'ap1' }, depth: 0 })
    t.settle()

    expect(t.items[0].text).toBe('途中')
    expect(t.items[0].status).toBe('done')
    // 承認を待っている途中で止めたツールは、失敗ではなく止まったものとして残す。
    expect(t.items[1].status).toBe('stopped')
    expect(t.items[1].approvalId).toBe('')
  })
})

it('同じ瞬間に届いた通知が同じ識別子にならない', () => {
  // 時刻を識別子にすると、同じミリ秒に 2 件届いたときに衝突して
  // 描画が同一の項目と誤認する。
  const items = []
  const tx = new Transcript(items)
  tx.apply({ type: 'error', error: '1 件目' })
  tx.apply({ type: 'error', error: '2 件目' })
  expect(items).toHaveLength(2)
  expect(items[0].id).not.toBe(items[1].id)
})

describe('推論', () => {
  it('本文とは別に溜める', () => {
    const tx = new Transcript([])
    tx.apply({ type: 'message_start', message_id: 'm1', agent_id: 'main' })
    tx.apply({ type: 'thinking', message_id: 'm1', text: '考え中' })
    tx.apply({ type: 'delta', message_id: 'm1', text: '答え' })
    tx.apply({ type: 'message_end', message_id: 'm1' })

    expect(tx.items).toHaveLength(1)
    expect(tx.items[0].thinking).toBe('考え中')
    expect(tx.items[0].text).toBe('答え')
  })

  it('推論だけの発言も残す', () => {
    // 推論して、本文を出さずにツールを呼んだ回。過程まで消すと何も見えない。
    const tx = new Transcript([])
    tx.apply({ type: 'message_start', message_id: 'm1', agent_id: 'main' })
    tx.apply({ type: 'thinking', message_id: 'm1', text: '調べよう' })
    tx.apply({ type: 'message_end', message_id: 'm1' })
    expect(tx.items).toHaveLength(1)
  })

  it('本文も推論も無い発言は残さない', () => {
    const tx = new Transcript([])
    tx.apply({ type: 'message_start', message_id: 'm1', agent_id: 'main' })
    tx.apply({ type: 'message_end', message_id: 'm1' })
    expect(tx.items).toHaveLength(0)
  })

  it('確定履歴からも読み取る', () => {
    const tx = new Transcript([])
    tx.loadHistory([
      { id: 'a', role: 'assistant', content: '答え', thinking: '過程', agent_id: 'main' },
    ])
    expect(tx.items[0].thinking).toBe('過程')
  })
})

describe('送信した発言の識別子', () => {
  it('保存された識別子で差し替える', () => {
    // 差し替えないと、その発言を指す操作 (巻き戻し) が開き直すまで使えない。
    const tx = new Transcript([])
    tx.pushUser('頼む')
    const local = tx.items[0].id
    tx.apply({ type: 'user_saved', message_id: 'srv-1' })
    expect(local).not.toBe('srv-1')
    expect(tx.items[0].id).toBe('srv-1')
  })

  it('識別子が来なければそのまま', () => {
    const tx = new Transcript([])
    tx.pushUser('頼む')
    const local = tx.items[0].id
    tx.apply({ type: 'user_saved' })
    expect(tx.items[0].id).toBe(local)
  })

  it('要素そのものを持ち越さない', () => {
    // items は画面の状態配列である。配列へ入れる前の参照を持ったまま書き換え
    // ると、値は変わるのに再描画が起きず、巻き戻しの操作が会話を開き直すまで
    // 出てこない。実際にこれで取りこぼしていたので、識別子だけを持つ形を
    // ここで固定する。
    const tx = new Transcript([])
    tx.pushUser('頼む')
    expect(typeof tx.lastSent).toBe('string')
  })

  it('差し替えは 1 度きり', () => {
    const tx = new Transcript([])
    tx.pushUser('1 回目')
    tx.apply({ type: 'user_saved', message_id: 'srv-1' })
    tx.apply({ type: 'user_saved', message_id: 'srv-2' })
    expect(tx.items[0].id).toBe('srv-1')
  })
})

// 許可も答えも別の要求で送るため、経過のストリームには何も現れない。走り出した
// ことを画面が自分で進めないと、結果が返るまで「承認待ち」のまま止まって見える。
describe('返事を返したあとの行', () => {
  const upTo = (tx) => {
    tx.pushUser('書いて')
    tx.apply({ type: 'message_start', message_id: 'm1', agent_id: 'general' })
    tx.apply({
      type: 'tool_call', tool_call_id: 'c1', tool: 'write_file', args: { path: 'a.md' },
    })
    tx.apply({
      type: 'approval_request', tool_call_id: 'c1', approval: { id: 'ap1' },
    })
  }

  it('許可したら走っている行になる', () => {
    const items = []
    const tx = new Transcript(items)
    upTo(tx)
    const row = items.find((x) => x.kind === 'tool')
    expect(row.status).toBe('awaiting')

    tx.responded('ap1')
    expect(row.status).toBe('running')
    expect(row.approvalId).toBe('')
    // 待たせた分は道具の所要時間ではないので、いまから数え直す。
    expect(row.startedAt).toBeGreaterThan(0)
  })

  it('知らない識別子では何も起きない', () => {
    const items = []
    const tx = new Transcript(items)
    upTo(tx)
    const row = items.find((x) => x.kind === 'tool')
    tx.responded('ほかの何か')
    expect(row.status).toBe('awaiting')
  })
})

// 圧縮は会話の見え方を変える。失敗ではないので、赤い通知と同じ扱いにしない。
describe('圧縮の知らせ', () => {
  it('知らせは失敗ではない項目として並ぶ', () => {
    const items = []
    const tx = new Transcript(items)
    tx.pushUser('/compact')
    tx.apply({ type: 'notice', text: '12 件をまとめました。' })

    const last = items[items.length - 1]
    expect(last.kind).toBe('notice')
    expect(last.status).toBe('done')
    expect(last.text).toContain('まとめました')
  })

  // 圧縮より前の発言も画面には残る。どこで切り替わったかが見えないと、
  // 答えが変わった理由が読めない。
  it('履歴の要約は区切りとして出る', () => {
    const items = []
    const tx = new Transcript(items)
    tx.loadHistory([
      msg({ id: 'a', role: 'user', content: '古い依頼', created_at: '2026-09-07T09:00:00Z' }),
      msg({ id: 'b', role: 'summary', content: 'これまでの経過', agent_id: 'general' }),
      msg({ id: 'c', role: 'user', content: '新しい依頼', created_at: '2026-09-07T09:10:00Z' }),
    ])

    expect(items.map((i) => i.kind)).toEqual(['user', 'summary', 'user'])
    expect(items[1].text).toBe('これまでの経過')
    // 古い発言は消えない。
    expect(items[0].text).toBe('古い依頼')
  })
})

describe('実行中の出力', () => {
  const call = (t) =>
    t.apply({ type: 'tool_call', tool_call_id: 'x.0', tool: 'run_command', args: {}, depth: 0 })

  it('届いたそばから積まれ、結果とは別に持つ', () => {
    const t = new Transcript([])
    call(t)
    t.apply({ type: 'tool_output', tool_call_id: 'x.0', text: '1 行目\n' })
    t.apply({ type: 'tool_output', tool_call_id: 'x.0', text: '2 行目\n' })

    expect(t.items[0].live).toBe('1 行目\n2 行目\n')
    // 結果へ混ぜない。混ぜると、途中の文字で成否が早とちりする。
    expect(t.items[0].result).toBe('')
    expect(t.items[0].status).toBe('running')
  })

  it('結果が来たら流していた分を捨てる', () => {
    const t = new Transcript([])
    call(t)
    t.apply({ type: 'tool_output', tool_call_id: 'x.0', text: '途中\n' })
    t.apply({ type: 'tool_result', tool_call_id: 'x.0', result: '途中\n終わり' })

    // 両方を残すと同じものが 2 度並ぶ。全部を持つのは結果のほうである。
    expect(t.items[0].live).toBe('')
    expect(t.items[0].result).toBe('途中\n終わり')
    expect(t.items[0].status).toBe('done')
  })

  it('末尾だけを残す', () => {
    const t = new Transcript([])
    call(t)
    for (let i = 0; i < 400; i++) {
      t.apply({ type: 'tool_output', tool_call_id: 'x.0', text: `行 ${i}\n` })
    }
    const lines = t.items[0].live.split('\n').filter((l) => l !== '')
    expect(lines.length).toBe(300)
    // 捨てるのは古い側。読まれるのはほぼ常に直近である。
    expect(lines[lines.length - 1]).toBe('行 399')
    expect(lines[0]).toBe('行 100')
  })

  it('繋ぎ直しで二重にならない', () => {
    const t = new Transcript([])
    call(t)
    t.apply({ type: 'tool_output', tool_call_id: 'x.0', text: 'あ\n' })
    // 繋ぎ直すと控えが頭から流し直される。
    call(t)
    t.apply({ type: 'tool_output', tool_call_id: 'x.0', text: 'あ\n' })

    expect(t.items.length).toBe(1)
    expect(t.items[0].live).toBe('あ\n')
  })

  it('知らない呼び出しの出力は捨てる', () => {
    const t = new Transcript([])
    t.apply({ type: 'tool_output', tool_call_id: 'none', text: 'x' })
    expect(t.items.length).toBe(0)
  })
})
