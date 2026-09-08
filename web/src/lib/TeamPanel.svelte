<script>
  // チームセッションの右パネル (#731906 / #189542)。
  //
  // 上から 共通エージェント / 固有エージェント / チケット の 3 つを積む。
  // それぞれ折りたためる。チケットは一覧だけを置き、1 件は会話の上に重ねて
  // 開く — この幅では概要と注記が読めないためである。
  import * as api from './api.js'
  import Confirm from './Confirm.svelte'
  import MemberEditor from './MemberEditor.svelte'
  import TicketDetail from './TicketDetail.svelte'
  import { whoColor } from './who.js'

  let { sessionId, roster, tickets, models = [], colorOf, onRoster, onTickets, onLead } = $props()

  const members = $derived(roster?.members ?? [])
  const available = $derived(roster?.available ?? [])
  const locals = $derived(members.filter((m) => m.local))
  const joined = $derived(members.filter((m) => !m.local))
  const takenIds = $derived([...members.map((m) => m.id), ...available.map((a) => a.id)])

  // 終了は既定で出さない。終わった仕事が並ぶと、残っているものが埋もれる。
  let showClosed = $state(false)
  let open = $state({ common: true, local: true, tickets: true })
  let editing = $state(null)
  let pendingDelete = $state(null)
  let detail = $state(null)
  let busy = $state(false)
  let error = $state(null)

  const shown = $derived(
    showClosed ? tickets : (tickets ?? []).filter((t) => t.status !== '終了'),
  )

  // 状態と優先度の並びはサーバーが決める。画面に書き写すと、増やしたときに
  // 片方だけ古くなる。いまは固定なので、値そのものを並びとして持つ。
  const STATUSES = ['新規', '進行中', '解決', 'レビュー', '終了']
  const PRIORITIES = ['低', '中', '高', '緊急']

  async function guard(fn) {
    busy = true
    error = null
    try {
      return await fn()
    } catch (e) {
      error = e.message
      return null
    } finally {
      busy = false
    }
  }

  async function join(id, on) {
    const r = await guard(() => api.joinMember(sessionId, id, on))
    if (r) onRoster(r)
  }

  // 窓口を移す。移さないかぎり、いまの窓口は外せない — 宛先の無い発言の
  // 行き先が消えるためである。移せることが、規定エージェントを外す道になる。
  async function makeLead(id) {
    busy = true
    error = null
    try {
      onRoster(await onLead(id))
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  async function copy(id) {
    const r = await guard(() => api.copyAgent(sessionId, id, ''))
    if (r) onRoster(r)
  }

  async function removeLocal(id) {
    pendingDelete = null
    const r = await guard(() => api.deleteSessionAgent(sessionId, id))
    if (r) onRoster(r)
  }

  async function newTicket() {
    const t = await guard(() =>
      api.createTicket(sessionId, { title: '新しい仕事', body: '' }),
    )
    if (t) {
      await onTickets()
      detail = t
    }
  }

  async function refreshDetail(updated) {
    detail = updated
    await onTickets()
  }
</script>

<aside>
  <section>
    <button class="head" onclick={() => (open.common = !open.common)} aria-expanded={open.common}>
      <span class="caret" class:on={open.common}></span>共通エージェント
      <span class="n">{joined.length} / {joined.length + available.length}</span>
    </button>
    {#if open.common}
      <p class="lede">
        参加させると、この会話で宛先に選べます。定義を直せば、参加している全てのチームに効きます。
        宛先を書かない発言は窓口へ届くので、外したい相手が窓口なら先に窓口を移してください。
      </p>
      {#each joined as m (m.id)}
        <div class="row">
          <span class="dot" style:background={whoColor(m.id, colorOf)}></span>
          <span class="id">{m.id}</span>
          <span class="tier">T{m.tier}</span>
          {#if m.lead}<span class="lead">窓口</span>{/if}
          {#if !m.lead}
            <button class="quiet" disabled={busy} onclick={() => makeLead(m.id)}>窓口に</button>
          {/if}
          <button class="quiet" disabled={busy || m.lead} onclick={() => join(m.id, false)}>
            外す
          </button>
          <button class="quiet" disabled={busy} onclick={() => copy(m.id)}>コピー</button>
        </div>
      {/each}
      {#each available as a (a.id)}
        <div class="row off">
          <span class="dot" style:background={whoColor(a.id, colorOf)}></span>
          <span class="id">{a.id}</span>
          <span class="tier">T{a.tier}</span>
          <button class="quiet" disabled={busy} onclick={() => join(a.id, true)}>参加</button>
          <button class="quiet" disabled={busy} onclick={() => copy(a.id)}>コピー</button>
        </div>
      {:else}
        {#if joined.length === 0}<p class="empty">共通エージェントがありません。</p>{/if}
      {/each}
    {/if}
  </section>

  <section>
    <button class="head" onclick={() => (open.local = !open.local)} aria-expanded={open.local}>
      <span class="caret" class:on={open.local}></span>固有エージェント
      <span class="n">{locals.length}</span>
    </button>
    {#if open.local}
      <p class="lede">この会話の中だけに居ます。会話を消せば一緒に消えます。</p>
      {#each locals as m (m.id)}
        <div class="row">
          <span class="dot" style:background={whoColor(m.id, colorOf)}></span>
          <span class="id">{m.id}</span>
          <span class="tier">T{m.tier}</span>
          {#if m.lead}<span class="lead">窓口</span>{/if}
          {#if !m.lead}
            <button class="quiet" disabled={busy} onclick={() => makeLead(m.id)}>窓口に</button>
          {/if}
          <button class="quiet" onclick={() => (editing = { agent: m })}>編集</button>
          <button class="quiet" disabled={busy || m.lead} onclick={() => (pendingDelete = m)}>
            削除
          </button>
        </div>
      {:else}
        <p class="empty">まだ居ません。</p>
      {/each}
      <button class="add" onclick={() => (editing = { agent: null })}>＋ 新しく作る</button>
    {/if}
  </section>

  <section class="grow">
    <button class="head" onclick={() => (open.tickets = !open.tickets)} aria-expanded={open.tickets}>
      <span class="caret" class:on={open.tickets}></span>チケット
      <span class="n">{shown.length}</span>
    </button>
    {#if open.tickets}
      <p class="lede">
        メンバーは自分宛てのやり取りしか読めません。誰が何をどこまで進めたかは、ここにしか残りません。
      </p>
      <div class="tools">
        <button class="add" onclick={newTicket} disabled={busy}>＋ 起票</button>
        <label class="closed">
          <input type="checkbox" bind:checked={showClosed} /> 終了も出す
        </label>
      </div>
      {#each shown as t (t.number)}
        <button class="tick" onclick={() => (detail = t)}>
          <span class="tnum mono">#{t.number}</span>
          <span class="ttitle">{t.title}</span>
          <span class="st s{STATUSES.indexOf(t.status)}">{t.status}</span>
          {#if t.priority === '緊急' || t.priority === '高'}
            <span class="pri" class:urgent={t.priority === '緊急'}>{t.priority}</span>
          {/if}
          <span class="who">{t.assignee || '—'}</span>
        </button>
      {:else}
        <p class="empty">まだありません。</p>
      {/each}
    {/if}
  </section>

  {#if error}<p class="err">{error}</p>{/if}
  {#each roster?.errors ?? [] as e}
    <p class="err">{e.reason}</p>
  {/each}
</aside>

{#if editing}
  <MemberEditor
    {sessionId}
    agent={editing.agent}
    takenIds={editing.agent ? takenIds.filter((id) => id !== editing.agent.id) : takenIds}
    {models}
    onDone={(r) => {
      editing = null
      onRoster(r)
    }}
    onCancel={() => (editing = null)}
  />
{/if}

{#if pendingDelete}
  <Confirm
    title="この担当を消しますか"
    body={pendingDelete.id}
    note="定義だけが消えます。この会話に残っている発言はそのままです。"
    confirmLabel="削除する"
    onConfirm={() => removeLocal(pendingDelete.id)}
    onCancel={() => (pendingDelete = null)}
  />
{/if}

{#if detail}
  <TicketDetail
    {sessionId}
    ticket={detail}
    members={members}
    statuses={STATUSES}
    priorities={PRIORITIES}
    onChanged={refreshDetail}
    onClose={() => (detail = null)}
  />
{/if}

<style>
  aside {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow-y: auto;
    padding: 6px 8px 12px;
    background: var(--rail);
    border-left: 1px solid var(--border);
  }
  section {
    flex: none;
    border-bottom: 1px solid var(--line);
    padding-bottom: 8px;
    margin-bottom: 8px;
  }
  section.grow { border-bottom: none; }

  .head {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 5px 4px;
    background: transparent;
    border-color: transparent;
    color: var(--fg-bright);
    font-size: 12px;
    font-weight: 600;
    text-align: left;
  }
  .head:hover { background: var(--g4); border-color: transparent; }
  .caret {
    width: 0;
    height: 0;
    border-left: 4px solid var(--fg-dim);
    border-top: 3px solid transparent;
    border-bottom: 3px solid transparent;
    transition: transform var(--dur) var(--ease);
  }
  .caret.on { transform: rotate(90deg); }
  .n { margin-left: auto; color: var(--g9); font-weight: 400; font-size: 11px; }

  .lede {
    margin: 2px 4px 6px;
    color: var(--g9);
    font-size: 10px;
    line-height: 1.6;
  }
  .empty { margin: 4px; color: var(--g9); font-size: 11px; }

  .row {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 2px 4px;
    border-radius: var(--radius);
    font-size: 12px;
  }
  .row:hover { background: var(--g4); }
  .row.off .id { color: var(--fg-dim); }
  .dot { width: 6px; height: 6px; border-radius: 50%; flex: none; }
  .id {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tier { flex: none; color: var(--g9); font-size: 10px; }
  .lead {
    flex: none;
    padding: 0 4px;
    border: 1px solid var(--accent-line);
    border-radius: 999px;
    color: var(--accent-line);
    font-size: 9px;
  }
  .row button {
    flex: none;
    padding: 0 5px;
    font-size: 10px;
    color: var(--fg-dim);
    opacity: 0;
  }
  .row:hover button,
  .row button:focus-visible { opacity: 1; }
  .row button:disabled { opacity: 0; }

  .add {
    width: 100%;
    margin-top: 4px;
    font-size: 11px;
    color: var(--fg-dim);
  }

  .tools { display: flex; align-items: center; gap: 6px; margin-bottom: 4px; }
  .tools .add { width: auto; margin: 0; }
  .closed {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--g9);
    font-size: 10px;
  }

  .tick {
    display: flex;
    align-items: center;
    gap: 5px;
    width: 100%;
    padding: 3px 4px;
    background: transparent;
    border-color: transparent;
    text-align: left;
    font-size: 12px;
  }
  .tick:hover { background: var(--g4); border-color: transparent; }
  .tnum { flex: none; color: var(--g9); font-size: 10px; }
  .ttitle {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--fg);
  }
  /* 状態は色で見分ける。文字だけだと、一覧を目で流したときに拾えない。 */
  .st {
    flex: none;
    padding: 0 5px;
    border-radius: 999px;
    font-size: 9px;
    line-height: 15px;
    border: 1px solid var(--border);
    color: var(--fg-dim);
  }
  .st.s1 { border-color: var(--accent-line); color: var(--accent-line); }
  .st.s2, .st.s3 { border-color: var(--ok, var(--accent-line)); color: var(--ok, var(--accent-line)); }
  .st.s4 { color: var(--g9); }
  .pri {
    flex: none;
    color: var(--fg-muted);
    font-size: 9px;
  }
  .pri.urgent { color: var(--danger-text); }
  .who { flex: none; color: var(--g9); font-size: 10px; max-width: 5rem;
         overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .err {
    margin: 6px 4px 0;
    color: var(--danger-text);
    font-size: 11px;
  }
</style>
