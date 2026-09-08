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

  let {
    sessionId,
    roster,
    tickets,
    models = [],
    colorOf,
    onRoster,
    onTickets,
    onLead,
    width = 264,
    bounds = { min: 200, max: 620, base: 264 },
    onResize,
    onSizing,
  } = $props()

  // 左端を掴んで幅を変える。パネルは右にあるので、左へ引くほど広くなる。
  //
  // 掴んでいる間は窓ごと拾う。パネルの上から外れた瞬間に止まると、速く
  // 動かしたときに毎回そこで手が離れる。
  function grab(e) {
    e.preventDefault()
    const from = e.clientX
    const start = width
    onSizing?.(true)
    // 掴んでいる間に文字が選ばれると、離したあと選択が残る。
    const held = document.body.style.userSelect
    document.body.style.userSelect = 'none'

    const move = (ev) => onResize?.(start + (from - ev.clientX))
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      document.body.style.userSelect = held
      onSizing?.(false)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  // 掴めない人のための道。掴む操作しか無いものは、その手が使えなければ
  // 変えられないままになる。
  function nudge(e) {
    const step = e.shiftKey ? 48 : 16
    if (e.key === 'ArrowLeft') {
      onResize?.(width + step)
    } else if (e.key === 'ArrowRight') {
      onResize?.(width - step)
    } else if (e.key === 'Home') {
      onResize?.(bounds.max)
    } else if (e.key === 'End') {
      onResize?.(bounds.min)
    } else {
      return
    }
    e.preventDefault()
  }

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

  // 状態ごとの列に分ける。左から 新規 → 終了 で、仕事が左から右へ流れる。
  // 1 本の並びだと、どこで止まっているのかを読み取るのに全部を見ることに
  // なる。列に分ければ、詰まっている場所が形で分かる。
  //
  // 列の中は優先度の高い順、同じなら期限の近い順。番号順に並べると、いま
  // 効いている仕事が古い番号の下に沈む。
  function byWeight(a, b) {
    const p = PRIORITIES.indexOf(b.priority) - PRIORITIES.indexOf(a.priority)
    if (p !== 0) return p
    if (a.due !== b.due) {
      if (!a.due) return 1
      if (!b.due) return -1
      return a.due < b.due ? -1 : 1
    }
    return a.number - b.number
  }

  const columns = $derived.by(() => {
    const names = showClosed ? STATUSES : STATUSES.filter((s) => s !== '終了')
    return names.map((status) => ({
      status,
      items: (tickets ?? []).filter((t) => t.status === status).sort(byWeight),
    }))
  })

  const shown = $derived(columns.reduce((n, c) => n + c.items.length, 0))

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

  // 消せるのは人だけ。モデルに消させないのは、消えたことが誰にも見えない
  // からで、注記はその消えた票に付いていた (#189542)。
  async function removeTicket(n) {
    pendingTicket = null
    detail = null
    await guard(() => api.deleteTicket(sessionId, n))
    await onTickets()
  }

  let pendingTicket = $state(null)
</script>

<aside>
  <!-- フォーカスできる separator は、幅を決めるつまみとして ARIA が定めて
       いる形そのものである (window splitter)。svelte の検査はその組み合わせを
       知らず、div でも button でも別々に咎めるので、ここだけ黙らせる。 -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="grip"
    role="separator"
    aria-orientation="vertical"
    aria-label="パネルの幅"
    aria-valuenow={width}
    aria-valuemin={bounds.min}
    aria-valuemax={bounds.max}
    tabindex="0"
    onpointerdown={grab}
    onkeydown={nudge}
    ondblclick={() => onResize?.(bounds.base)}
    title="ドラッグで幅を変えられます (ダブルクリックで既定へ)"
  ></div>

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
      <span class="n">{shown}</span>
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
      {#if shown === 0}
        <p class="empty">まだありません。</p>
      {:else}
        <!-- 列は横に並べ、幅が足りなければ横へ流す。パネルを広げれば全部が
             一度に見える。 -->
        <div class="board">
          {#each columns as col (col.status)}
            <div class="col" class:done={col.status === '終了'}>
              <p class="colhead s{STATUSES.indexOf(col.status)}">
                {col.status}<span class="cn">{col.items.length}</span>
              </p>
              {#each col.items as t (t.number)}
                <!-- 題を主に置く。状態は列が示しているので、札には出さない。 -->
                <div class="card" class:closed={t.status === '終了'}>
                  <button class="open" onclick={() => (detail = t)}>
                    <div class="top">
                      <span class="tnum mono">#{t.number}</span>
                      {#if t.priority === '緊急' || t.priority === '高'}
                        <span class="pri" class:urgent={t.priority === '緊急'}>{t.priority}</span>
                      {/if}
                    </div>
                    <p class="ttitle">{t.title}</p>
                    <div class="foot">
                      <span class="who" class:none={!t.assignee}>
                        {t.assignee || '未割り当て'}
                      </span>
                      {#if t.due}<span class="due mono">{t.due}</span>{/if}
                      {#if t.note_count}<span class="notes">注記 {t.note_count}</span>{/if}
                    </div>
                  </button>
                  <button
                    class="kill quiet"
                    title="このチケットを消す"
                    aria-label="このチケットを消す"
                    onclick={() => (pendingTicket = t)}
                  >
                    <svg viewBox="0 0 12 12" width="11" height="11" aria-hidden="true">
                      <path d="M3 3 L9 9 M9 3 L3 9" fill="none" stroke="currentColor"
                        stroke-width="1.6" stroke-linecap="round" />
                    </svg>
                  </button>
                </div>
              {:else}
                <p class="colempty">—</p>
              {/each}
            </div>
          {/each}
        </div>
      {/if}
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

{#if pendingTicket}
  <Confirm
    title="このチケットを消しますか"
    body={"#" + pendingTicket.number + " " + pendingTicket.title}
    note="注記も一緒に消えます。元に戻せません。"
    confirmLabel="消す"
    onConfirm={() => removeTicket(pendingTicket.number)}
    onCancel={() => (pendingTicket = null)}
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
    onDelete={() => (pendingTicket = detail)}
    onClose={() => (detail = null)}
  />
{/if}

<style>
  aside {
    position: relative;
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow-y: auto;
    padding: 6px 8px 12px;
    background: var(--rail);
    border-left: 1px solid var(--border);
  }

  /* 掴む場所。境界そのものは 1px しかないので、当たりだけ広く取る。
     線を太くすると、閉じている会話との境目がそこだけ違って見える。 */
  .grip {
    position: absolute;
    top: 0;
    bottom: 0;
    left: -3px;
    width: 7px;
    padding: 0;
    z-index: 1;
    background: transparent;
    border: none;
    border-radius: 0;
    cursor: col-resize;
    touch-action: none;
  }

  .grip::after {
    content: "";
    position: absolute;
    top: 0;
    bottom: 0;
    left: 3px;
    width: 1px;
    background: transparent;
    transition: background var(--dur) var(--ease);
  }
  .grip:hover::after,
  .grip:focus-visible::after {
    background: var(--accent-line);
  }
  .grip:focus-visible {
    outline: none;
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

  /* 状態ごとの列。左から右へ、仕事が進む向きに並べる。パネルが狭ければ
     横へ流す — 縦に積み直すと、左から右へという意味が消える。 */
  .board {
    display: flex;
    gap: 5px;
    overflow-x: auto;
    padding-bottom: 4px;
  }
  .col {
    flex: 1 0 132px;
    min-width: 0;
  }
  .col.done { opacity: 0.6; }
  .colhead {
    position: sticky;
    top: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    gap: 4px;
    margin: 0 0 4px;
    padding: 2px 5px;
    border-radius: var(--radius);
    background: var(--g4);
    color: var(--fg-dim);
    font-size: 10px;
    font-weight: 600;
    white-space: nowrap;
  }
  .colhead .cn {
    margin-left: auto;
    color: var(--g9);
    font-weight: 400;
  }
  .colempty {
    margin: 0;
    padding: 6px 0;
    text-align: center;
    color: var(--g7);
    font-size: 10px;
  }

  /* 1 枚のカード。題を読ませたいので、番号と担当はその上下へ回す。 */
  .card {
    position: relative;
    margin-bottom: 5px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--raised);
  }
  .card:hover { border-color: var(--border-hover); }
  /* 終了したものは沈める。並んでいても、目が拾う先ではない。 */
  .card.closed { opacity: 0.55; }

  .card .open {
    display: block;
    width: 100%;
    padding: 6px 7px;
    background: transparent;
    border-color: transparent;
    text-align: left;
  }
  .card .open:hover { background: transparent; border-color: transparent; }

  .top {
    display: flex;
    align-items: center;
    gap: 5px;
    margin-bottom: 3px;
  }
  .tnum { flex: none; color: var(--g9); font-size: 10px; }
  /* 題は 2 行まで見せる。1 行で切ると、似た書き出しの仕事が見分けられない。 */
  .ttitle {
    margin: 0;
    color: var(--fg-bright);
    font-size: 12px;
    line-height: 1.5;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    overflow-wrap: anywhere;
  }
  /* 列は狭い。入り切らなければ折り返す。切り詰めると、担当も期限も
     読めない断片になる。 */
  .foot {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px 6px;
    margin-top: 4px;
    font-size: 10px;
    color: var(--g9);
  }
  .foot .who {
    color: var(--fg-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 100%;
  }
  .foot .who.none { color: var(--g9); font-style: italic; }

  /* 消す手。取り消せないので、hover で出す位置に置く。 */
  .kill {
    position: absolute;
    top: 3px;
    right: 3px;
    display: grid;
    place-items: center;
    width: 18px;
    height: 18px;
    padding: 0;
    color: var(--fg-dim);
    opacity: 0;
  }
  .card:hover .kill,
  .kill:focus-visible { opacity: 1; }
  .kill:hover { color: var(--danger-text); background: var(--danger-surface); }
  /* 列の見出しは状態ごとに色を変える。文字だけだと、目で流したときに
     どこが進行中の列なのかを毎回読むことになる。 */
  .colhead.s1 { color: var(--accent-line); }
  .colhead.s2,
  .colhead.s3 { color: var(--ok, var(--accent-line)); }
  .colhead.s4 { color: var(--g9); }
  .pri {
    flex: none;
    margin-left: auto;
    color: var(--fg-muted);
    font-size: 9px;
  }
  .pri.urgent { color: var(--danger-text); font-weight: 600; }

  .err {
    margin: 6px 4px 0;
    color: var(--danger-text);
    font-size: 11px;
  }
</style>
