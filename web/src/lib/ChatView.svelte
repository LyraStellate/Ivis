<script>
  import Item from './Item.svelte'
  import Composer from './Composer.svelte'
  import { stickToBottom, scrollToBottom } from './stick.js'

  let {
    session, agents, items, busy, notice, status, railHidden, onToggleRail,
    onSend, onCancel, onApprove, onAgentChange, onDismiss,
  } = $props()

  let scroller = $state(null)
  let pinned = $state(true)

  const agent = $derived(agents.find((a) => a.id === session?.agent_id) ?? null)
  const missingAgent = $derived(session != null && agent == null)
  const providerDown = $derived(status != null && !status.provider_ok)

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
    tools_unsupported: 'このモデルではスキルの読み込みと委譲は使えません。別のモデルを選んでください。',
    not_found: '一覧を再読込してください。',
  }
</script>

<header>
  <button
    class="rail quiet"
    onclick={onToggleRail}
    title={railHidden ? '一覧を表示' : '一覧を隠す'}
    aria-label={railHidden ? '一覧を表示' : '一覧を隠す'}
  >
    {railHidden ? '»' : '«'}
  </button>
  <div class="title">{session?.title ?? ''}</div>

  <select
    value={session?.agent_id}
    onchange={(e) => onAgentChange(e.currentTarget.value)}
    disabled={busy}
    aria-label="エージェント"
  >
    {#each agents as a (a.id)}
      <option value={a.id}>{a.name}</option>
    {/each}
  </select>

  <span class="model mono">{agent?.model ?? '—'}</span>

  {#if busy}
    <button onclick={onCancel}>中断 <kbd>Esc</kbd></button>
  {/if}
</header>

{#if missingAgent}
  <p class="banner">
    このセッションのエージェント <span class="mono">{session.agent_id}</span> の定義が見つかりません。
    履歴は読めますが、続きは送れません。定義を戻すか、上のエージェントを選び直してください。
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
    {#each items as item (item.id)}
      <Item {item} {onApprove} />
    {/each}
  </div>
</div>

{#if !pinned}
  <div class="catch-up">
    <button onclick={() => scrollToBottom(scroller)}>最新へ</button>
  </div>
{/if}

<Composer disabled={busy || missingAgent} {busy} {onSend} />

<style>
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 14px;
    border-bottom: 1px solid var(--border);
  }
  .rail {
    flex: none;
    padding: 2px 7px;
    font-size: 13px;
    line-height: 1.2;
  }
  .title {
    flex: 1;
    min-width: 0;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  header select {
    width: auto;
    min-width: 9rem;
  }
  .model {
    color: var(--fg-muted);
  }
  kbd {
    font: 11px var(--mono);
    color: var(--fg-muted);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0 3px;
    margin-left: 3px;
  }

  .banner {
    margin: 0;
    padding: 7px 14px;
    background: var(--danger-surface);
    border-bottom: 1px solid var(--e6);
    color: var(--danger-text);
    font-size: 12px;
  }

  .notice {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 14px;
    background: var(--danger-surface);
    border-bottom: 1px solid var(--e6);
    color: var(--danger-text);
  }
  .notice .remedy {
    color: var(--fg-muted);
    margin-left: 8px;
  }
  .notice button {
    margin-left: auto;
    flex: none;
  }

  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-anchor: none;
  }
  .stream {
    max-width: 880px;
    margin: 0 auto;
    padding: 14px 14px 22px;
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
    background: var(--control-active);
    border-color: var(--border-hover);
    box-shadow: 0 2px 10px rgb(0 0 0 / 0.4);
  }
</style>
