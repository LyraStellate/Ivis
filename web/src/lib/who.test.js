import { describe, it, expect } from 'vitest'
import { whoColor, chooseColor, USER_COLOR, COLORS } from './who.js'

describe('whoColor', () => {
  it('同じ ID からは常に同じ色が出る', () => {
    expect(whoColor('researcher')).toBe(whoColor('researcher'))
  })

  it('用意した色のいずれかを指す', () => {
    for (const id of ['a', 'main', 'researcher', 'writer', 'planner']) {
      expect(COLORS.map((c) => 'var(--who-' + c + ')')).toContain(whoColor(id))
    }
  })

  it('並べ替えただけの ID を同じ枠に入れない', () => {
    // 文字コードの和で写すとここが同じ色になり、名前の似たエージェントを
    // 見分けられなくなる。
    expect(whoColor('abc')).not.toBe(whoColor('acb'))
  })

  it('十分な数の ID を与えると全ての枠が埋まる', () => {
    const seen = new Set()
    for (let i = 0; i < 200; i++) seen.add(whoColor('agent-' + i))
    expect(seen.size).toBe(COLORS.length)
  })

  it('定義で選ばれた色を優先する', () => {
    expect(whoColor('anything', () => 'rose')).toBe('var(--who-rose)')
  })

  it('知らない色名は自動の割り当てに戻す', () => {
    expect(whoColor('main', () => 'puce')).toBe(whoColor('main'))
  })

  it('ID が無いときは利用者と同じ扱いにして落ちない', () => {
    expect(whoColor('')).toBe(USER_COLOR)
    expect(whoColor(undefined)).toBe(USER_COLOR)
  })
})

// 話し手が 1 つの一覧に居るとは限らない。チームのメンバーは共通の一覧に
// 居ないので、そこだけを見ると定義で色を選んでも自動色になる。
describe('chooseColor', () => {
  const common = [{ id: 'general', color: 'blue' }]
  const members = [{ id: 'painter', color: 'rose' }, { id: 'plain' }]

  it('先に渡した一覧を優先する', () => {
    expect(chooseColor('general', common, members)).toBe('blue')
  })

  it('後ろの一覧からも引く', () => {
    expect(chooseColor('painter', common, members)).toBe('rose')
  })

  it('色が選ばれていなければ空', () => {
    expect(chooseColor('plain', common, members)).toBe('')
    expect(chooseColor('居ない', common, members)).toBe('')
    expect(chooseColor('', common, members)).toBe('')
  })

  it('一覧が無くても落ちない', () => {
    expect(chooseColor('general', undefined, null)).toBe('')
  })

  // 引いた色は whoColor がそのまま使う。
  it('引いた色が whoColor へ渡ると、その色になる', () => {
    const colorOf = (id) => chooseColor(id, common, members)
    expect(whoColor('painter', colorOf)).toBe('var(--who-rose)')
    // 選ばれていなければ ID から決まる自動色へ落ちる。
    expect(whoColor('plain', colorOf)).toMatch(/^var\(--who-/)
  })
})
