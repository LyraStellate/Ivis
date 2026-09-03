import { describe, it, expect } from 'vitest'
import { whoColor, USER_COLOR } from './who.js'

describe('whoColor', () => {
  it('同じ ID からは常に同じ色が出る', () => {
    expect(whoColor('researcher')).toBe(whoColor('researcher'))
  })

  it('8 つの枠のいずれかを指す', () => {
    for (const id of ['a', 'main', 'researcher', 'writer', 'planner']) {
      expect(whoColor(id)).toMatch(/^var\(--who-[1-8]\)$/)
    }
  })

  it('並べ替えただけの ID を同じ枠に入れない', () => {
    // 文字コードの和で写すとここが同じ色になり、名前の似たエージェントを
    // 見分けられなくなる。
    expect(whoColor('abc')).not.toBe(whoColor('acb'))
  })

  it('十分な数の ID を与えると 8 つの枠が埋まる', () => {
    const seen = new Set()
    for (let i = 0; i < 200; i++) seen.add(whoColor('agent-' + i))
    expect(seen.size).toBe(8)
  })

  it('ID が無いときは利用者と同じ扱いにして落ちない', () => {
    expect(whoColor('')).toBe(USER_COLOR)
    expect(whoColor(undefined)).toBe(USER_COLOR)
  })
})
