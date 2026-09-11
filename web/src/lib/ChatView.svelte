<script>
  import Item from './Item.svelte'
  import Composer from './Composer.svelte'
  import { stickToBottom, scrollToBottom } from './stick.js'
  import { leads, owners } from './group.js'

  import { elapsed } from './format.js'

  let {
    session, agents: allAgents, items, busy, moved = 0, stage = null, commands = [],
    members = [], isTeam = false, roster = null,
    // panelHidden は null なら右のパネルそのものが無い会話 (直列)。
    panelHidden = null, onTogglePanel = null,
    notice, status, railHidden, onToggleRail,
    onSend, onCancel, onApprove, onAnswer, onAgentChange, onDismiss, onRewind, colorOf, usage,
    draftBack,
  } = $props()

  let scroller = $state(null)
  let pinned = $state(true)

  const agents = $derived(allAgents)

  // 続けられない理由。直列では答え手の定義が消えたとき、チームでは名簿が
  // 空になったときである。
  //
  // チームで session.agent_id を見てはいけない。あれは会話を作ったときの
  // 記録であって、実行では読まない値になった。見ると、いつでも「居ない」に
  // なって送信が止まる (#640275)。
  //
  // 名簿が届く前に「居ない」と言わないよう、roster が来るまでは黙る。
  const agent = $derived(allAgents.find((a) => a.id === session?.agent_id) ?? null)
  const missingAgent = $derived(session != null && !isTeam && agent == null)
  const emptyTeam = $derived(isTeam && roster != null && members.length === 0)
  const providerDown = $derived(status != null && !status.provider_ok)

  // Discord の会話は画面からは進まない。送れても、その内容はチャンネルに
  // 出ないので、次にそこで話す人は知らないやり取りの続きを読むことになる。
  const fromDiscord = $derived(session?.source === 'discord')

  // 続けて同じ話し手が話す間は名前を出し直さない。誰の作業かは色で示す。
  const itemLeads = $derived(leads(items))
  const itemOwners = $derived(owners(items))

  // 何も届かない時間が続いていることを、末尾に 1 行だけ出す。
  //
  // 無音になる場所は決まっていない。道具を続けて呼ぶ間、次の生成が始まるまで、
  // モデルが道具の引数を書いている間 — どれも画面に該当する項目が無いか、
  // あっても空である。発言や道具の行に付けて回るのではなく、届いたかどうかで
  // 測って 1 か所に出す。動いている間 (ふつうの生成は 1 秒も途切れない) は
  // 出ない。
  const QUIET = 2000
  let now = $state(Date.now())
  $effect(() => {
    if (!busy) return
    now = Date.now()
    const id = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(id)
  })
  // 承認や問いを待っている間は数えない。止まっているのではなく、利用者の番で
  // ある。そこで秒数を出しても急かしているだけになる。
  const stalled = $derived(
    busy && stage != null && moved > 0 && now - moved >= QUIET ? elapsed(now - moved) : '',
  )

  // 上の帯が同じことを伝えている場合は、失敗の通知を重ねて出さない。
  // 同じ内容が 2 か所に出ると、どちらを読めばよいか分からなくなる。
  const showNotice = $derived(
    notice != null && !(providerDown && notice.kind === 'provider_unavailable'),
  )

  // 失敗の種類ごとに、次に取るべき行動を添える。種類を無視して同じ文言に
  // すると、提供元の起動忘れなのか設定の誤りなのか判断できない。
  const remedy = {
    provider_unavailable: 'Ollama を起動してから、もう一度送ってください。',
    model_not_found: 'ollama pull でモデルを取得してください。',
    tools_unsupported:
      'このモデルではスキルの読み込みと委譲は使えません。別のモデルを選んでください。',
    not_found: '一覧を再読込してください。',
  }
</script>

