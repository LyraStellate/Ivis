import { describe, it, expect } from 'vitest'
import { leads, owners } from './group.js'

const user = (id) => ({ id, kind: 'user' })
const agent = (id, agentId) => ({ id, kind: 'agent', agentId })
const tool = (id) => ({ id, kind: 'tool', tool: 'list_dir' })
const delegate = (id, agentId) => ({ id, kind: 'delegate', agentId })
const notice = (id) => ({ id, kind: 'notice' })

describe('leads', () => {
  it('話し手が変わる位置だけが名前を出す', () => {
    expect(leads([user('1'), agent('2', 'main'), agent('3', 'main')])).toEqual([
      true,
      true,
      false,
    ])
  })

  it('ツールの行は話し手を切り替えない', () => {
    // 道具を 1 つ使うたびに名前が出ると、1 ターンが細切れになる。
    expect(leads([agent('1', 'main'), tool('2'), agent('3', 'main')])).toEqual([
      true,
      false,
      false,
    ])
  })

  it('委譲のまとまりも話し手を切り替えない', () => {
    // 委譲の中で話しているのは子であって、親の話し手は変わらない。
    expect(leads([agent('1', 'main'), delegate('2', 'sub'), agent('3', 'main')])).toEqual([
      true,
      false,
      false,
    ])
  })

  it('別のエージェントに変わると名前を出す', () => {
    expect(leads([agent('1', 'main'), agent('2', 'sub'), agent('3', 'main')])).toEqual([
      true,
      true,
      true,
    ])
  })

  it('利用者とエージェントは互いに話し手を切り替える', () => {
    expect(leads([user('1'), agent('2', 'main'), user('3'), agent('4', 'main')])).toEqual([
      true,
      true,
      true,
      true,
    ])
  })

  it('通知は話し手を切り替えない', () => {
    expect(leads([agent('1', 'main'), notice('2'), agent('3', 'main')])).toEqual([
      true,
      false,
      false,
    ])
  })

  it('空の並びでも落ちない', () => {
    expect(leads([])).toEqual([])
    expect(leads(undefined)).toEqual([])
  })
})

describe('owners', () => {
  it('話し手を切り替えない項目には直前の話し手を持たせる', () => {
    const list = [agent('1', 'main'), tool('2'), agent('3', 'main')]
    expect(owners(list).map((o) => o.agentId)).toEqual(['main', 'main', 'main'])
  })

  it('話し手が変わればそこから後は新しい話し手になる', () => {
    const list = [user('1'), agent('2', 'main'), tool('3'), agent('4', 'sub')]
    expect(owners(list).map((o) => (o.isUser ? 'user' : o.agentId))).toEqual([
      'user',
      'main',
      'main',
      'sub',
    ])
  })

  it('話し手が決まる前の項目は null になる', () => {
    expect(owners([tool('1')])).toEqual([null])
  })
})
