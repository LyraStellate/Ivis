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

  let busy = $state(false)
  let notice = $state(null)
  let settingsOpen = $state(false)
  let pendingDelete = $state(null)
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
    if (busy) return
    currentId = id
    session = sessions.find((s) => s.id === id) ?? (await guard(() => api.getSession(id)))
    const history = await guard(() => api.listMessages(id))
    tx.loadHistory(history ?? [])
    notice = null
  }

  async function newSession(agentId) {
    const created = await guard(() => api.createSession(agentId ?? status?.default_agent))
    if (!created) return
    sessions = [created, ...sessions]
    await openSession(created.id)
  }

  async function removeSession(id) {
    await guard(() => api.deleteSession(id))
    sessions = sessions.filter((s) => s.id !== id)
    if (currentId === id) {
      currentId = null
      session = null
      tx.reset()
    }
    pendingDelete = null
  }

  async function changeAgent(agentId) {
    if (!currentId) return
    const updated = await guard(() => api.patchSession(currentId, { agent_id: agentId }))
    if (!updated) return
    session = updated
    sessions = sessions.map((s) => (s.id === updated.id ? updated : s))
  }

  async function send(text) {
    if (!currentId || busy) return
    busy = true
    notice = null
    tx.pushUser(text)
    controller = new AbortController()

    try {
      for await (const ev of api.send(currentId, text, controller.signal)) {
        if (ev.type === 'error' && !ev.message_id) notice = { kind: ev.kind, text: ev.error }
        tx.apply(ev)
      }
    } catch (e) {
      if (e.name !== 'AbortError') notice = { kind: e.kind, text: e.message }
    } finally {
      busy = false
      controller = null
      // 生成中のまま取り残された項目を閉じる。ここで全件を取り直さない。
      tx.settle()
      // 一覧の表題と並びだけ更新する。
      const list = await guard(api.listSessions)
      if (list) {
        sessions = list
        session = list.find((s) => s.id === currentId) ?? session
      }
    }
  }

  async function cancel() {
    if (!currentId || !busy) return
    await guard(() => api.cancelRun(currentId))
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
    {busy}
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
        onSend={send}
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

  {#if pendingDelete}
    <Confirm
      title="この会話を削除しますか"
      body={pendingDelete.title}
      note="削除すると元に戻せません。エージェントの定義とスキルには影響しません。"
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
