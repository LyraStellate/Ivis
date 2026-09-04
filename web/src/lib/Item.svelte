<script>
  // 発言 1 件を描く。委譲は子を抱えるので自分自身を入れ子に使う。
  import Self from './Item.svelte'
  import Markdown from './Markdown.svelte'
  import { clock, duration, summarizeArgs } from './format.js'
  import { leads, owners } from './group.js'
  import { whoColor, USER_COLOR, USER_NAME } from './who.js'

  let {
    item,
    onApprove,
    onRewind = null,
    colorOf = null,
    lead = true,
    owner = null,
    nested = false,
  } = $props()

  // 巻き戻せるのは、保存済みの利用者の発言だけ。委譲の中の発言は、消したあと
  // に何を送ればよいかが決まらないので起点にしない。
  const canRewind = $derived(
    onRewind != null && !nested && item.kind === 'user' && !String(item.id).startsWith('sent-'),
  )

  // 誰のターンの中にいるか。ターン全体を括る細い縦線の色になる。
  const ownColor = $derived(owner?.isUser ? 'var(--g7)' : whoColor(owner?.agentId, colorOf))
  const isUserTurn = $derived(owner?.isUser === true)

  // 名前は話し手が変わったときだけ出す。
  const name = $derived(item.kind === 'user' ? USER_NAME : (item.agentId ?? ''))
  const nameColor = $derived(item.kind === 'user' ? USER_COLOR : whoColor(item.agentId, colorOf))

  const open = $derived(item.status === 'error' || item.status === 'awaiting')

  // 推論は、考えている間だけ開く。何も出ないまま待たされるより、いま何を
  // たどっているかが見えるほうが、待つ理由が分かる。本文が出はじめたら
  // 役目が終わるので畳む。結論が過程に押し下げられないようにするため。
  let openThink = $state(null)
  const thinkLive = $derived(item.status === 'streaming' && !item.text)
  // 利用者が自分で開閉したら、その意思を以後優先する。
  const showThink = $derived(openThink ?? thinkLive)

  // 考えている間は末尾を見せ続ける。上端で止まっていると、伸びているのに
  // 何も動いていないように見える。
  let thinkBox = $state(null)
  $effect(() => {
    item.thinking
    if (thinkLive && thinkBox) thinkBox.scrollTop = thinkBox.scrollHeight
  })

  // 委譲した先の経過も、考えている間だけ開く。何が起きているか見えないまま
  // 待たされるより、たどっている最中が見えるほうが待つ理由が分かる。
  // 終わったら畳む。親が受け取った成果のほうが、そのときの読み手の関心である。
  let openDg = $state(null)
  const dgLive = $derived(item.status === 'running')
  const showDg = $derived(openDg ?? dgLive)

  // 委譲の中は、渡した先を話し手として同じ規則で組み直す。
  const kids = $derived(item.children ?? [])
  const kidLeads = $derived(leads(kids))
  const kidOwners = $derived(owners(kids))

  // 受け取った成果は、多くの場合そのまま子の最後の発言である。両方を出すと
  // 同じ文章が続けて 2 度並ぶので、違うときだけ本文を出す。
  const lastSaid = $derived(() => {
    for (let i = kids.length - 1; i >= 0; i--) if (kids[i].kind === 'agent') return kids[i].text ?? ''
    return null
  })
  const repeats = $derived(
    item.result != null && lastSaid() != null && item.result.trim() === lastSaid().trim(),
  )
</script>

