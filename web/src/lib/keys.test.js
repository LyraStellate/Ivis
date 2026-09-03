import { describe, it, expect } from 'vitest'
import { isSubmit } from './keys.js'

const key = (over) => ({ key: 'Enter', isComposing: false, keyCode: 13, ...over })

describe('isSubmit', () => {
  it('Enter は送信する', () => {
    expect(isSubmit(key())).toBe(true)
  })

  it('Shift+Enter は改行なので送信しない', () => {
    expect(isSubmit(key({ shiftKey: true }))).toBe(false)
  })

  it('変換中の Enter は送信しない', () => {
    // 和文を打っているときの Enter は候補の確定であり、送信ではない。
    // ここを通すと、確定した瞬間に書きかけが飛ぶ。
    expect(isSubmit(key({ isComposing: true }))).toBe(false)
  })

  it('isComposing を持たない環境でも keyCode 229 で変換中と分かる', () => {
    expect(isSubmit(key({ isComposing: undefined, keyCode: 229 }))).toBe(false)
  })

  it('Ctrl+Enter も送信する', () => {
    // 以前の打ち方を覚えている指がそのまま動くようにしておく。
    expect(isSubmit(key({ ctrlKey: true }))).toBe(true)
  })

  it('Enter 以外は送信しない', () => {
    expect(isSubmit(key({ key: 'a' }))).toBe(false)
  })
})
