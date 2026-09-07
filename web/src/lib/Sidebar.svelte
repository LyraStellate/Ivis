<script>
  import { byBucket } from './format.js'

  let {
    sessions,
    agents,
    status,
    currentId,
    runningId,
    onOpen,
    onNew,
    onNewTeam,
    onDelete,
    onReload,
    onSettings,
  } = $props()

  let listEl = $state(null)

  // 新しい会話の隣に畳んでおく。日常的に使うのは直列の会話のほうなので、
  // チームは 1 手増える位置に置く (#731906)。
  let moreOpen = $state(false)

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
          <!-- 生成中でも開ける。返事を待つ間ほかの会話を読めないほうが困る。
               走っている会話には印を出し、どこが動いているかを示す。 -->
          <button class="open" onclick={() => onOpen(s.id)} onkeydown={onListKeydown}>
            <span class="name">
              {#if s.kind === 'team'}<span class="team" title="チームセッション">◇</span>{/if}
              {s.title}
            </span>
            {#if s.id === runningId}
              <span class="run" title="生成中"><i></i><i></i><i></i></span>
            {:else if s.kind === 'team'}
              <!-- チームには担当が 1 人ではない。1 つの名前を出すと嘘になる。 -->
              <span class="agent">{(s.members?.length ?? 0)} 人のチーム</span>
            {:else}
              <span class="agent">{agentName(s.agent_id)}</span>
            {/if}
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
    <div class="starter">
      <button class="new" onclick={() => onNew()} disabled={agents.length === 0}>新しい会話</button>
      <button
        class="more"
        onclick={() => (moreOpen = !moreOpen)}
        disabled={agents.length === 0}
        title="ほかの始め方"
        aria-label="ほかの始め方"
        aria-expanded={moreOpen}
      >
        <svg viewBox="0 0 12 12" width="10" height="10" aria-hidden="true">
          <path
            d="M2.5 4.5 L6 8 L9.5 4.5"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      </button>
    </div>
    {#if moreOpen}
      <button
        class="team-new"
        onclick={() => {
          moreOpen = false
          onNewTeam?.()
        }}
        disabled={agents.length === 0}
      >
        ◇ チームセッション
      </button>
    {/if}
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
  /* 走っている会話の印。名前の代わりに置くのは、行の幅を奪わないため。
     発言中の印 (Item.svelte) と同じ動きにして、同じ意味だと分かるようにする。 */
  .run {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    flex: none;
  }
  .run i {
    width: 3px;
    height: 3px;
    border-radius: 50%;
    background: var(--fg-muted);
    animation: blink 1.2s ease-in-out infinite;
  }
  .run i:nth-child(2) { animation-delay: 0.15s; }
  .run i:nth-child(3) { animation-delay: 0.3s; }
  @keyframes blink {
    0%, 60%, 100% { opacity: 0.25; }
    30% { opacity: 1; }
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
  .starter { display: flex; gap: 1px; }
  .new { flex: 1; border-top-right-radius: 0; border-bottom-right-radius: 0; }
  .more {
    flex: none;
    display: grid;
    place-items: center;
    width: 24px;
    padding: 0;
    border-top-left-radius: 0;
    border-bottom-left-radius: 0;
    color: var(--fg-dim);
  }
  .team-new {
    width: 100%;
    text-align: left;
    font-size: 12px;
    color: var(--fg-dim);
  }
  /* チームの印。担当の名前を出せない行を、記号だけで見分けられるようにする。 */
  .team { color: var(--accent-line); margin-right: 3px; }
  .acts { display: flex; gap: 6px; }
  .acts button { flex: 1; }
  .issues {
    color: var(--danger-text);
    font-size: 11px;
    text-align: left;
  }
  .issues:hover { background: var(--danger-surface); color: var(--danger-text); }
</style>
