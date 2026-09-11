<script>
  // チームセッションの右パネル (#731906 / #189542)。
  //
  // 上から この会話のメンバー / 共通エージェント / チームエージェント /
  // チケット の 4 つを積む。それぞれ折りたためる。
  //
  // 有効にしたものを 1 つの欄にまとめるのは、「@ を打つと誰が出るのか」に
  // 画面の 1 か所が答えられるようにするためである。由来で割ると 2 か所を見て
  // 頭の中で合成することになり、並びもメンション補完と一致しない (#731906)。
  //
  // チケットは一覧だけを置き、1 件は会話の上に重ねて開く — この幅では概要と
  // 注記が読めないためである。
  import * as api from './api.js'
  import Confirm from './Confirm.svelte'
  import FlowMap from './FlowMap.svelte'
  import MemberEditor from './MemberEditor.svelte'
  import TicketDetail from './TicketDetail.svelte'
  import { whoColor } from './who.js'

  let {
    sessionId,
    roster,
    tickets,
    flow = null,
    models = [],
    colorOf,
    onRoster,
    onTickets,
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
  // まだ有効でないものは由来で分ける。共通の定義を持っているのは設定画面で、
  // チームの定義を作って直すのはここ。操作が違うので、欄も分かれる。
  const freeCommon = $derived(available.filter((a) => a.scope !== 'team'))
  const freeTeam = $derived(available.filter((a) => a.scope === 'team'))
  // 有効になっているチームエージェントも、ここから直せる。共有物なので、
  // 有効かどうかで編集できたりできなかったりするのは筋が通らない。
  const teamMembers = $derived(members.filter((m) => m.scope === 'team'))
  // ID は共通とチームを通して一意。作るときはその全部を避ける。
  const takenIds = $derived([...members.map((m) => m.id), ...available.map((a) => a.id)])

  // 終了は既定で出さない。終わった仕事が並ぶと、残っているものが埋もれる。
  let showClosed = $state(false)
  let open = $state({ members: true, common: true, team: true, tickets: true, flow: true })
  // 「最新へ」のために FlowMap を掴む。読むのは押されたときだけだが、
  // 束ねずに置くと Svelte が非反応の書き換えとして警告する。
  let map = $state(null)
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

  // 返事の返っていない矢印の数。欄の見出しに出す — 折りたたんでいても
  // 待っているものがあるかどうかは分かるようにする。
  const waiting = $derived((flow?.arrows ?? []).filter((a) => a.open).length)
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

  // この会話で使うかどうかを切り替える。定義そのものには触れない。
  async function enable(id, on) {
    const r = await guard(() => api.enableMember(sessionId, id, on))
    if (r) onRoster(r)
  }

  async function copy(id) {
    const r = await guard(() => api.copyAgent(sessionId, id, ''))
    if (r) onRoster(r)
  }

  // 定義を消す。有効にしている全ての会話から居なくなる。
  //
  // 消しても名簿からは掃除しないので、引き直した名簿には「定義が見つかり
  // ません」が 1 件出る。何が消えたのかが見えるほうが、黙って減るよりよい。
  async function removeAgent(id) {
    pendingDelete = null
    if (await guard(() => api.deleteTeamAgent(id).then(() => true))) await reload()
  }

  // 名簿を引き直す。共有物への操作 (編集・削除) は会話に紐づいていないので、
  // 名簿を返さない。
  async function reload() {
    const r = await guard(() => api.getRoster(sessionId))
    if (r) onRoster(r)
  }

  async function newTicket() {
    const t = await guard(() =>
      api.createTicket(sessionId, { title: '新しい仕事', body: '' }),
    )
    if (t) {
      await onTickets()
      await open1(t.number)
    }
  }

  // 1 件を開く。一覧の行をそのまま渡してはいけない。
  //
  // 一覧 (list_tickets) は注記の数だけを返し、本文は返さない。行をそのまま
  // 渡すと、注記が 3 件あると札に出ているのに、開いた先では「まだありません」
  // になる。開く前に引き直す (#189542)。
  async function open1(number) {
    const full = await guard(() => api.getTicket(sessionId, number))
    if (full) detail = full
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
    <button class="head" onclick={() => (open.members = !open.members)} aria-expanded={open.members}>
      <span class="caret" class:on={open.members}></span>この会話のメンバー
      <span class="n">{members.length}</span>
    </button>
    {#if open.members}
      <p class="lede">
        @ で宛先に選べるのはここに居る人だけです。並びは Tier 順で、メンション補完もこの順に出ます。
      </p>
      {#each members as m (m.id)}
        <div class="row">
          <span class="dot" style:background={whoColor(m.id, colorOf)}></span>
          <span class="id">{m.id}</span>
          <span class="tier">T{m.tier}</span>
          <span class="from">{m.scope === 'team' ? 'チーム' : '共通'}</span>
          <button class="quiet" disabled={busy} onclick={() => enable(m.id, false)}>外す</button>
        </div>
      {:else}
        <p class="empty">まだ誰も居ません。下の欄から足してください。</p>
      {/each}
    {/if}
  </section>

  <section>
    <button class="head" onclick={() => (open.common = !open.common)} aria-expanded={open.common}>
      <span class="caret" class:on={open.common}></span>共通エージェント
      <span class="n">{freeCommon.length}</span>
    </button>
    {#if open.common}
      <p class="lede">
        直列の会話でも使う定義です。直すのは設定画面から。この会話のためだけに変えたいなら、
        チームへコピーしてください。
      </p>
      {#each freeCommon as a (a.id)}
        <div class="row off">
          <span class="dot" style:background={whoColor(a.id, colorOf)}></span>
          <span class="id">{a.id}</span>
          <span class="tier">T{a.tier}</span>
          <button class="quiet" disabled={busy} onclick={() => enable(a.id, true)}>参加</button>
          <button class="quiet" disabled={busy} onclick={() => copy(a.id)}>チームへコピー</button>
        </div>
      {:else}
        <p class="empty">
          {members.length > 0 ? '残りはありません。' : '共通エージェントがありません。'}
        </p>
      {/each}
    {/if}
  </section>

  <section>
    <button class="head" onclick={() => (open.team = !open.team)} aria-expanded={open.team}>
      <span class="caret" class:on={open.team}></span>チームエージェント
      <span class="n">{teamMembers.length + freeTeam.length}</span>
    </button>
    {#if open.team}
      <p class="lede">
        <strong>すべてのチーム会話で共有されます。</strong>
        直せば、有効にしている全ての会話に効きます。削除すると、全ての会話から居なくなります。
      </p>
      {#each teamMembers as m (m.id)}
        <div class="row">
          <span class="dot" style:background={whoColor(m.id, colorOf)}></span>
          <span class="id">{m.id}</span>
          <span class="tier">T{m.tier}</span>
          <span class="from on">この会話</span>
          <button class="quiet" onclick={() => (editing = { agent: m })}>編集</button>
          <button class="quiet" disabled={busy} onclick={() => (pendingDelete = m)}>削除</button>
        </div>
      {/each}
      {#each freeTeam as a (a.id)}
        <div class="row off">
          <span class="dot" style:background={whoColor(a.id, colorOf)}></span>
          <span class="id">{a.id}</span>
          <span class="tier">T{a.tier}</span>
          <button class="quiet" disabled={busy} onclick={() => enable(a.id, true)}>有効化</button>
          <button class="quiet" onclick={() => (editing = { agent: a })}>編集</button>
          <button class="quiet" disabled={busy} onclick={() => (pendingDelete = a)}>削除</button>
        </div>
      {/each}
      {#if teamMembers.length + freeTeam.length === 0}
        <p class="empty">まだ居ません。</p>
      {/if}
      <button class="add" onclick={() => (editing = { agent: null })}>＋ 新しく作る</button>
    {/if}
  </section>

  <section>
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
                  <button class="open" onclick={() => open1(t.number)}>
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

  <!-- 連絡の記録 (#512740)。チケットが「何の仕事がどこまで進んだか」を持つ
       のに対し、こちらは「誰が誰に何を頼み、それが返ってきたか」を持つ。 -->
  <section class="grow">
    <button class="head" onclick={() => (open.flow = !open.flow)} aria-expanded={open.flow}>
      <span class="caret" class:on={open.flow}></span>流れ図
      <span class="n">{waiting}</span>
    </button>
    {#if open.flow}
      <p class="lede">
        列がターン、行がエージェントです。左から右へ流れます。濃い矢印が返事待ちで、升を押すとそのターンのやり取りが出ます。掴むと横へ動きます。
      </p>
      <div class="tools">
        <button class="add" onclick={() => map?.latest()}>最新へ</button>
      </div>
      <FlowMap bind:this={map} {flow} {colorOf} />
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
      if (r) onRoster(r)
      else reload()
    }}
    onCancel={() => (editing = null)}
  />
{/if}

{#if pendingDelete}
  <Confirm
    title="この担当を消しますか"
    body={pendingDelete.id}
    note="定義が消え、有効にしている全ての会話から居なくなります。発言とチケットはそのままです。"
    confirmLabel="削除する"
    onConfirm={() => removeAgent(pendingDelete.id)}
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
  /* 由来の印。押せる場所と間違えられないよう、枠は付けない。 */
  .from {
    flex: none;
    color: var(--g9);
    font-size: 9px;
  }
  .from.on { color: var(--accent-line); }
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
