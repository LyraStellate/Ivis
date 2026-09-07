<script>
  import { onMount } from 'svelte'
  import * as api from './lib/api.js'
  import { Transcript } from './lib/conversation.js'
  import Sidebar from './lib/Sidebar.svelte'
  import ChatView from './lib/ChatView.svelte'
  import SettingsPanel from './lib/SettingsPanel.svelte'
  import TeamPanel from './lib/TeamPanel.svelte'
  import Confirm from './lib/Confirm.svelte'

  let status = $state(null)
  let agents = $state([])
  let sessions = $state([])
  let currentId = $state(null)
  let session = $state(null)

  // 画面が持つ発言の配列はこれ 1 つ。確定履歴もストリームもここへ集める。
  let items = $state([])
  const tx = new Transcript(items)

  // 生成は会話に属する。画面全体を止めると、返事を待つ間ほかの会話を
  // 開くこともできない。ツールが増えて 1 ターンが数分かかるようになった
  // 以上、待っている間に別の会話を読めないのは通らない。
  let runningId = $state(null)
  const busy = $derived(runningId != null && runningId === currentId)

  // 使えるコマンド。入力欄が候補を出すのに使う。
  let commands = $state([])

  // いま何を待っているか。届いたイベントから読み取る。何も届かない時間に
  // 秒数だけ出しても、それが何の時間なのかは分からない。
  let stage = $state(null)

  // 最後に何かが届いた時刻。
  //
  // 無音は発言の中とは限らない。次の生成が始まるまでの間、道具を続けて呼ぶ間、
  // 最初の一片が返るまでの間 — いずれも画面には何の項目も無い。届いたかどうか
  // で測れば、どこで止まっていても同じ 1 か所に出せる。
  let moved = $state(0)

  // 生成中の会話から離れたら、そのターンの続きは画面へ反映しない。戻って
  // きた時点の履歴と、流れ続けるイベントが二重に積まれるのを避けるため。
  // 生成そのものは走り続け、終わったところで履歴を取り直す。
  let detached = false
  let notice = $state(null)
  let settingsOpen = $state(false)
  let pendingDelete = $state(null)
  let pendingRewind = $state(null)

  // 直前のターンでコンテキストをどれだけ使ったか。会話を開いた時点では保存された値、
  // 生成中は流れてくるイベントで更新する。
  let usage = $state(null)
  let railHidden = $state(false)

  // チームセッションの名簿とチケット。会話を開いたときに取り、手番が回った
  // あとに取り直す。差分をイベントに載せないのは、載せると同じものを 2 つの
  // 経路で組み立てることになり、食い違ったときにどちらが正か決められない
  // からである (#189542)。
  let roster = $state(null)
  let tickets = $state([])
  let models = $state([])
  let panelHidden = $state(false)
  const isTeam = $derived(session?.kind === 'team')
  const members = $derived(roster?.members ?? [])

  let controller = null

  // 会話が無いときに「誰が答えるか」を出すために使う。
  const defaultAgent = $derived(agents.find((a) => a.id === status?.default_agent) ?? null)

  // 発言の色は定義で選ばれていればそれを使う。定義を画面の各所へ配るのでは
  // なく、ID から色名を引く関数を 1 つ渡す。
  const colorOf = $derived((id) => agents.find((a) => a.id === id)?.color ?? '')

  async function guard(fn) {
    try {
      return await fn()
    } catch (e) {
      if (e.name !== 'AbortError') notice = { kind: e.kind, text: e.message }
      return null
    }
  }

  async function refreshMeta() {
    status = await guard(api.getStatus)
    agents = (await guard(api.listAgents)) ?? []
    sessions = (await guard(api.listSessions)) ?? []
    commands = (await guard(api.listCommands)) ?? []
  }

  async function openSession(id) {
    if (id === currentId) return
    // 生成中でも開ける。離れた会話の続きは、終わってから履歴として届く。
    if (runningId != null && runningId !== id) detached = true
    currentId = id
    session = sessions.find((s) => s.id === id) ?? (await guard(() => api.getSession(id)))
    const history = await guard(() => api.listMessages(id))
    tx.loadHistory(history ?? [])
    usage = usageOf(session)
    notice = null
    roster = null
    tickets = []
    if (session?.kind === 'team') await loadTeam(id)
  }

  // 名簿とチケットを取り直す。会話をまたいで持ち越さないよう、いま開いて
  // いる会話のものだけを反映する。
  async function loadTeam(id) {
    const [r, t] = await Promise.all([
      guard(() => api.getRoster(id)),
      guard(() => api.listTickets(id, { closed: true })),
    ])
    if (currentId !== id) return
    if (r) roster = r
    if (t) tickets = t
    if (models.length === 0) models = (await guard(api.listModels)) ?? []
  }

  async function refreshTickets() {
    if (!currentId || !isTeam) return
    const t = await guard(() => api.listTickets(currentId, { closed: true }))
    if (t) tickets = t
  }

  async function newSession(agentId, kind) {
    const created = await guard(() =>
      api.createSession(agentId ?? status?.default_agent, kind),
    )
    if (!created) return
    sessions = [created, ...sessions]
    await openSession(created.id)
    // チームは組んでから話しかけるものなので、右のパネルを開いて始める。
    if (created.kind === 'team') panelHidden = false
  }

  async function removeSession(id) {
    if (id === runningId) await cancel()
    await guard(() => api.deleteSession(id))
    sessions = sessions.filter((s) => s.id !== id)
    if (currentId === id) {
      currentId = null
      session = null
      tx.reset()
    }
    pendingDelete = null
  }

  // 分母が無いときは何も出さない。割合を推定で出すと嘘になる。
  function usageOf(sess) {
    if (!sess?.context_limit) return null
    return { tokens: sess.context_tokens ?? 0, limit: sess.context_limit }
  }

  // 巻き戻しはファイルを戻さない。会話だけが戻ることを確認で明示する。
  function askRewind(item) {
    if (busy) return
    pendingRewind = { item, count: countFrom(item) }
  }

  // 消える件数は、いま見えている行の数で数える。委譲のまとまりは中身も
  // まとめて消えるので、その分も数える。
  function countFrom(item) {
    const i = items.indexOf(item)
    if (i < 0) return 0
    let n = 0
    const walk = (list) => {
      for (const it of list) {
        n += 1
        if (it.children) walk(it.children)
      }
    }
    walk(items.slice(i))
    return n
  }

  async function rewind() {
    const target = pendingRewind
    pendingRewind = null
    if (!target || !currentId) return
    const res = await guard(() => api.rewindSession(currentId, target.item.id))
    if (!res) return
    session = res.session
    sessions = sessions.map((s) => (s.id === session.id ? session : s))
    usage = usageOf(session)
    const history = await guard(() => api.listMessages(currentId))
    tx.loadHistory(history ?? [])
    draftBack = { text: res.text }
  }

  // 巻き戻した依頼は入力欄へ戻す。やり直す動機はほぼ常に言い直すことにある。
  // 同じ本文を続けて戻すこともあるため、値ではなく入れ物ごと差し替える。
  let draftBack = $state(null)

  async function changeAgent(agentId) {
    if (!currentId) return
    const updated = await guard(() => api.patchSession(currentId, { agent_id: agentId }))
    if (!updated) return
    session = updated
    sessions = sessions.map((s) => (s.id === updated.id ? updated : s))
  }

  async function send(text) {
    if (!currentId || runningId != null) return
    const sid = currentId
    runningId = sid
    detached = false
    notice = null
    tx.pushUser(text)
    controller = new AbortController()
    // 何かが届いた最後の時刻。無音が続いていることを画面が知る唯一の手がかり。
    moved = Date.now()
    stage = { label: '返答を待っています' }

    try {
      for await (const ev of api.send(sid, text, controller.signal)) {
        moved = Date.now()
        if (currentId !== sid) detached = true
        if (detached) continue
        stage = stageOf(ev, stage)
        if (ev.type === 'error' && !ev.message_id) notice = { kind: ev.kind, text: ev.error }
        if (ev.type === 'usage') {
          usage = ev.context_limit ? { tokens: ev.prompt_tokens ?? 0, limit: ev.context_limit } : null
        }
        tx.apply(ev)
      }
    } catch (e) {
      if (!detached && e.name !== 'AbortError') notice = { kind: e.kind, text: e.message }
    } finally {
      runningId = null
      controller = null
      stage = null
      if (!detached) {
        // 生成中のまま取り残された項目を閉じる。ここで全件を取り直さない。
        tx.settle()
      }
      // 一覧の表題と並びだけ更新する。
      const list = await guard(api.listSessions)
      if (list) {
        sessions = list
        session = list.find((s) => s.id === currentId) ?? session
      }
      // チケットは手番の中で変わる。ラウンドが終わったところで取り直す。
      if (!detached && currentId === sid) await refreshTickets()
      // 離れている間に進んだ分は画面に無い。戻っていれば取り直す。
      if (detached && currentId === sid) {
        const history = await guard(() => api.listMessages(sid))
        tx.loadHistory(history ?? [])
        usage = usageOf(session)
      }
      detached = false
    }
  }

  // stageOf は届いたイベントから、いま待っているものの名前を決める。
  //
  // null は「待っていない」を表す。承認と問いの間は利用者の番であって、
  // 止まっているわけではない。そこで秒数を数えると急かしているだけになる。
  function stageOf(ev, prev) {
    switch (ev.type) {
      case 'stage':
        return ev.stage === 'compacting'
          ? { label: 'やり取りをまとめています', done: ev.done ?? 0, unit: '字' }
          : { label: '返答を作っています' }
      case 'approval_request':
      case 'question':
        return null
      // チームの手番。誰の番かと、あと何人待っているかを出す。手番が何度も
      // 入れ替わる間、画面には長く何も届かない (#640275)。
      case 'turn_start':
        return { label: `${ev.agent_id} の番です`, done: ev.queued ?? 0, unit: '件待ち' }
      case 'turn_end':
        return { label: '次の相手へ渡しています', done: ev.queued ?? 0, unit: '件待ち' }
      case 'team_message':
        return { label: `${ev.to} へ渡しています` }
      case 'tool_call':
        return { label: `${ev.tool} を実行しています` }
      case 'tool_result':
        return { label: '結果を読んでいます' }
      case 'delegate_start':
        return { label: `${ev.agent_id} に任せています` }
      case 'message_start':
      case 'delta':
      case 'thinking':
        return { label: '書いています' }
      case 'message_end':
        return { label: '続きを考えています' }
      case 'done':
        return null
      default:
        return prev
    }
  }

  async function cancel() {
    if (runningId == null) return
    const sid = runningId
    await guard(() => api.cancelRun(sid))
    controller?.abort()
  }

  // 返事が届いたら、その場で行を進める。断ったときは結果がすぐ返るので待つ。
  async function approve(approvalId, ok) {
    await guard(() => api.respondApproval(approvalId, ok))
    if (ok) tx.responded(approvalId)
  }

  async function answer(questionId, text) {
    await guard(() => api.respondQuestion(questionId, text))
    tx.responded(questionId)
  }

  async function reload() {
    status = await guard(api.reloadDefs)
    agents = (await guard(api.listAgents)) ?? []
  }

  function onKeydown(e) {
    // 変換中の Esc は候補の取り消しであって、生成の中断ではない。
    if (e.isComposing) return
    if (e.key === 'Escape') {
      if (settingsOpen) {
        settingsOpen = false
      } else if (pendingDelete) {
        pendingDelete = null
      } else if (pendingRewind) {
        pendingRewind = null
      } else if (busy) {
        cancel()
      }
    }
  }

  onMount(refreshMeta)
