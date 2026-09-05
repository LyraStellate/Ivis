import { describe, it, expect } from 'vitest'
import { bucket, byBucket, summarizeArgs, duration } from './format.js'

const at = (s) => new Date(s)

describe('bucket', () => {
  const now = at('2026-09-03T10:00:00')

  it('暦日で数える', () => {
    // 24 時間で数えると、昨日の深夜と今日の未明が同じ区分に入って
    // 日付から探せなくなる。
    expect(bucket('2026-09-03T00:01:00', now)).toBe('今日')
    expect(bucket('2026-09-02T23:59:00', now)).toBe('昨日')
  })

  it('7 日までとそれ以前を分ける', () => {
    expect(bucket('2026-08-27T12:00:00', now)).toBe('過去 7 日')
    expect(bucket('2026-08-26T12:00:00', now)).toBe('それ以前')
  })

  it('読めない日付でも落ちない', () => {
    expect(bucket('', now)).toBe('それ以前')
  })
})

describe('byBucket', () => {
  it('新しい順の並びを、続いた区分ごとにまとめる', () => {
    const now = at('2026-09-03T10:00:00')
    const list = [
      { id: '1', updated_at: '2026-09-03T09:00:00' },
      { id: '2', updated_at: '2026-09-03T08:00:00' },
      { id: '3', updated_at: '2026-09-02T08:00:00' },
    ]
    expect(byBucket(list, now).map((g) => [g.label, g.items.length])).toEqual([
      ['今日', 2],
      ['昨日', 1],
    ])
  })

  it('空でも落ちない', () => {
    expect(byBucket([], new Date())).toEqual([])
    expect(byBucket(undefined, new Date())).toEqual([])
  })

  it('Discord の会話を先頭へ固定する', () => {
    const now = new Date('2026-09-05T12:00:00')
    const groups = byBucket(
      [
        { id: 'a', updated_at: '2026-09-05T11:00:00' },
        { id: 'b', updated_at: '2026-08-01T09:00:00', source: 'discord' },
        { id: 'c', updated_at: '2026-09-04T09:00:00' },
      ],
      now,
    )

    // 場所に結び付いた会話なので、最後に喋った日付で探すことにはならない。
    expect(groups[0].label).toBe('Discord')
    expect(groups[0].items.map((s) => s.id)).toEqual(['b'])
    // 残りは今まで通り日付で分かれる。
    expect(groups.slice(1).map((g) => g.label)).toEqual(['今日', '昨日'])
  })

  it('Discord が無ければ区分を作らない', () => {
    const now = new Date('2026-09-05T12:00:00')
    const groups = byBucket([{ id: 'a', updated_at: '2026-09-05T11:00:00' }], now)
    expect(groups.map((g) => g.label)).toEqual(['今日'])
  })
})

describe('summarizeArgs', () => {
  it('何を操作するかを先に出す', () => {
    // content が先に来ると、長い本文で行が潰れて対象が読めない。
    expect(summarizeArgs({ content: 'x'.repeat(80), path: 'docs/a.md' })).toBe('docs/a.md')
  })
})

describe('duration', () => {
  it('秒に届くまではミリ秒で出す', () => {
    expect(duration(12)).toBe('12ms')
    expect(duration(1500)).toBe('1.5s')
  })
})
