// 末尾へ追従するのは、利用者が既に末尾付近にいるときだけとする。遡って
// 読んでいる間は位置を動かさない。更新のたびに scrollTop へ代入する方法は
// 取らない。利用者自身の操作と区別が付かないためである。

const THRESHOLD = 64

export function scrollToBottom(el) {
  if (el) el.scrollTop = el.scrollHeight
}

export function isAtBottom(el) {
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight <= THRESHOLD
}

/**
 * スクロールする要素に付ける。中身は単一の子要素にまとめておくこと。
 * options.onPinned(pinned) で追従しているかどうかが通知される。
 */
export function stickToBottom(node, options = {}) {
  let opts = options
  let pinned = true
  // 直前に見ていた位置。追従をやめるかどうかは、末尾からの距離ではなく
  // 「上へ動いたか」で決める。中身が伸びた瞬間は末尾から離れるが、それは
  // 利用者が遡ったわけではない。距離だけで判断すると、生成が速いほど
  // 勝手に追従が外れる。委譲の中でツールの行が次々に増えるときに顕著だった。
  let lastTop = node.scrollTop

  const content = node.firstElementChild
  const follow = () => {
    if (!pinned) return
    scrollToBottom(node)
    lastTop = node.scrollTop
  }

  const onScroll = () => {
    const top = node.scrollTop
    const wentUp = top < lastTop - 2
    lastTop = top

    if (pinned && wentUp && !isAtBottom(node)) {
      pinned = false
      opts.onPinned?.(false)
      return
    }
    // 末尾へ戻ってきたら追従を再開する。
    if (!pinned && isAtBottom(node)) {
      pinned = true
      opts.onPinned?.(true)
    }
  }

  // 内容が伸びたときと、窓の大きさが変わったときの両方を見る。
  const ro = new ResizeObserver(follow)
  if (content) ro.observe(content)
  ro.observe(node)

  node.addEventListener('scroll', onScroll, { passive: true })
  scrollToBottom(node)
  lastTop = node.scrollTop

  return {
    update(next) {
      opts = next ?? {}
    },
    destroy() {
      ro.disconnect()
      node.removeEventListener('scroll', onScroll)
    },
  }
}
