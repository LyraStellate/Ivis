<script>
  import Item from './Item.svelte'
  import Composer from './Composer.svelte'
  import { stickToBottom, scrollToBottom } from './stick.js'
  import { leads, owners } from './group.js'

  let {
    session, agents, items, busy, notice, status, railHidden, onToggleRail,
    onSend, onCancel, onApprove, onAnswer, onAgentChange, onDismiss, onRewind, colorOf, usage,
    draftBack,
  } = $props()

  let scroller = $state(null)
  let pinned = $state(true)

  const agent = $derived(agents.find((a) => a.id === session?.agent_id) ?? null)
  const missingAgent = $derived(session != null && agent == null)
  const providerDown = $derived(status != null && !status.provider_ok)

  // Discord の会話は画面からは進まない。送れても、その内容はチャンネルに
  // 出ないので、次にそこで話す人は知らない文脈の続きを読むことになる。
  const fromDiscord = $derived(session?.source === 'discord')

  // 続けて同じ話し手が話す間は名前を出し直さない。誰の作業かは色で示す。
  const itemLeads = $derived(leads(items))
  const itemOwners = $derived(owners(items))

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
</header>

{#if missingAgent}
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
  </div>
</div>

{#if !pinned}
  <div class="catch-up">
    <button onclick={() => scrollToBottom(scroller)}>最新へ</button>
  </div>
{/if}

<Composer
  disabled={missingAgent || fromDiscord}
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
