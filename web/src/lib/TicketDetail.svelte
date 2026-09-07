<script>
  // チケット 1 件を、会話の上に重ねて開く。
  //
  // 右パネルの幅では概要と注記が読めない。読めない場所に置いた情報は、
  // 無いのと同じである (#189542)。
  import * as api from './api.js'
  import Markdown from './Markdown.svelte'
  import Confirm from './Confirm.svelte'
  import { trapFocus } from './focus.js'
  import { clock } from './format.js'

  let { sessionId, ticket, members = [], statuses, priorities, onChanged, onClose } = $props()

  let note = $state('')
  let saving = $state(false)
  let error = $state(null)
  let pendingNote = $state(null)
  let editing = $state(false)
  // 題と概要の書きかけ。開いた時点の写しで、更新が届いても上書きしない。
  const seed = () => ({ title: ticket.title, body: ticket.body })
  let draft = $state(seed())

  async function patch(fields) {
    saving = true
    error = null
    try {
      onChanged(await api.patchTicket(sessionId, ticket.number, fields))
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  // 注記は消せない。取り消せない操作なので、人が書くときは確認を挟む。
  // モデルからの追記に確認は挟まない — 挟めば毎回止まる。
  async function addNote() {
    pendingNote = null
    const body = note.trim()
    if (!body) return
    saving = true
    error = null
    try {
      onChanged(await api.addTicketNote(sessionId, ticket.number, body))
      note = ''
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  async function saveText() {
    await patch({ title: draft.title, body: draft.body })
    editing = false
  }
</script>

<div
  class="veil"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onClose()
  }}
>
  <div class="box" role="dialog" aria-modal="true" aria-label={'#' + ticket.number} use:trapFocus>
    <header>
      <span class="num mono">#{ticket.number}</span>
      {#if editing}
        <input class="title" bind:value={draft.title} />
      {:else}
        <h2>{ticket.title}</h2>
      {/if}
      <button class="quiet" onclick={onClose} aria-label="閉じる">✕</button>
    </header>

    <div class="fields">
      <label>
        <span>状態</span>
        <select value={ticket.status} onchange={(e) => patch({ status: e.currentTarget.value })}>
          {#each statuses as s}<option value={s}>{s}</option>{/each}
        </select>
      </label>
      <label>
        <span>優先度</span>
        <select value={ticket.priority} onchange={(e) => patch({ priority: e.currentTarget.value })}>
          {#each priorities as p}<option value={p}>{p}</option>{/each}
        </select>
      </label>
      <label>
        <span>担当</span>
        <select value={ticket.assignee} onchange={(e) => patch({ assignee: e.currentTarget.value })}>
          <option value="">未割り当て</option>
          {#each members as m (m.id)}<option value={m.id}>{m.id}</option>{/each}
        </select>
      </label>
      <label>
        <span>期限</span>
        <input
          type="date"
          value={ticket.due}
          onchange={(e) => patch({ due: e.currentTarget.value })}
        />
      </label>
    </div>

    <div class="body">
      {#if editing}
        <textarea bind:value={draft.body} rows="8"></textarea>
        <div class="acts">
          <button onclick={() => (editing = false)}>取りやめ</button>
          <button class="primary" onclick={saveText}>保存</button>
        </div>
      {:else}
        {#if ticket.body}
          <Markdown text={ticket.body} />
        {:else}
          <p class="empty">概要はまだ書かれていません。</p>
        {/if}
        <button
          class="quiet edit"
          onclick={() => {
            draft = seed()
            editing = true
          }}
        >
          題と概要を直す
        </button>
      {/if}
    </div>

    <section class="notes">
      <p class="cap">注記 — 消せません</p>
      {#each ticket.notes ?? [] as n (n.id)}
        <div class="note" class:auto={n.auto}>
          <span class="meta mono">{n.author || '利用者'} · {clock(n.created_at)}</span>
          <span class="text">{n.body}</span>
        </div>
      {:else}
        <p class="empty">まだありません。</p>
      {/each}

      <div class="add">
        <input
          bind:value={note}
          placeholder="判断したこと、試して駄目だったこと"
          onkeydown={(e) => {
            if (e.key === 'Enter' && !e.isComposing && note.trim()) pendingNote = note.trim()
          }}
        />
        <button disabled={saving || !note.trim()} onclick={() => (pendingNote = note.trim())}>
          足す
        </button>
      </div>
    </section>

    {#if error}<p class="err">{error}</p>{/if}
    <p class="foot mono">
      起票 {ticket.author || '利用者'} · {clock(ticket.created_at)}
    </p>
  </div>
</div>

{#if pendingNote}
  <Confirm
    title="注記を足しますか"
    body={pendingNote}
    note="注記は後から消せません。"
    confirmLabel="足す"
    onConfirm={addNote}
    onCancel={() => (pendingNote = null)}
  />
{/if}

<style>
  .veil {
    position: fixed;
    inset: 0;
    background: rgb(0 0 0 / 0.55);
    display: grid;
    place-items: center;
    z-index: 20;
  }
  .box {
    width: min(38rem, calc(100vw - 3rem));
    max-height: calc(100vh - 3rem);
    overflow-y: auto;
    background: var(--g2);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    padding: 14px 18px 16px;
    box-shadow: 0 12px 40px rgb(0 0 0 / 0.5);
  }
  header {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .num { color: var(--fg-dim); font-size: 12px; flex: none; }
  h2 { margin: 0; font-size: 14px; flex: 1; }
  .title { flex: 1; }
  header button { flex: none; }

  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
    gap: 8px;
    margin: 12px 0;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--line);
  }
  .fields label { display: grid; gap: 3px; }
  .fields span { color: var(--fg-dim); font-size: 11px; }

  .body { position: relative; }
  .body .edit { margin-top: 6px; font-size: 11px; color: var(--fg-dim); }
  .acts { display: flex; justify-content: flex-end; gap: 7px; margin-top: 8px; }
  .empty { color: var(--g9); font-size: 12px; margin: 4px 0; }

  .notes { margin-top: 14px; }
  .cap {
    margin: 0 0 6px;
    color: var(--fg-dim);
    font-size: 11px;
  }
  .note {
    display: grid;
    gap: 1px;
    padding: 5px 0;
    border-top: 1px solid var(--line);
    font-size: 12px;
  }
  /* Ivis が状態や担当の変更から残した行。人が書いたものと見分けが付かないと、
     注記が誰の言葉なのか分からなくなる。 */
  .note.auto .text { color: var(--fg-muted); font-size: 11px; }
  .meta { color: var(--g9); font-size: 10px; }
  .text { white-space: pre-wrap; overflow-wrap: anywhere; }

  .add {
    display: flex;
    gap: 6px;
    margin-top: 10px;
  }
  .add input { flex: 1; }

  .err { margin: 10px 0 0; color: var(--danger-text); font-size: 12px; }
  .foot { margin: 12px 0 0; color: var(--g9); font-size: 10px; }
</style>
