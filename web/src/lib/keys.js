// 入力欄のキー操作。

/**
 * 送信すべき打鍵かを判定する。Enter で送信、Shift+Enter で改行。
 *
 * 和文を打つときの Enter は変換候補の確定であって送信ではない。これを送信と
 * みなすと、確定した瞬間に書きかけが飛ぶ。IME が動いている間は isComposing が
 * 真になる。古い環境では keyCode に 229 が入るだけの場合があるので両方見る。
 */
export function isSubmit(e) {
  if (e.key !== 'Enter') return false
  if (e.isComposing || e.keyCode === 229) return false
  if (e.shiftKey || e.altKey) return false
  return true
}