</script>

<svelte:window onkeydown={onKeydown} />

<div class="app" class:narrow={railHidden} class:teamed={isTeam && !panelHidden}>
  <Sidebar
    {sessions}
    {agents}
    {status}
    {currentId}
    {runningId}
    onOpen={openSession}
    onNew={newSession}
    onNewTeam={(a) => newSession(a, 'team')}
    onDelete={(s) => (pendingDelete = s)}
    onReload={reload}
    onSettings={() => (settingsOpen = true)}
  />

  <main>
    {#if currentId}
      <ChatView
        {railHidden}
        onToggleRail={() => (railHidden = !railHidden)}
        {session}
        {agents}
        {items}
        {busy}
        {moved}
        {stage}
        {commands}
        {members}
        leadId={roster?.lead_id ?? ''}
        panelHidden={isTeam ? panelHidden : null}
        onTogglePanel={() => (panelHidden = !panelHidden)}
        {notice}
        {status}
        {colorOf}
        {usage}
        {draftBack}
        onSend={send}
        onRewind={askRewind}
        onCancel={cancel}
        onApprove={approve}
        onAnswer={answer}
        onAgentChange={changeAgent}
        onDismiss={() => (notice = null)}
      />
    {:else}
      <!-- 何も無い画面は、案内文ではなく次の一手を出す。 -->
      <div class="blank">
        <p class="lead">会話を始める</p>
        {#if defaultAgent}
          <p class="sub">
            <span class="mono">{defaultAgent.name}</span> が
            {#if defaultAgent.model}<span class="mono">{defaultAgent.model}</span> で{/if}答えます。
            相手は後から変えられます。
          </p>
        {/if}
        <button class="primary" onclick={() => newSession()} disabled={agents.length === 0}>
          新しい会話
        </button>
        {#if agents.length === 0}
          <p class="warn">
            エージェントの定義が 1 つも読み込めていません。設定で探索パスを確かめてください。
          </p>
        {:else if status && !status.provider_ok}
          <p class="warn">
            Ollama に接続できていません。起動してから、設定で接続先を確かめてください。
          </p>
        {/if}
      </div>
    {/if}
  </main>

  {#if isTeam && !panelHidden && currentId}
    <TeamPanel
      sessionId={currentId}
      {roster}
      {tickets}
      {models}
      {colorOf}
      onRoster={(r) => (roster = r)}
      onTickets={refreshTickets}
    />
  {/if}

  {#if settingsOpen}
    <SettingsPanel
      {agents}
      onAgentsChanged={async () => {
        agents = (await guard(api.listAgents)) ?? []
        status = await guard(api.getStatus)
      }}
      onClose={() => {
        settingsOpen = false
        refreshMeta()
      }}
    />
  {/if}

  {#if pendingRewind}
    <Confirm
      title="ここからやり直しますか"
      body={pendingRewind.item.text}
      note={`この依頼から後の ${pendingRewind.count} 件が履歴ごと消えます。作ったファイルは戻りません。`}
      confirmLabel="やり直す"
      onConfirm={rewind}
      onCancel={() => (pendingRewind = null)}
    />
  {/if}

  {#if pendingDelete}
    <Confirm
      title="この会話を削除しますか"
      body={pendingDelete.title}
      note={pendingDelete.source === 'discord'
        ? 'このチャンネルの履歴が消えます。次にメンションされたら、新しい会話として作り直されます。'
        : '削除すると元に戻せません。エージェントの定義とスキルには影響しません。'}
      confirmLabel="削除する"
      onConfirm={() => removeSession(pendingDelete.id)}
      onCancel={() => (pendingDelete = null)}
    />
  {/if}
</div>

<style>
  /* 行の高さを画面に固定する。auto のままだと内容の分だけ伸び、
     中の領域がスクロールせずに入力欄が画面の外へ出る。 */
  .app {
    display: grid;
    grid-template-columns: 240px minmax(0, 1fr) 0;
    grid-template-rows: minmax(0, 1fr);
    transition: grid-template-columns var(--dur) var(--ease);
    height: 100%;
    overflow: hidden;
  }
  .app.narrow {
    grid-template-columns: 0 minmax(0, 1fr) 0;
  }
  /* チームの名簿とチケットは会話の右に置く。開いている間だけ幅を取る。 */
  .app.teamed {
    grid-template-columns: 240px minmax(0, 1fr) 264px;
  }
  .app.teamed.narrow {
    grid-template-columns: 0 minmax(0, 1fr) 264px;
  }
  main {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    height: 100%;
  }

  .blank {
    margin: auto;
    display: grid;
    justify-items: center;
    gap: 10px;
    max-width: 26rem;
    padding: 0 1.5rem;
    text-align: center;
  }
  .lead {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--fg-bright);
  }
  .sub {
    margin: 0;
    color: var(--fg-muted);
    line-height: 1.8;
  }
  .warn {
    margin: 6px 0 0;
    color: var(--danger-text);
    font-size: 12px;
    line-height: 1.8;
  }
</style>
