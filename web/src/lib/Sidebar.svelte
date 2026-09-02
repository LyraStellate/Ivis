<script>
  let { sessions, agents, status, currentId, onOpen, onNew, onDelete, onReload, onSettings } = $props()

  let newAgent = $state('')

  $effect(() => {
    if (!newAgent && status?.default_agent) newAgent = status.default_agent
  })

  const problems = $derived(
    (status?.agent_errors?.length ?? 0) +
      (status?.skill_errors?.length ?? 0) +
      (status?.skill_conflicts?.length ?? 0),
  )
</script>

<aside>
  <header>
    <span class="brand">Ivis</span>
    <span class="dot" class:ok={status?.provider_ok} title={status?.provider_ok ? 'Ollama に接続できています' : status?.provider_error ?? '接続状態は不明です'}></span>
  </header>

  <div class="new">
    <select bind:value={newAgent} aria-label="エージェント">
      {#each agents as a (a.id)}
        <option value={a.id}>{a.name}</option>
      {/each}
    </select>
    <button onclick={() => onNew(newAgent)} disabled={agents.length === 0}>新しい会話</button>
  </div>

  <nav>
    {#each sessions as s (s.id)}
      <div class="row" class:active={s.id === currentId}>
        <button class="open" onclick={() => onOpen(s.id)}>
          <span class="title">{s.title}</span>
          <span class="agent">{s.agent_id}</span>
        </button>
        <button class="del" title="削除" onclick={() => onDelete(s.id)}>×</button>
      </div>
    {:else}
      <p class="hint">まだ会話がありません。</p>
    {/each}
  </nav>

  <footer>
    {#if problems > 0}
      <p class="warn">{problems} 件の読み込み問題があります(設定で確認)</p>
    {/if}
    <div class="acts">
      <button onclick={onReload}>再読込</button>
      <button onclick={onSettings}>設定</button>
    </div>
  </footer>
</aside>

<style>
  aside {
    display: flex;
    flex-direction: column;
    background: var(--panel);
    border-right: 1px solid var(--line);
    min-height: 0;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.9rem 1rem 0.6rem;
  }
  .brand {
    font-weight: 600;
    letter-spacing: 0.14em;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--danger);
  }
  .dot.ok { background: var(--ok); }

  .new {
    display: grid;
    gap: 0.4rem;
    padding: 0 0.75rem 0.75rem;
    border-bottom: 1px solid var(--line);
  }

  nav {
    flex: 1;
    overflow-y: auto;
    padding: 0.5rem;
    min-height: 0;
  }
  .row {
    display: flex;
    align-items: stretch;
    gap: 2px;
    margin-bottom: 2px;
  }
  .row.active .open { background: var(--panel-2); border-color: var(--accent); }
  .open {
    flex: 1;
    min-width: 0;
    text-align: left;
    background: transparent;
    border-color: transparent;
    display: grid;
  }
  .title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .agent {
    font-size: 0.78em;
    color: var(--fg-dim);
  }
  .del {
    background: transparent;
    border-color: transparent;
    color: var(--fg-dim);
    padding: 0 0.5rem;
  }
  .del:hover { color: var(--danger); }

  .hint, .warn {
    color: var(--fg-dim);
    font-size: 0.85em;
    padding: 0 0.5rem;
  }
  .warn { color: var(--danger); }

  footer {
    border-top: 1px solid var(--line);
    padding: 0.6rem 0.75rem;
  }
  .acts {
    display: flex;
    gap: 0.4rem;
  }
  .acts button { flex: 1; }
</style>
