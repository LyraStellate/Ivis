import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.setOptions({ gfm: true, breaks: true })

// 生成された HTML の中のリンクは新しいタブで開き、開いた先から元の画面を
// 触れないようにする。
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})

/**
 * markdown を挿入できる HTML へ変換する。
 * 変換してから無害化する。順序を逆にしない。
 */
export function renderMarkdown(src) {
  if (!src) return ''
  return DOMPurify.sanitize(marked.parse(src), { ADD_ATTR: ['target'] })
}
