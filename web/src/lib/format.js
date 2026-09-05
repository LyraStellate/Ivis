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

/** 一覧をまとめるための区分。新しい順に並んでいることを前提にする。 */
export const BUCKETS = ['今日', '昨日', '過去 7 日', 'それ以前']

/**
 * 更新日時を区分へ写す。日付の境界で分けるので、24 時間ではなく暦日で数える。
 * 「昨日の 23:59」と「今日の 00:01」が同じ区分に入ると、日付で探せなくなる。
 */
export function bucket(iso, now = new Date()) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return BUCKETS[3]
  const day = (x) => Math.floor((x - x.getTimezoneOffset() * 60000) / 86400000)
  const diff = day(now) - day(d)
  if (diff <= 0) return BUCKETS[0]
  if (diff === 1) return BUCKETS[1]
  if (diff <= 7) return BUCKETS[2]
  return BUCKETS[3]
}

/**
 * 並びを区分ごとのまとまりへ分ける。空の区分は落とす。
 *
 * Discord から来た会話は日付ではなく出自でまとめ、先頭に固定する。場所に
 * 結び付いた会話なので、最後に喋った日付で探すことにはならない。
 */
export function byBucket(sessions, now = new Date()) {
  const pinned = []
  const rest = []
  for (const s of sessions ?? []) (s.source === 'discord' ? pinned : rest).push(s)

  const out = []
  if (pinned.length) out.push({ label: 'Discord', items: pinned, pinned: true })
  for (const s of rest) {
    const label = bucket(s.updated_at, now)
    const last = out[out.length - 1]
    if (last && !last.pinned && last.label === label) last.items.push(s)
    else out.push({ label, items: [s] })
  }
  return out
}
