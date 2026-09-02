<script>
  // 発言 1 件を描く。委譲は子を抱えるので自分自身を入れ子に使う。
  import Self from './Item.svelte'
  import Markdown from './Markdown.svelte'
  import { clock, duration, summarizeArgs } from './format.js'

  let { item, onApprove, nested = false } = $props()

  const mark = { running: '', awaiting: '', done: '✓', error: '✕', denied: '—', stopped: '—' }
  const open = $derived(item.status === 'error' || item.status === 'awaiting')
</script>

{#if item.kind === 'user'}
  <article class="turn user">
    <div class="who">
      <span class="name">you</span>
      {#if item.time}<span class="time">{clock(item.time)}</span>{/if}
    </div>
    <div class="body plain">{item.text}</div>
  </article>

{:else if item.kind === 'agent'}
  <article class="turn agent">
    <div class="who">
      <span class="name">{item.agentId ?? ''}</span>
      {#if item.status === 'streaming'}<span class="live">生成中</span>{/if}
      {#if item.time}<span class="time">{clock(item.time)}</span>{/if}
    </div>
    <div class="body">
      <Markdown text={item.text} />
      {#if item.error}
        <p class="failed">{item.error}</p>
      {/if}
    </div>
  </article>

{:else if item.kind === 'notice'}
  <p class="failed standalone">{item.text}</p>

{:else if item.kind === 'tool'}
  <article class="tool">
    <details {open}>
      <summary>
        <span class="glyph {item.status}">{mark[item.status] ?? ''}</span>
        <span class="tname mono">{item.tool}</span>
        <span class="args mono">{summarizeArgs(item.args)}</span>
        {#if item.ms}<span class="ms mono">{duration(item.ms)}</span>{/if}
        {#if item.status === 'awaiting'}<span class="waiting">承認待ち</span>{/if}
      </summary>
      <div class="detail">
        {#if item.args}
          <pre class="mono">{JSON.stringify(item.args, null, 2)}</pre>
        {/if}
        {#if item.result}
          <pre class="mono result">{item.result}</pre>
        {/if}
      </div>
    </details>

    {#if item.approvalId}
      <div class="approval">
        <p>
          <span class="mono">{item.tool}</span> の実行を許可しますか。許可しない場合は、その旨が
          エージェントへ伝わります。
        </p>
        <div class="acts">
          <button class="primary" onclick={() => onApprove(item.approvalId, true)}>許可する</button>
          <button onclick={() => onApprove(item.approvalId, false)}>許可しない</button>
        </div>
      </div>
    {/if}
  </article>

{:else if item.kind === 'delegate'}
  <article class="delegate">
    <details open={item.status === 'running' || item.status === 'error'}>
      <summary>
        <span class="glyph {item.status}">{mark[item.status] ?? ''}</span>
        <span class="tname">{item.agentId} へ委譲</span>
        <span class="args">{item.task ?? ''}</span>
      </summary>
      <div class="children">
        {#each item.children ?? [] as child (child.id)}
          <Self item={child} {onApprove} nested={true} />
        {/each}
        {#if item.result}
          <div class="handback">
            <span class="label">受け取った成果</span>
            <Markdown text={item.result} />
          </div>
        {/if}
      </div>
    </details>
  </article>
{/if}

<style>
  article {
    margin-bottom: 10px;
  }

  /* 発言はどれも「話し手の行」と「本文」の 2 段に揃える。吹き出しを使わず、
     並びを一定にすることで、長い履歴でも目が迷わない。 */
  .turn {
    margin-bottom: 14px;
  }
  .who {
    display: flex;
    align-items: baseline;
    gap: 8px;
    margin-bottom: 2px;
  }
  .who .name {
    font-size: 11px;
    font-weight: 600;
    color: var(--fg-muted);
  }
  .user .who .name {
    color: var(--accent-text);
  }
  .who .time {
    margin-left: auto;
    font-size: 11px;
    color: var(--n9);
  }
  .who .live {
    font-size: 11px;
    color: var(--accent-text);
  }

  .body {
    padding-left: 11px;
    border-left: 2px solid transparent;
  }
  .user .body {
    border-left-color: var(--a7);
  }
  .body.plain {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .failed {
    margin: 6px 0 0;
    color: var(--danger-text);
    font-size: 12px;
  }
  .failed.standalone {
    background: var(--danger-surface);
    border: 1px solid var(--e6);
    border-radius: var(--radius);
    padding: 6px 10px;
  }

  details {
    border-radius: var(--radius);
  }
  summary {
    display: flex;
    align-items: baseline;
    gap: 8px;
    cursor: pointer;
    padding: 3px 6px;
    border-radius: var(--radius);
    list-style: none;
    color: var(--fg-muted);
    transition: background-color var(--dur) var(--ease);
  }
  summary::-webkit-details-marker { display: none; }
  summary:hover { background: var(--control-hover); }

  .glyph {
    width: 1em;
    flex: none;
    text-align: center;
    font-size: 11px;
  }
  .glyph.done { color: var(--ok); }
  .glyph.error { color: var(--danger); }
  .glyph.denied,
  .glyph.stopped { color: var(--fg-muted); }
  .glyph.running::after,
  .glyph.awaiting::after {
    content: '·';
    color: var(--accent-text);
  }

  .tname { color: var(--fg); flex: none; }
  .args {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ms { flex: none; font-size: 11px; }
  .waiting { flex: none; color: var(--accent-text); font-size: 11px; }

  .detail {
    padding: 4px 6px 6px 28px;
    display: grid;
    gap: 6px;
  }
  .detail pre {
    margin: 0;
    background: var(--n1);
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
    margin: 4px 0 0 28px;
    border: 1px solid var(--a7);
    background: var(--a3);
    border-radius: var(--radius);
    padding: 8px 11px;
  }
  .approval p { margin: 0 0 7px; }
  .approval .acts { display: flex; gap: 6px; }

  .children {
    margin: 4px 0 0 12px;
    padding-left: 12px;
    border-left: 2px solid var(--border);
  }
  .handback .label {
    display: block;
    font-size: 11px;
    color: var(--fg-muted);
    margin-bottom: 2px;
  }
</style>
