/** 時刻を HH:MM で返す。 */
export function clock(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0')
}

/** 所要時間を読みやすい単位で返す。 */
export function duration(ms) {
  if (!ms && ms !== 0) return ''
  if (ms < 1000) return ms + 'ms'
  return (ms / 1000).toFixed(1) + 's'
}

/** 1 行の要約に出す引数の優先順。何を操作しようとしているかが先に読める。 */
const LEAD = ['path', 'name', 'agent', 'skill', 'script', 'task']
/** 要約に出さない引数。長すぎて行を潰すため、展開したときだけ見せる。 */
const BULKY = ['content']

/** 引数を 1 行にまとめる。ツールの行に主要な引数を出すために使う。 */
export function summarizeArgs(args) {
  if (!args || typeof args !== 'object') return ''
  const keys = Object.keys(args)
    .filter((k) => !BULKY.includes(k))
    .sort((a, b) => rank(a) - rank(b))

  const parts = []
  for (const k of keys) {
    const v = args[k]
    let s = typeof v === 'string' ? v : JSON.stringify(v)
    if (s == null) continue
    s = s.replace(/\s+/g, ' ').trim()
    if (s.length > 56) s = s.slice(0, 56) + '…'
    parts.push(LEAD.includes(k) ? s : k + '=' + s)
  }
  return parts.join('  ')
}

function rank(key) {
  const i = LEAD.indexOf(key)
  return i < 0 ? LEAD.length : i
}
