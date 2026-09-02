// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { renderMarkdown } from './markdown.js'

describe('markdown の描画', () => {
  it('見出しとコードブロックを組み立てる', () => {
    const html = renderMarkdown('# 見出し\n\n```js\nconst a = 1\n```\n')
    expect(html).toContain('<h1>')
    expect(html).toContain('<pre>')
    expect(html).toContain('language-js')
  })

  it('script を取り除く', () => {
    const html = renderMarkdown('本文\n\n<script>alert(1)</script>\n')
    expect(html).not.toContain('<script')
    expect(html).toContain('本文')
  })

  it('javascript: のリンクを残さない', () => {
    const html = renderMarkdown('[押す](javascript:alert(1))')
    expect(html).not.toContain('javascript:')
  })

  it('画像の onerror を取り除く', () => {
    const html = renderMarkdown('<img src=x onerror="alert(1)">')
    expect(html).not.toContain('onerror')
  })

  it('外部リンクを新しいタブで開く', () => {
    const html = renderMarkdown('[例](https://example.com)')
    expect(html).toContain('target="_blank"')
    expect(html).toContain('rel="noopener noreferrer"')
  })
})
