// 続けて同じ話し手が話す間は、名前と時刻を出し直さない。1 ターンが 1 つの
// まとまりとして読めるようにするための規則。

/**
 * その項目の話し手を返す。話し手を切り替えないものは null を返す。
 * ツールの行と委譲のまとまりは、そのエージェント自身の作業なので切り替えない。
 * ここで切り替えてしまうと、道具を 1 つ使うたびに名前が出て流れが切れる。
 */
export function speaker(item) {
  if (!item) return null
  if (item.kind === 'user') return 'user'
  if (item.kind === 'agent') return 'agent:' + (item.agentId ?? '')
  return null
}

/**
 * 並びと同じ長さの真偽値の配列を返す。真の位置だけが話し手の行を出す。
 */
export function leads(items) {
  const out = []
  let last = null
  for (const item of items ?? []) {
    const s = speaker(item)
    if (s == null) {
      out.push(false)
      continue
    }
    out.push(s !== last)
    last = s
  }
  return out
}

/**
 * 並びと同じ長さの、いま話している人の配列を返す。話し手を切り替えない項目
 * (ツールの行など) には、直前の話し手をそのまま持たせる。誰の作業かを色で
 * 示すために要る。
 */
export function owners(items) {
  const out = []
  let cur = null
  for (const item of items ?? []) {
    if (speaker(item) != null) {
      cur = { isUser: item.kind === 'user', agentId: item.agentId ?? '' }
    }
    out.push(cur)
  }
  return out
}
