const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusable(node) {
  return [...node.querySelectorAll(FOCUSABLE)].filter(
    (el) => el.offsetWidth > 0 || el.offsetHeight > 0 || el === document.activeElement,
  )
}

/**
 * 重ねて開いた領域の中に Tab 移動を閉じ込める。閉じたときは元の位置へ焦点を戻す。
 * 背後へ抜けられると、見えているものと操作しているものが食い違う。
 */
export function trapFocus(node) {
  const previous = document.activeElement

  const onKeydown = (e) => {
    if (e.key !== 'Tab') return
    const els = focusable(node)
    if (els.length === 0) return

    const first = els[0]
    const last = els[els.length - 1]
    const active = document.activeElement

    if (!node.contains(active)) {
      ;(e.shiftKey ? last : first).focus()
      e.preventDefault()
      return
    }
    if (!e.shiftKey && active === last) {
      first.focus()
      e.preventDefault()
    } else if (e.shiftKey && active === first) {
      last.focus()
      e.preventDefault()
    }
  }

  document.addEventListener('keydown', onKeydown, true)

  return {
    destroy() {
      document.removeEventListener('keydown', onKeydown, true)
      if (previous instanceof HTMLElement && document.contains(previous)) previous.focus()
    },
  }
}
