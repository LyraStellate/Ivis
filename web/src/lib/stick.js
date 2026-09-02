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

  const content = node.firstElementChild
  const follow = () => {
    if (pinned) scrollToBottom(node)
  }

  const onScroll = () => {
    const now = isAtBottom(node)
    if (now !== pinned) {
      pinned = now
      opts.onPinned?.(pinned)
    }
  }

  // 内容が伸びたときと、窓の大きさが変わったときの両方を見る。
  const ro = new ResizeObserver(follow)
  if (content) ro.observe(content)
  ro.observe(node)

  node.addEventListener('scroll', onScroll, { passive: true })
  scrollToBottom(node)

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
