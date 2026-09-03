// 話し手の見せ方を決める。札 (アバター) は置かないので、誰の発言かを伝える
// 手段は名前の色と、そのエージェントへ委譲したときの縦線の色だけになる。

/** app.css が持つ話し手の色の数。 */
const SLOTS = 8

/**
 * エージェント ID から色を決める。同じ ID は常に同じ色になる。
 * 色相環を機械的に割ると濁った黄や蛍光の緑が出るため、明度をそろえて選んだ
 * 8 色の中から選ぶ。
 */
export function whoColor(agentId) {
  if (!agentId) return 'var(--fg-bright)'
  return 'var(--who-' + slot(agentId) + ')'
}

/** 利用者は色を持たない。人は常駐の話し手ではないため。 */
export const USER_COLOR = 'var(--fg-bright)'

/** 画面に出す利用者の名前。 */
export const USER_NAME = 'あなた'

/**
 * FNV-1a で 1..SLOTS へ写す。文字コードの和では "abc" と "acb" が同じ枠に
 * 入り、名前の似たエージェントが同じ色になる。
 */
function slot(id) {
  let h = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return ((h >>> 0) % SLOTS) + 1
}
