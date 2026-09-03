<script>
  import { isSubmit } from './keys.js'

  let { disabled = false, busy = false, onSend, onCancel } = $props()

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
    // 生成中は送れない。ただし書いておくことはできる。返ってくるのを待つ間に
    // 次の依頼をまとめられるようにするためで、書きかけは消さない。
    if (disabled || busy) return
    const text = draft.trim()
    if (!text) return
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
      placeholder={busy ? '次のメッセージを書いておけます' : 'メッセージを入力'}
      aria-label="メッセージ"
    ></textarea>
    <div class="bar">
      <span class="hint">
        {busy ? '生成中は送信できません。Esc で中断' : 'Shift + Enter で改行'}
      </span>

      <!-- 始める操作と止める操作を同じ場所に置く。走っているものを止める
           のだから、探す場所も同じであるべきである。 -->
      <button
        class="go"
        class:stop={busy}
        type="button"
        onclick={busy ? onCancel : submit}
        disabled={!busy && (disabled || !draft.trim())}
        title={busy ? '生成を中断' : '送信'}
        aria-label={busy ? '生成を中断' : '送信'}
      >
        {#if busy}
          <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
            <rect x="5" y="5" width="6" height="6" rx="1.5" fill="currentColor" />
          </svg>
        {:else}
          <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
            <path d="M6.2 4.2 L11.8 8 L6.2 11.8 Z" fill="currentColor" />
          </svg>
        {/if}
      </button>
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
    padding: 8px 8px 8px 10px;
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

  .go {
    flex: none;
    margin-left: auto;
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    padding: 0;
    border-radius: 50%;
    background: var(--accent-solid);
    border-color: var(--accent-solid);
    color: var(--accent-on);
  }
  .go:hover:not(:disabled) {
    background: #ffffff;
    border-color: #ffffff;
    color: var(--accent-on);
  }
  .go:active:not(:disabled) { background: #b6d8ef; border-color: #b6d8ef; }
  .go:disabled {
    background: var(--control);
    border-color: var(--border);
    color: var(--g9);
  }

  /* 生成中。止める対象は「走っているもの」なので、進行中を表す色をそのまま
     使う。ツールの行の実行中の印と同じ色である。 */
  .go.stop {
    background: var(--control);
    border-color: var(--accent-line);
    color: var(--accent-line);
  }
  .go.stop:hover {
    background: var(--control-hover);
    border-color: var(--border-hover);
    color: var(--accent-line);
  }
</style>
