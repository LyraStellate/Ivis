<script>
  import { byBucket } from './format.js'

  let { sessions, agents, status, currentId, busy, onOpen, onNew, onDelete, onReload, onSettings } =
    $props()

  let listEl = $state(null)

  const agentName = (id) => agents.find((a) => a.id === id)?.name ?? id

  // 一覧は更新の新しい順に届く。同じ区分が続く間をひとまとまりにする。
  const groups = $derived(byBucket(sessions))

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
    <span class="brand">ivis</span>
    <span class="state" class:ok={status?.provider_ok}>
      <span class="dot"></span>
      {status?.provider_ok ? 'ollama' : '未接続'}
    </span>
  </header>

  <div class="list" bind:this={listEl}>
    {#each groups as g (g.label)}
      <p class="bucket">{g.label}</p>
      {#each g.items as s (s.id)}
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
          <button class="del quiet" title="この会話を削除" onclick={() => onDelete(s)}>
            <svg viewBox="0 0 12 12" width="11" height="11" aria-hidden="true">
              <path
                d="M3 3 L9 9 M9 3 L3 9"
                fill="none"
                stroke="currentColor"
                stroke-width="1.6"
                stroke-linecap="round"
              />
            </svg>
          </button>
        </div>
      {/each}
    {:else}
      <p class="hint">会話はまだありません</p>
    {/each}
  </div>

  <footer>
    {#if problems > 0}
      <button class="issues quiet" onclick={onSettings}>
        読み込めなかった定義が {problems} 件
      </button>
    {/if}
    <button class="new" onclick={() => onNew()} disabled={agents.length === 0}>新しい会話</button>
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
    background: var(--rail);
  }

  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 12px;
    height: 40px;
    flex: none;
    border-bottom: 1px solid var(--border);
  }
  .brand {
    font-size: 13px;
    font-weight: 600;
    color: var(--fg-bright);
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 11px;
    color: var(--fg-dim);
  }
  .state .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--danger);
  }
  .state.ok .dot { background: var(--ok); }

  .list {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 6px;
  }

  .bucket {
    margin: 10px 0 3px;
    padding: 0 8px;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    color: var(--fg-dim);
  }
  .bucket:first-child { margin-top: 2px; }

  /* 通常・hover・選択を、面と文字の明るさの 2 つで分ける。
     文字が 2 つ目の手段になるので、左端に帯を足さなくても区別が付く。 */
  .row {
    display: flex;
    align-items: stretch;
    border-radius: var(--radius);
  }
  .row:hover { background: var(--g4); }
  .row.current { background: var(--g5); }

  .open {
    flex: 1;
    min-width: 0;
    display: grid;
    gap: 1px;
    text-align: left;
    background: transparent;
    border-color: transparent;
    padding: 4px 8px;
    color: var(--fg-dim);
  }
  .open:hover:not(:disabled) {
    background: transparent;
    border-color: transparent;
    color: var(--fg);
  }
  .row.current .open { color: var(--fg-bright); }
  .open:disabled { color: var(--g9); }

  .name {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .agent {
    font-size: 11px;
    color: var(--g9);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .del {
    display: grid;
    place-items: center;
    align-self: center;
    width: 20px;
    height: 20px;
    margin-right: 4px;
    padding: 0;
    flex: none;
    color: var(--fg-dim);
    opacity: 0;
  }
  .row:hover .del,
  .del:focus-visible { opacity: 1; }
  .del:hover { color: var(--danger-text); background: var(--danger-surface); }

  .hint {
    color: var(--fg-dim);
    font-size: 12px;
    padding: 6px 8px;
  }

  footer {
    flex: none;
    border-top: 1px solid var(--border);
    padding: 8px;
    display: grid;
    gap: 6px;
  }
  .new { width: 100%; }
  .acts { display: flex; gap: 6px; }
  .acts button { flex: 1; }
  .issues {
    color: var(--danger-text);
    font-size: 11px;
    text-align: left;
  }
  .issues:hover { background: var(--danger-surface); color: var(--danger-text); }
</style>