<div class="item" class:lead>
  {#if lead}
    <div class="who">
      <span class="name" style:color={nameColor}>{name}</span>
      {#if item.status === 'streaming'}
        <span class="typing" role="status" aria-label="生成中"><i></i><i></i><i></i></span>
      {/if}
      {#if item.time}<span class="time mono tnum">{clock(item.time)}</span>{/if}
    </div>
  {/if}

  <div class="line" class:user={isUserTurn} class:nested style:--own={ownColor}>
    {#if item.kind === 'user'}
      <div class="body plain">{item.text}</div>
      {#if canRewind}
        <button class="rewind quiet" onclick={() => onRewind(item)}>
          ここからやり直す
        </button>
      {/if}

    {:else if item.kind === 'agent'}
      <div class="body">
        {#if item.thinking}
          <div class="think" class:on={showThink}>
            <button class="peek" onclick={() => (openThink = !showThink)} aria-expanded={showThink}>
              {thinkLive ? '考えています' : '推論'}
            </button>
            {#if showThink}
              <pre class="mono" bind:this={thinkBox}>{item.thinking}</pre>
            {/if}
          </div>
        {/if}
        <Markdown text={item.text} />
        {#if item.error}<p class="failed">{item.error}</p>{/if}
      </div>

    {:else if item.kind === 'notice'}
      <p class="failed standalone">{item.text}</p>

    {:else if item.kind === 'tool'}
      <details {open}>
        <summary>
          <span class="dot {item.status}"></span>
          <span class="tname mono">{item.tool}</span>
          <span class="args mono">{summarizeArgs(item.args)}</span>
          {#if item.status === 'awaiting'}<span class="waiting">承認待ち</span>{/if}
          {#if item.ms}<span class="ms mono tnum">{duration(item.ms)}</span>{/if}
        </summary>
        <div class="detail">
          {#if item.args}<pre class="mono">{JSON.stringify(item.args, null, 2)}</pre>{/if}
          {#if item.result}<pre class="mono result">{item.result}</pre>{/if}
        </div>
      </details>

      {#if item.approvalId}
        <div class="approval">
          <p>
            <span class="mono">{item.tool}</span> の実行を許可しますか。許可しない場合は、
            その旨がエージェントへ伝わります。
          </p>
          <div class="acts">
            <button class="primary" onclick={() => onApprove(item.approvalId, true)}>
              許可する
            </button>
            <button onclick={() => onApprove(item.approvalId, false)}>許可しない</button>
          </div>
        </div>
      {/if}

    {:else if item.kind === 'delegate'}
      <!-- 委譲は、渡した先の色の縦線で子の会話を囲み、成果で左へ折り返す。
           経過は走っている間だけ開き、終わったら畳む。受け取った成果は
           畳まない。親の会話にとってはそれが結果そのものだからである。 -->
      <div class="dg" class:on={showDg} style:--spine={whoColor(item.agentId, colorOf)}>
        <button class="head" onclick={() => (openDg = !showDg)} aria-expanded={showDg}>
          <span class="dot {item.status}"></span>
          <span class="tname" style:color={whoColor(item.agentId, colorOf)}>{item.agentId}</span>
          <span class="to">へ委譲</span>
          <span class="args">{item.task ?? ''}</span>
          {#if item.ms}<span class="ms mono tnum">{duration(item.ms)}</span>{/if}
        </button>

        {#if showDg}
          <div class="children">
            {#each kids as child, i (child.id)}
              <Self
                item={child}
                {onApprove}
                {colorOf}
                lead={kidLeads[i]}
                owner={kidOwners[i]}
                nested
              />
            {/each}
          </div>
        {/if}

        {#if item.result}
          <div class="back">
            {#if repeats && showDg}
              <!-- 経過が見えているなら、同じ文章を続けて 2 度出さない。 -->
              <span class="label">{item.agentId} の応答はここまで</span>
            {:else}
              <span class="label">受け取った成果</span>
              <Markdown text={item.result} />
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>
</div>

<style>
  /* やり直しは、その依頼の上で手を止めたときだけ出す。常に見えていると、
     会話を読む間ずっと消す操作が視界に入る。 */
  .rewind {
    position: absolute;
    top: 0;
    right: 0;
    padding: 1px 6px;
    font-size: 11px;
    color: var(--fg-dim);
    background: var(--bg);
    opacity: 0;
    transition: opacity var(--dur) var(--ease);
  }
  .item:hover .rewind,
  .rewind:focus-visible {
    opacity: 1;
  }

  /* 推論は本文の前に置くが、地に沈めて結論より前へ出ない扱いにする。 */
  .think {
    margin: 0 0 6px;
    color: var(--fg-muted);
  }
  .peek {
    padding: 0;
    background: none;
    border: none;
    font-size: 11px;
    color: var(--fg-dim);
  }
  .peek:hover { color: var(--fg-muted); background: none; }
  .peek::before {
    content: "▸  ";
    color: var(--g9);
  }
  .think.on .peek::before { content: "▾  "; }
  .think pre {
    margin: 4px 0 0;
    padding: 8px 10px;
    /* 長い推論で本文が画面外へ押し出されないよう、高さを切って中で送る。 */
    max-height: 12rem;
    overflow-y: auto;
    background: var(--sunken);
    border-radius: var(--radius);
    white-space: pre-wrap;
    font-size: 11px;
    line-height: 1.7;
  }

  /* まとまりの中では上下の隙間を空けない。隙間があると縦線が切れて、
     ひとまとまりに見えなくなる。話し手が変わるときだけ間を空ける。 */
  .item { padding: 1px 0; }
  .item.lead { margin-top: 15px; }
  .item.lead:first-child { margin-top: 0; }

  .who {
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding-left: 13px;
    margin-bottom: 3px;
  }
  .who .name { font-size: 12px; font-weight: 600; }
  .who .time { margin-left: auto; font-size: 11px; color: var(--g9); }

  /* 生成中。その場から動かない小さな指標なので、色だけという原則の例外にする。 */
  .typing { display: inline-flex; align-items: center; gap: 3px; }
  .typing i {
    width: 3px;
    height: 3px;
    border-radius: 50%;
    background: var(--fg-muted);
    animation: blink 1.2s ease-in-out infinite;
  }
  .typing i:nth-child(2) { animation-delay: 0.15s; }
  .typing i:nth-child(3) { animation-delay: 0.3s; }
  @keyframes blink {
    0%, 60%, 100% { opacity: 0.25; }
    30% { opacity: 1; }
  }

  /* ターン全体を話し手の色で薄く括る。委譲の縦線 (濃い色) と役割が分かれる。 */
  .line {
    position: relative;
    padding-left: 11px;
    border-left: 2px solid color-mix(in srgb, var(--own) 50%, transparent);
  }
  .line.user { border-left-color: var(--own); }
  /* 委譲の中では、囲んでいる縦線が既に話し手を示している。ここで括ると
     同じことを言う線が 2 本並ぶ。 */
  .line.nested { border-left-color: transparent; }

  .body.plain { white-space: pre-wrap; overflow-wrap: anywhere; }

  .failed { margin: 6px 0 0; color: var(--danger-text); font-size: 12px; }
  .failed.standalone {
    margin: 0;
    background: var(--danger-surface);
    border: 1px solid var(--danger-border);
    border-radius: var(--radius);
    padding: 6px 10px;
  }

  summary {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    padding: 2px 6px;
    margin-left: -6px;
    border-radius: var(--radius);
    list-style: none;
    color: var(--fg-muted);
  }
  summary::-webkit-details-marker { display: none; }
  summary:hover { background: var(--g4); color: var(--fg); }

  /* 結末は色と形で示す。塗りは走らせた結果 (成功・失敗・実行中)、中抜きは
     走らずに終わったこと (拒否・中断) を表す。実行中から成功へは色が移る
     ので、終わったことがその場で分かる。 */
  .dot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 2px;
    background: var(--glyph);
    transition:
      background-color var(--dur) var(--ease),
      border-color var(--dur) var(--ease);
  }
  .dot.done { background: var(--ok); }
  .dot.error { background: var(--danger); }
  .dot.running { background: var(--accent-line); }
  .dot.awaiting { background: transparent; border: 2px solid var(--accent-line); }
  .dot.denied,
  .dot.stopped { background: transparent; border: 2px solid var(--g7); }

  .tname { color: var(--fg); flex: none; font-weight: 500; }
  .to { flex: none; }
  .args {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ms { flex: none; font-size: 11px; color: var(--g9); }
  .waiting { flex: none; color: var(--accent-line); font-size: 11px; }

  .detail { padding: 4px 0 6px 14px; display: grid; gap: 6px; }
  .detail pre {
    margin: 0;
    background: var(--sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 7px 9px;
    max-height: 22rem;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    color: var(--fg-muted);
  }
  .detail .result { color: var(--fg); }

  .approval {
    margin: 5px 0 0 14px;
    border: 1px solid var(--border-strong);
    border-left: 2px solid var(--accent-line);
    background: var(--g4);
    border-radius: var(--radius);
    padding: 9px 11px;
  }
  .approval p { margin: 0 0 8px; }
  .approval .acts { display: flex; gap: 6px; }

  /* 委譲。上下の折れは、2 辺の境界と 1 つの丸みを持つ擬似要素で描く。
     畳んでいるときは行き先が無いので折れを出さない。 */
  .dg { position: relative; }
  /* 委譲の見出しは、推論より強く出す。渡した先が誰かは会話の筋に関わるので、
     畳んでいても読み飛ばせないようにする。 */
  .dg .head {
    width: 100%;
    padding-left: 19px;
    margin-left: 0;
    background: none;
    border: none;
    text-align: left;
  }
  .dg .head:hover { background: var(--control); }
  .dg.on > .head::before {
    content: '';
    position: absolute;
    left: 0;
    top: 11px;
    width: 10px;
    height: 12px;
    border-left: 2px solid var(--spine);
    border-top: 2px solid var(--spine);
    border-top-left-radius: 7px;
  }
  .children { border-left: 2px solid var(--spine); padding-left: 11px; }
  .back { position: relative; padding-left: 13px; padding-top: 3px; }
  /* 畳んでいるときは繋ぐ先の縦線が無い。折り返しの印だけが宙に浮くので出さない。 */
  .dg:not(.on) .back { padding-left: 19px; }
  .dg:not(.on) .back::before { display: none; }
  .back::before {
    content: '';
    position: absolute;
    left: 0;
    top: -6px;
    width: 8px;
    height: 14px;
    border-left: 2px solid var(--spine);
    border-bottom: 2px solid var(--spine);
    border-bottom-left-radius: 7px;
  }
  .back .label {
    display: block;
    font-size: 11px;
    color: var(--fg-muted);
    margin-bottom: 2px;
  }
</style>
