<script>
  import Message from './Message.svelte'

  let { session, agents, messages, live, busy, approval, notice,
        onSend, onCancel, onApprove, onAgentChange, onDismiss } = $props()

  let draft = $state('')
  let scroller = $state(null)

  // 表示は「確定した履歴」か「生成中の途中経過」のどちらか。混ぜると
  // 同じ発言が二重に出る。
  const shown = $derived(live.length > 0 ? live : toItems(messages))

  function toItems(list) {
    const children = new Map()
    for (const m of list) {
      if (!m.parent_id) continue
      if (!children.has(m.parent_id)) children.set(m.parent_id, [])
      children.get(m.parent_id).push(m)
    }
    return list
      .filter((m) => !m.parent_id)
      .map((m) => ({ ...m, kind: m.role, children: children.get(m.id) ?? [] }))
  }

  $effect(() => {
    // 依存を明示するために参照する。新しい内容が来たら末尾へ送る。
    shown.length
    if (scroller) scroller.scrollTop = scroller.scrollHeight
  })

  function submit(e) {
    e.preventDefault()
    const text = draft.trim()
    if (!text || busy) return
    draft = ''
    onSend(text)
  }

  function keydown(e) {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) submit(e)
  }
</script>

<header>
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
</header>

{#if notice}
  <div class="notice" role="alert">
    <span>{notice.text}</span>
    {#if notice.kind === 'provider_unavailable'}
      <span class="how">Ollama を起動してから再送してください。</span>
    {:else if notice.kind === 'model_not_found'}
      <span class="how">ollama pull でモデルを取得してください。</span>
    {:else if notice.kind === 'tools_unsupported'}
      <span class="how">このモデルではスキルの読み込みと委譲は動きません。</span>
    {/if}
    <button onclick={onDismiss}>閉じる</button>
  </div>
{/if}

<div class="scroll" bind:this={scroller}>
  {#each shown as item, i (item.id ?? i)}
    <Message {item} />
  {/each}

  {#if approval}
    <div class="approval">
      <p><strong>{approval.tool}</strong> の実行を許可しますか?</p>
      <pre><code>{JSON.stringify(approval.arguments, null, 2)}</code></pre>
      <div class="acts">
        <button onclick={() => onApprove(true)}>許可</button>
        <button onclick={() => onApprove(false)}>拒否</button>
      </div>
    </div>
  {/if}
</div>

<form onsubmit={submit}>
  <textarea
    bind:value={draft}
    onkeydown={keydown}
    rows="3"
    placeholder="メッセージを入力 (Ctrl+Enter で送信)"
    disabled={busy}
  ></textarea>
  <div class="acts">
    {#if busy}
      <button type="button" onclick={onCancel}>中断</button>
    {:else}
      <button type="submit" disabled={!draft.trim()}>送信</button>
    {/if}
  </div>
</form>

<style>
  header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.7rem 1rem;
    border-bottom: 1px solid var(--line);
  }
  .title {
    flex: 1;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  header select { width: auto; min-width: 10rem; }

  .notice {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.5rem 1rem;
    background: #3a2220;
    border-bottom: 1px solid var(--line);
    color: #ffd9d3;
  }
  .notice .how { color: var(--fg-dim); }
  .notice button { margin-left: auto; }

  .scroll {
    flex: 1;
    overflow-y: auto;
    padding: 1rem;
    min-height: 0;
  }

  .approval {
    border: 1px solid var(--accent);
    border-radius: 8px;
    padding: 0.75rem 1rem;
    margin: 0.5rem 0;
    background: var(--panel);
  }
  .approval p { margin: 0 0 0.4rem; }
  .acts { display: flex; gap: 0.5rem; margin-top: 0.5rem; }

  form {
    border-top: 1px solid var(--line);
    padding: 0.75rem 1rem;
    display: grid;
    gap: 0.5rem;
  }
  textarea { resize: vertical; }
  form .acts { justify-content: flex-end; margin: 0; }
</style>
