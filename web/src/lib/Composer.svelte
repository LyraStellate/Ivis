<script>
  import { isSubmit } from './keys.js'

  let { disabled = false, busy = false, onSend } = $props()

  let draft = $state('')
  let area = $state(null)

  // 打った分だけ伸ばす。行数を決め打つと、短い依頼では無駄に空き、長い依頼
  // では書いたものが見えない。上限を超えたらその中で送る。
  const MAX = 320
  $effect(() => {
    draft
    if (!area) return
    area.style.height = 'auto'
    area.style.height = Math.min(area.scrollHeight, MAX) + 'px'
  })

  function submit() {
    const text = draft.trim()
    if (!text || disabled) return
    draft = ''
    onSend(text)
    area?.focus()
  }

  function onKeydown(e) {
    if (!isSubmit(e)) return
    e.preventDefault()
    submit()
  }

  // 記録の中には開閉できる行が多く並ぶ。Tab だけで入力欄へ届こうとすると、
  // その全部を通り抜けることになる。文字を打ち始めたら入力欄へ移す。
  function onWindowKeydown(e) {
    if (disabled || e.ctrlKey || e.metaKey || e.altKey) return
    if (e.key.length !== 1) return
    const el = document.activeElement
    if (el === area) return
    if (el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName))) return
    // 重ねて開いている画面の操作を奪わない。
    if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return
    area?.focus()
  }
</script>

<svelte:window onkeydown={onWindowKeydown} />

<form
  onsubmit={(e) => {
    e.preventDefault()
    submit()
  }}
>
  <div class="well">
    <textarea
      bind:this={area}
      bind:value={draft}
      onkeydown={onKeydown}
      rows="1"
      {disabled}
      placeholder={busy ? '生成中。Esc で中断できます' : 'メッセージを入力'}
      aria-label="メッセージ"
    ></textarea>
    <div class="bar">
      <span class="hint">Shift + Enter で改行</span>
      <button class="primary" type="submit" disabled={disabled || !draft.trim()}>送信</button>
    </div>
  </div>
</form>

<style>
  form {
    flex: none;
    padding: 0 14px 14px;
  }

  /* 入力欄は会話の地より明るい面として持ち上げる。いま書く場所がどこかを
     面の順序で示す。 */
  .well {
    max-width: 880px;
    margin: 0 auto;
    background: var(--raised);
    border: 1px solid transparent;
    border-radius: var(--radius-lg);
    padding: 8px 10px 8px;
  }
  .well:focus-within {
    border-color: var(--accent-line);
  }

  textarea {
    background: transparent;
    border: none;
    padding: 0;
    resize: none;
    overflow-y: auto;
    line-height: 1.6;
  }
  textarea:hover { border-color: transparent; }
  textarea:focus-visible { outline: none; }

  .bar {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 6px;
  }
  .hint {
    font-size: 11px;
    color: var(--g9);
  }
  .bar button { margin-left: auto; }
</style>
