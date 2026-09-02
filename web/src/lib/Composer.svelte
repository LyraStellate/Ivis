<script>
  let { disabled = false, busy = false, onSend } = $props()

  let draft = $state('')
  let area = $state(null)

  function submit() {
    const text = draft.trim()
    if (!text || disabled) return
    draft = ''
    onSend(text)
    area?.focus()
  }

  function onKeydown(e) {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      submit()
    }
  }
</script>

<form
  onsubmit={(e) => {
    e.preventDefault()
    submit()
  }}
>
  <textarea
    bind:this={area}
    bind:value={draft}
    onkeydown={onKeydown}
    rows="3"
    {disabled}
    placeholder={busy ? '生成中です。中断は Esc。' : 'メッセージを入力'}
    aria-label="メッセージ"
  ></textarea>
  <div class="bar">
    <span class="hint">Ctrl + Enter で送信</span>
    <button class="primary" type="submit" disabled={disabled || !draft.trim()}>送信</button>
  </div>
</form>

<style>
  form {
    border-top: 1px solid var(--border);
    padding: 10px 14px 12px;
    display: grid;
    gap: 7px;
    background: var(--surface);
  }
  textarea {
    resize: vertical;
    min-height: 3.6em;
    line-height: 1.6;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .hint {
    font-size: 11px;
    color: var(--fg-muted);
  }
  .bar button {
    margin-left: auto;
  }
</style>
