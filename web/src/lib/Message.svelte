<script>
  // Svelte 5 では svelte:self ではなく自分自身を import して入れ子にする。
  import Self from './Message.svelte'

  // item は確定した履歴 (role を持つ) か、生成中の途中経過 (kind を持つ) の
  // どちらか。表示上の種別は kind に寄せてある。
  let { item, nested = false } = $props()

  const kind = $derived(item.kind ?? item.role)
  const label = $derived(
    {
      user: 'あなた',
      assistant: item.agent_id ?? item.agent ?? 'assistant',
      tool: item.tool_name ?? 'ツール',
      tool_call: `${item.tool} を呼び出し`,
      tool_result: `${item.tool} の結果`,
      delegate: `${item.agent_id ?? item.agent} へ委譲`,
      delegate_start: `${item.agent} へ委譲`,
      delegate_end: `${item.agent} からの結果`,
    }[kind] ?? kind,
  )

  const body = $derived(item.text ?? item.content ?? '')
  const collapsible = $derived(
    ['tool', 'tool_call', 'tool_result', 'delegate', 'delegate_start', 'delegate_end'].includes(kind),
  )
</script>

<article class="msg {kind}" class:nested>
  {#if kind === 'user'}
    <div class="bubble user">{body}</div>
  {:else if kind === 'assistant'}
    <div class="who">{label}{#if item.error}<span class="err"> — {item.error}</span>{/if}</div>
    <div class="bubble">{body}</div>
  {:else if collapsible}
    <details>
      <summary>{label}</summary>
      {#if item.args}
        <pre><code>{JSON.stringify(item.args, null, 2)}</code></pre>
      {/if}
      {#if body}
        <pre><code>{body}</code></pre>
      {/if}
      {#if item.children?.length}
        <div class="children">
          {#each item.children as child (child.id)}
            <Self item={{ ...child, kind: child.role }} nested={true} />
          {/each}
        </div>
      {/if}
    </details>
  {:else}
    <div class="who">{label}</div>
    <div class="bubble">{body}</div>
  {/if}
</article>

<style>
  .msg { margin-bottom: 0.9rem; }
  .msg.nested { margin-bottom: 0.5rem; }

  .who {
    font-size: 0.78em;
    color: var(--fg-dim);
    margin-bottom: 0.2rem;
  }
  .err { color: var(--danger); }

  .bubble {
    white-space: pre-wrap;
    word-break: break-word;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 0.6rem 0.85rem;
  }
  .bubble.user {
    background: var(--panel-2);
    border-color: #3a4353;
    margin-left: 15%;
  }

  details {
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 0.4rem 0.7rem;
    background: var(--panel);
  }
  summary {
    cursor: pointer;
    color: var(--fg-dim);
    font-size: 0.85em;
  }
  .children {
    margin-top: 0.5rem;
    padding-left: 0.75rem;
    border-left: 2px solid var(--line);
  }
</style>
