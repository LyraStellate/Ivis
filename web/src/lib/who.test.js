import { describe, it, expect } from 'vitest'
import { whoColor, USER_COLOR, COLORS } from './who.js'

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
