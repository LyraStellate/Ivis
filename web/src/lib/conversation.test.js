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
