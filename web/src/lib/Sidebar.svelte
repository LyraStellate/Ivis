<script>
  let { sessions, agents, status, currentId, busy, onOpen, onNew, onDelete, onReload, onSettings } =
    $props()

  let listEl = $state(null)

  const agentName = (id) => agents.find((a) => a.id === id)?.name ?? id

  const problems = $derived(
    (status?.agent_errors?.length ?? 0) +
      (status?.skill_errors?.length ?? 0) +
      (status?.skill_conflicts?.length ?? 0),
  )

  // 上下キーで行を移動する。Enter と Space は button の既定の動作に任せる。
  function onListKeydown(e) {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    const rows = [...listEl.querySelectorAll('button.open')]
    const at = rows.indexOf(document.activeElement)
    const next = e.key === 'ArrowDown' ? at + 1 : at - 1
    if (next < 0 || next >= rows.length) return
    rows[next].focus()
    e.preventDefault()
  }
</script>

<aside>
  <header>
    <span class="brand">IVIS</span>
    <span class="state" class:ok={status?.provider_ok}>
      <span class="dot"></span>
      {status?.provider_ok ? 'ollama' : '未接続'}
    </span>
  </header>

  <div class="list" bind:this={listEl}>
    {#each sessions as s (s.id)}
      <div class="row" class:current={s.id === currentId}>
        <button
          class="open"
          onclick={() => onOpen(s.id)}
          onkeydown={onListKeydown}
          disabled={busy && s.id !== currentId}
        >
          <span class="name">{s.title}</span>
          <span class="agent">{agentName(s.agent_id)}</span>
        </button>
        <button class="del quiet" title="この会話を削除" onclick={() => onDelete(s)}>×</button>
      </div>
    {:else}
      <p class="hint">まだ会話がありません。</p>
    {/each}
  </div>

  <footer>
    {#if problems > 0}
      <button class="issues quiet" onclick={onSettings}>
        読み込みの問題 {problems} 件
      </button>
    {/if}
    <button class="new" onclick={() => onNew()} disabled={agents.length === 0}>
      新しい会話
    </button>
    <div class="acts">
      <button class="quiet" onclick={onReload}>再読込</button>
      <button class="quiet" onclick={onSettings}>設定</button>
    </div>
  </footer>
</aside>

<style>
  aside {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
    background: var(--surface);
    border-right: 1px solid var(--border);
  }

  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 12px;
    border-bottom: 1px solid var(--border);
  }
  .brand {
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 0.22em;
    color: var(--fg);
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 11px;
    color: var(--fg-muted);
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--danger);
  }
  .state.ok .dot {
    background: var(--ok);
  }

  .list {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 6px;
  }

  /* 通常・hover・選択を別々の段で表す。選択は面の色だけでなく左の帯でも示す。 */
  .row {
    display: flex;
    align-items: stretch;
    border-radius: var(--radius);
    border-left: 2px solid transparent;
    transition: background-color var(--dur) var(--ease), border-color var(--dur) var(--ease);
  }
  .row:hover {
    background: var(--control-hover);
  }
  .row.current {
    background: var(--control-active);
    border-left-color: var(--accent);
  }

  .open {
    flex: 1;
    min-width: 0;
    display: grid;
    gap: 1px;
    text-align: left;
    background: transparent;
    border-color: transparent;
    padding: 5px 8px;
  }
  .open:hover:not(:disabled) {
    background: transparent;
    border-color: transparent;
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .agent {
    font-size: 11px;
    color: var(--fg-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .del {
    align-self: center;
    padding: 0 7px;
    font-size: 14px;
    line-height: 1;
    opacity: 0;
  }
  .row:hover .del,
  .del:focus-visible {
    opacity: 1;
  }
  .del:hover {
    color: var(--danger-text);
    background: var(--danger-surface);
  }

  .hint {
    color: var(--fg-muted);
    font-size: 12px;
    padding: 6px 8px;
  }

  footer {
    border-top: 1px solid var(--border);
    padding: 8px;
    display: grid;
    gap: 6px;
  }
  .new {
    width: 100%;
  }
  .acts {
    display: flex;
    gap: 6px;
  }
  .acts button {
    flex: 1;
  }
  .issues {
    color: var(--danger-text);
    font-size: 11px;
    text-align: left;
  }
  .issues:hover {
    background: var(--danger-surface);
    color: var(--danger-text);
  }
</style>
