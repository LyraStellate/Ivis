<script>
  import { onMount } from 'svelte'
  import * as api from './lib/api.js'
  import { Transcript } from './lib/conversation.js'
  import Sidebar from './lib/Sidebar.svelte'
  import ChatView from './lib/ChatView.svelte'
  import SettingsPanel from './lib/SettingsPanel.svelte'
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

  // 生成中の会話から離れたら、そのターンの続きは画面へ反映しない。戻って
  // きた時点の履歴と、流れ続けるイベントが二重に積まれるのを避けるため。
  // 生成そのものは走り続け、終わったところで履歴を取り直す。
  let detached = false
  let notice = $state(null)
  let settingsOpen = $state(false)
  let pendingDelete = $state(null)
  let pendingRewind = $state(null)

  // 直前のターンで文脈をどれだけ使ったか。会話を開いた時点では保存された値、
  // 生成中は流れてくるイベントで更新する。
  let usage = $state(null)
  let railHidden = $state(false)

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
  }

  async function newSession(agentId) {
    const created = await guard(() => api.createSession(agentId ?? status?.default_agent))
    if (!created) return
    sessions = [created, ...sessions]
    await openSession(created.id)
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

    try {
      for await (const ev of api.send(sid, text, controller.signal)) {
        if (currentId !== sid) detached = true
        if (detached) continue
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
      // 離れている間に進んだ分は画面に無い。戻っていれば取り直す。
      if (detached && currentId === sid) {
        const history = await guard(() => api.listMessages(sid))
        tx.loadHistory(history ?? [])
        usage = usageOf(session)
      }
      detached = false
    }
  }

  async function cancel() {
    if (runningId == null) return
    const sid = runningId
    await guard(() => api.cancelRun(sid))
    controller?.abort()
  }

  async function approve(approvalId, ok) {
    await guard(() => api.respondApproval(approvalId, ok))
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

<div class="app" class:narrow={railHidden}>
  <Sidebar
    {sessions}
    {agents}
    {status}
    {currentId}
    {runningId}
    onOpen={openSession}
    onNew={newSession}
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
        {notice}
        {status}
        {colorOf}
        {usage}
        {draftBack}
        onSend={send}
        onRewind={askRewind}
        onCancel={cancel}
        onApprove={approve}
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
    grid-template-columns: 240px minmax(0, 1fr);
    grid-template-rows: minmax(0, 1fr);
    transition: grid-template-columns var(--dur) var(--ease);
    height: 100%;
    overflow: hidden;
  }
  .app.narrow {
    grid-template-columns: 0 minmax(0, 1fr);
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
