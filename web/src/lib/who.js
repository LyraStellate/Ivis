// 話し手の見せ方を決める。札 (アバター) は置かないので、誰の発言かを伝える
// 手段は名前の色と、そのエージェントへ委譲したときの縦線の色だけになる。

/** app.css が持つ話し手の色。順序は色を自動で割り当てるときの枠でもある。 */
export const COLORS = ['blue', 'green', 'amber', 'violet', 'teal', 'rose', 'orange', 'indigo']

/**
 * エージェント ID から色を決める。定義で色が選ばれていればそれを使い、
 * 無ければ ID から決める。同じ ID は常に同じ色になる。
 *
 * @param agentId 話し手
 * @param colorOf ID から定義された色名を引く関数。省略すると常に自動。
 */
export function whoColor(agentId, colorOf) {
  if (!agentId) return USER_COLOR
  const named = colorOf?.(agentId)
  if (named && COLORS.includes(named)) return 'var(--who-' + named + ')'
  return 'var(--who-' + COLORS[slot(agentId)] + ')'
}

/**
 * 定義で選ばれた色を、渡された一覧から順に探す。見つからなければ空。
 *
 * 一覧を複数受けるのは、話し手が 1 つの一覧に居るとは限らないためである。
 * チームのメンバーは共通のエージェント一覧に居ないので、そこだけを見ると
 * 定義で色を選んでも常に ID から決まる自動色になる。
 *
 * @param id 話し手
 * @param lists {id, color} を持つものの一覧。先に渡したものを優先する。
 */
export function chooseColor(id, ...lists) {
  if (!id) return ''
  for (const list of lists) {
    const hit = list?.find((a) => a.id === id)
    if (hit?.color) return hit.color
  }
  return ''
}

/** 利用者は色を持たない。人は常駐の話し手ではないため。 */
export const USER_COLOR = 'var(--fg-bright)'

/** 画面に出す利用者の名前。 */
export const USER_NAME = 'あなた'

/**
 * FNV-1a で 0..COLORS.length-1 へ写す。文字コードの和では "abc" と "acb" が
 * 同じ枠に入り、名前の似たエージェントが同じ色になる。
 */
function slot(id) {
  let h = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0) % COLORS.length
}
