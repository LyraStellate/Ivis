<script>
  import { isSubmit } from './keys.js'
  import Gauge from './Gauge.svelte'

  let {
    commands = [],
    // members はチームセッションの名簿。宛先の候補に使う。直列の会話では空。
    members = [],
    leadId = '',
    disabled = false,
    // reason は送れない理由。ただ押せなくすると、壊れているのか、そういう
    // ものなのかが分からない。
    reason = '',
    busy = false,
    agents = [],
    agentId = '',
    usage = null,
    draftBack = null,
    onSend,
    onCancel,
    onAgentChange,
  } = $props()

  // いま誰が答えるかは、書く場所のすぐ隣にあるべきである。画面の隅に置くと、
  // 送る直前に相手を確かめる動作が視線の往復になる。
  const current = $derived(agents.find((a) => a.id === agentId) ?? null)

  let draft = $state('')
  let area = $state(null)

  // 巻き戻した依頼を書きかけとして戻す。消してから同じ文を打ち直させる
  // 理由がない。入れ物ごと差し替わるので、同じ本文でも毎回反映される。
  $effect(() => {
    if (!draftBack) return
    draft = draftBack.text
    area?.focus()
  })

  // 打った分だけ伸ばす。行数を決め打つと、短い依頼では無駄に空き、長い依頼
  // では書いたものが見えない。上限を超えたらその中で送る。
  const MAX = 320
  $effect(() => {
    draft
    if (!area) return
    area.style.height = 'auto'
    area.style.height = Math.min(area.scrollHeight, MAX) + 'px'
  })

  // 先頭が / なら、それはコマンドである。先頭が @ なら宛先である。どちらも
  // 名前を打っている間だけ候補を出す。引数や本文まで打ったあとも出し続けると、
  // 書いている文字の上に一覧が居座る。
  //
  // 宛先を先頭でしか受けないのは、サーバーが本文の先頭しか読まないため
  // (internal/team の ParseMention)。効かない場所で候補を出せば、選んだのに
  // 届かないことが起きる (#640275)。
  //
  // Esc で閉じたことを覚える。閉じたそばから開き直しては、下の文字が読めない。
  let dismissed = $state(false)
  let pick = $state(0)

  const typingCmd = $derived(/^\/[^\s]*$/.test(draft))
  const typingTo = $derived(members.length > 0 && /^@[^\s]*$/.test(draft))
  const mark = $derived(typingTo ? '@' : '/')

  // 宛先の候補。"*" を先に置く。誰に頼めばよいか分からないときにこそ開く
  // 一覧なので、判断を委ねる選択肢が上にある。
  const addressees = $derived([
    { name: '*', desc: leadId ? `宛先を ${leadId} が決めます` : '宛先を窓口が決めます' },
    ...members.map((m) => ({
      name: m.id,
      desc: `${m.name}${m.lead ? ' (窓口)' : ''} \u00b7 Tier ${m.tier}`,
    })),
  ])

  const matches = $derived.by(() => {
    const list = typingCmd ? commands : typingTo ? addressees : []
    const typed = draft.toLowerCase()
    return list.filter((c) => (mark + c.name).toLowerCase().startsWith(typed))
  })
  const showList = $derived(!dismissed && matches.length > 0)

  $effect(() => {
    draft
    dismissed = false
    pick = 0
  })

  // 候補を選ぶと名前まで入る。引数や本文が続くので、送信まではしない。
  function complete(name) {
    draft = mark + name + ' '
    dismissed = true
    area?.focus()
  }

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
    if (showList) {
      // 候補が出ている間は、上下と Tab をその選択に使う。Enter は選ぶ側に
      // 寄せる。打ち終えたつもりで送ってしまうより、選び直せるほうがよい。
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        const d = e.key === 'ArrowDown' ? 1 : -1
        pick = (pick + d + matches.length) % matches.length
        return
      }
      if (e.key === 'Tab' || (isSubmit(e) && matches.length > 0)) {
        e.preventDefault()
        complete(matches[pick].name)
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        dismissed = true
        return
      }
    }
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
    {#if showList}
      <!-- 打つ場所のすぐ上に出す。設定の奥に一覧があっても、いま打っている
           人は読まない。 -->
      <ul class="cmds" role="listbox" aria-label={typingTo ? '宛先' : 'コマンド'}>
        {#each matches as c, i (c.name)}
          <li>
            <button
              type="button"
              class:on={i === pick}
              role="option"
              aria-selected={i === pick}
              onmousedown={(e) => {
                e.preventDefault()
                complete(c.name)
              }}
            >
              <span class="cname mono">{mark}{c.name}</span>
              <span class="cdesc">{c.desc}</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
    <textarea
      bind:this={area}
      bind:value={draft}
      onkeydown={onKeydown}
      rows="1"
      {disabled}
      placeholder={reason || (busy ? '次のメッセージを書いておけます' : 'メッセージを入力')}
      aria-label="メッセージ"
    ></textarea>
    <div class="bar">
      {#if agents.length > 0}
        <label class="who">
          <span class="sr">エージェント</span>
          <select
            value={agentId}
            onchange={(e) => onAgentChange?.(e.currentTarget.value)}
            disabled={busy}
          >
            {#each agents as a (a.id)}
              <option value={a.id}>{a.name}</option>
            {/each}
          </select>
        </label>
        {#if current?.model}<span class="model mono">{current.model}</span>{/if}
      {/if}

      <Gauge tokens={usage?.tokens ?? 0} limit={usage?.limit ?? 0} />

      <!-- コマンドがあることは、打つ場所の隣でしか伝わらない。設定の奥に
           書いても、いま送ろうとしている人は読まない。 -->
      <span class="hint">
        {reason ||
          (busy
            ? '生成中は送信できません。Esc で中断'
            : draft.startsWith('/')
              ? 'コマンドとして実行します。/help で一覧'
              : members.length > 0 && !draft.startsWith('@')
                ? `宛先を書かなければ ${leadId} へ届きます。@ で相手を選べます`
                : 'Shift + Enter で改行')}
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

  /* コマンドの候補。入力欄の中に置き、打っている文字のすぐ上に出す。 */
  .cmds {
    list-style: none;
    margin: 0 0 6px;
    padding: 0 0 6px;
    border-bottom: 1px solid var(--line);
  }
  .cmds button {
    display: flex;
    align-items: baseline;
    gap: 8px;
    width: 100%;
    padding: 4px 6px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--fg);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .cmds button:hover,
  .cmds button.on {
    background: var(--hover, rgb(128 128 128 / 0.12));
  }
  .cname {
    flex: none;
    color: var(--accent-line);
    font-size: 12px;
  }
  .cdesc {
    overflow: hidden;
    color: var(--fg-muted);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
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

  /* 相手とモデルは、書く手を邪魔しない大きさに留める。決めるのは設定側で、
     ここは今の相手を確かめて切り替えるだけの場所である。 */
  .who select {
    height: 24px;
    padding: 0 22px 0 8px;
    font-size: 11px;
    background-color: transparent;
    border-color: transparent;
  }
  .who select:hover:not(:disabled) {
    background-color: var(--control);
    border-color: var(--border);
  }
  .model {
    flex: none;
    font-size: 11px;
    color: var(--g9);
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
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
