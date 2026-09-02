<script>
  import * as api from './lib/api.js'
  import Sidebar from './lib/Sidebar.svelte'
  import Chat from './lib/Chat.svelte'
  import Settings from './lib/Settings.svelte'

  let status = $state(null)
  let agents = $state([])
  let sessions = $state([])
  let currentId = $state(null)
  let session = $state(null)
  let messages = $state([])
  // live はストリーミング中だけの表示。確定した内容はサーバーから読み直す。
  let live = $state([])
  let busy = $state(false)
  let approval = $state(null)
  let notice = $state(null)
  let showSettings = $state(false)

  let controller = null

  async function guard(fn) {
    try {
      return await fn()
    } catch (e) {
      notice = { kind: e.kind, text: e.message }
      return null
    }
  }

  async function refreshMeta() {
    status = await guard(api.getStatus)
    agents = (await guard(api.listAgents)) ?? []
    sessions = (await guard(api.listSessions)) ?? []
  }

  async function openSession(id) {
    currentId = id
    live = []
    approval = null
    session = await guard(() => api.listSessions().then((l) => l.find((s) => s.id === id)))
    messages = (await guard(() => api.listMessages(id))) ?? []
  }

  async function newSession(agentId) {
    const s = await guard(() => api.createSession(agentId ?? status?.default_agent))
    if (!s) return
    sessions = [s, ...sessions]
    await openSession(s.id)
  }

  async function removeSession(id) {
    await guard(() => api.deleteSession(id))
    sessions = sessions.filter((s) => s.id !== id)
    if (currentId === id) {
      currentId = null
      session = null
      messages = []
    }
  }

  async function changeAgent(agentId) {
    if (!currentId) return
    const s = await guard(() => api.patchSession(currentId, { agent_id: agentId }))
    if (s) {
      session = s
      sessions = sessions.map((x) => (x.id === s.id ? s : x))
    }
  }

  async function send(text) {
    if (!currentId || busy) return
    busy = true
    notice = null
    live = [{ kind: 'user', text }]
    controller = new AbortController()

    try {
      for await (const ev of api.send(currentId, text, controller.signal)) {
        apply(ev)
      }
    } catch (e) {
      if (e.name !== 'AbortError') notice = { kind: e.kind, text: e.message }
    } finally {
      busy = false
      approval = null
      controller = null
      // 確定した履歴で置き換える。途中経過の組み立てを信用しない。
      messages = (await guard(() => api.listMessages(currentId))) ?? messages
      live = []
      sessions = (await guard(api.listSessions)) ?? sessions
      session = sessions.find((s) => s.id === currentId) ?? session
    }
  }

  function apply(ev) {
    switch (ev.type) {
      case 'message_start':
        live = [...live, { kind: 'assistant', id: ev.message_id, agent: ev.agent_id, depth: ev.depth, text: '' }]
        break
      case 'delta': {
        const i = live.findLastIndex((m) => m.id === ev.message_id)
        if (i >= 0) live[i].text += ev.text
        break
      }
      case 'tool_call':
        live = [...live, { kind: 'tool_call', depth: ev.depth, tool: ev.tool, args: ev.args }]
        break
      case 'tool_result':
        live = [...live, { kind: 'tool_result', depth: ev.depth, tool: ev.tool, text: ev.result }]
        break
      case 'approval_request':
        approval = ev.approval
        break
      case 'delegate_start':
        live = [...live, { kind: 'delegate_start', depth: ev.depth, agent: ev.agent_id, text: ev.text }]
        break
      case 'delegate_end':
        live = [...live, { kind: 'delegate_end', depth: ev.depth, agent: ev.agent_id, text: ev.result }]
        break
      case 'error':
        notice = { text: ev.error }
        break
    }
  }

  async function respond(ok) {
    if (!approval) return
    const id = approval.id
    approval = null
    await guard(() => api.respondApproval(id, ok))
  }

  async function cancel() {
    if (!currentId) return
    await guard(() => api.cancelRun(currentId))
    controller?.abort()
  }

  async function reload() {
    status = await guard(api.reloadDefs)
    agents = (await guard(api.listAgents)) ?? []
  }

  $effect(() => {
    refreshMeta()
  })
</script>

<div class="layout">
  <Sidebar
    {sessions}
    {agents}
    {status}
    {currentId}
    onOpen={openSession}
    onNew={newSession}
    onDelete={removeSession}
    onReload={reload}
    onSettings={() => (showSettings = true)}
  />

  <main>
    {#if showSettings}
      <Settings agents={agents} onClose={() => { showSettings = false; refreshMeta() }} />
    {:else if currentId}
      <Chat
        {session}
        {agents}
        {messages}
        {live}
        {busy}
        {approval}
        {notice}
        onSend={send}
        onCancel={cancel}
        onApprove={respond}
        onAgentChange={changeAgent}
        onDismiss={() => (notice = null)}
      />
    {:else}
      <div class="empty">
        <h1>Ivis</h1>
        <p>左のパネルから会話を始めてください。</p>
        {#if status && !status.provider_ok}
          <p class="warn">Ollama に接続できません: {status.provider_error}</p>
        {/if}
      </div>
    {/if}
  </main>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: 260px 1fr;
    height: 100%;
  }
  main {
    min-width: 0;
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .empty {
    margin: auto;
    text-align: center;
    color: var(--fg-dim);
  }
  .empty h1 {
    color: var(--fg);
    letter-spacing: 0.08em;
  }
  .warn {
    color: var(--danger);
  }
</style>