<header>
  <button
    class="rail quiet"
    onclick={onToggleRail}
    title={railHidden ? '会話の一覧を開く' : '会話の一覧を閉じる'}
    aria-label={railHidden ? '会話の一覧を開く' : '会話の一覧を閉じる'}
  >
    <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
      <path
        d={railHidden ? 'M4.5 2 L8.5 6 L4.5 10' : 'M7.5 2 L3.5 6 L7.5 10'}
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  </button>

  <h1 class="title">{session?.title ?? ''}</h1>

  <!-- この会話のファイルがどこに落ちるか。会話ごとに場所が違うので、
       どこを見ればよいか分からないままになる。 -->
  {#if session?.workspace}
    <span class="place mono" title={session.workspace}>{session.workspace}</span>
  {/if}

  {#if panelHidden != null}
    <button
      class="rail quiet"
      onclick={onTogglePanel}
      title={panelHidden ? 'メンバーとチケットを開く' : 'メンバーとチケットを閉じる'}
      aria-label={panelHidden ? 'メンバーとチケットを開く' : 'メンバーとチケットを閉じる'}
    >
      <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
        <path
          d={panelHidden ? 'M7.5 2 L3.5 6 L7.5 10' : 'M4.5 2 L8.5 6 L4.5 10'}
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
    </button>
  {/if}
</header>

{#if emptyTeam}
  <p class="banner">
    この会話にはメンバーが 1 人も居ません。右のパネルから足してください。
  </p>
{:else if missingAgent}
  <p class="banner">
    このセッションのエージェント <span class="mono">{session.agent_id}</span> の定義が
    見つかりません。履歴は読めますが、続きは送れません。定義を戻すか、入力欄で
    エージェントを選び直してください。
  </p>
{:else if providerDown}
  <p class="banner">Ollama に接続できていません。{remedy.provider_unavailable}</p>
{/if}

{#if showNotice}
  <div class="notice" role="alert">
    <div>
      <span>{notice.text}</span>
      {#if remedy[notice.kind]}<span class="remedy">{remedy[notice.kind]}</span>{/if}
    </div>
    <button class="quiet" onclick={onDismiss}>閉じる</button>
  </div>
{/if}

<div class="scroll" bind:this={scroller} use:stickToBottom={{ onPinned: (p) => (pinned = p) }}>
  <div class="stream">
    {#each items as item, i (item.id)}
      <Item
        {item}
        {onApprove}
        {onAnswer}
        {colorOf}
        onRewind={busy ? null : onRewind}
        lead={itemLeads[i]}
        owner={itemOwners[i]}
      />
    {/each}

    {#if stalled}
      <p class="stalled" role="status">
        <span class="typing"><i></i><i></i><i></i></span>
        <span>{stage.label}</span>
        <!-- 割合は出せない。要約が何文字で終わるかは、書き終わるまで誰にも
             分からない。分母を作って割ると、進んでいるように見えるだけの
             数字になる。できた量をそのまま出す。 -->
        {#if stage.done}
          <span class="mono tnum">{stage.done.toLocaleString()}{stage.unit ?? ''}</span>
        {/if}
        <span class="mono tnum">{stalled}</span>
      </p>
    {/if}
  </div>
</div>

{#if !pinned}
  <div class="catch-up">
    <button onclick={() => scrollToBottom(scroller)}>最新へ</button>
  </div>
{/if}

<Composer
  {commands}
  {members}
  showAgents={!isTeam}
  disabled={missingAgent || emptyTeam || fromDiscord}
  reason={fromDiscord ? 'この会話は Discord から進みます。ここからは読むだけです' : ''}
  {busy}
  {agents}
  agentId={session?.agent_id ?? ''}
  {usage}
  {draftBack}
  {onSend}
  {onCancel}
  {onAgentChange}
/>

<style>
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 12px;
    height: 40px;
    flex: none;
    border-bottom: 1px solid var(--border);
  }
  .rail {
    flex: none;
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    padding: 0;
    color: var(--fg-dim);
  }
  /* 場所は題名を押しのけない。長いので末尾ではなく先頭を削る。 */
  .place {
    flex: 0 1 auto;
    min-width: 0;
    font-size: 11px;
    color: var(--fg-dim);
    direction: rtl;
    text-align: left;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .title {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    color: var(--fg-bright);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .banner,
  .notice {
    flex: none;
    margin: 0;
    padding: 7px 14px;
    background: var(--danger-surface);
    border-bottom: 1px solid var(--danger-border);
    color: var(--danger-text);
    font-size: 12px;
  }
  .notice {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 13px;
  }
  .notice .remedy { color: var(--fg-muted); margin-left: 8px; }
  .notice button { margin-left: auto; flex: none; }

  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-anchor: none;
  }
  .stream {
    max-width: 880px;
    margin: 0 auto;
    padding: 16px 14px 24px;
  }

  /* 止まっている間だけ出る行。発言と同じ重さで置くと、新しい何かが来たように
     見える。小さく淡く、末尾に添えるだけにする。 */
  .stalled {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 6px 0 0;
    padding-left: 13px;
    color: var(--g9);
    font-size: 11px;
  }
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

  .catch-up {
    position: relative;
    height: 0;
    text-align: center;
  }
  .catch-up button {
    position: absolute;
    left: 50%;
    bottom: 8px;
    transform: translateX(-50%);
    background: var(--g5);
    border-color: var(--border-hover);
    box-shadow: 0 2px 12px rgb(0 0 0 / 0.5);
  }
</style>
