import { describe, it, expect } from 'vitest'
import { blank, toForm, copyOf, check, payload, DEFAULT_ID, MIN_TIER } from './agentForm.js'

const ok = () => ({ ...blank('m'), id: 'writer' })

describe('check', () => {
  it('問題が無ければ null', () => {
    expect(check(ok())).toBeNull()
  })

  it('ID が空なら断る', () => {
    expect(check({ ...ok(), id: '' })?.field).toBe('id')
  })

  it('ID に区切りが入っていたら断る', () => {
    // ID はそのままファイル名になるため、別の場所を指せてはならない。
    expect(check({ ...ok(), id: 'a/b' })?.field).toBe('id')
    expect(check({ ...ok(), id: '../x' })?.field).toBe('id')
  })

  it('既にある ID なら断る', () => {
    expect(check(ok(), ['writer'])?.field).toBe('id')
  })

  it('モデル未指定なら断る', () => {
    expect(check({ ...ok(), model: '  ' })?.field).toBe('model')
  })

  it('利用者は Tier 0 を作れない', () => {
    expect(check({ ...ok(), tier: 0 })?.field).toBe('tier')
  })

  it('規定エージェントの Tier は 0 で固定', () => {
    expect(check({ ...ok(), id: DEFAULT_ID, tier: 0 })).toBeNull()
    expect(check({ ...ok(), id: DEFAULT_ID, tier: 1 })?.field).toBe('tier')
  })
})

describe('toForm と payload', () => {
  it('定義を往復しても中身が変わらない', () => {
    const a = {
      id: 'r', name: 'R', description: 'd', model: 'm', instructions: 'i',
      tier: 3, tools: ['read_file'], skills: ['*'],
      memory: true, thinking: true, color: 'rose', file: '/x/r.json', fixed: false,
    }
    const got = payload(toForm(a))
    expect(got).toMatchObject({
      id: 'r', tier: 3, memory: true, thinking: true, color: 'rose', tools: ['read_file'],
    })
    // 読み込み元と固定の印は編集の対象ではないので送らない。
    expect(got.file).toBeUndefined()
    expect(got.fixed).toBeUndefined()
  })

  it('新しい入力は規定より 1 つ下の Tier で始まる', () => {
    expect(blank().tier).toBe(MIN_TIER)
  })
})

describe('copyOf', () => {
  it('ID を空け、名前に複製と分かる印を付ける', () => {
    const c = copyOf({ ...ok(), name: 'Writer' })
    expect(c.id).toBe('')
    expect(c.name).toContain('複製')
  })

  it('中身はそのまま持ち越す', () => {
    const c = copyOf({ ...ok(), tier: 4, tools: ['read_file'], memory: true })
    expect(c.tier).toBe(4)
    expect(c.tools).toEqual(['read_file'])
    expect(c.memory).toBe(true)
  })
})
