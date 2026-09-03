<script>
  // エージェントの一覧と編集。設定と同じ窓の中で切り替わる。別の窓を開くと、
  // どちらが手前かを利用者が管理することになる。
  import * as api from './api.js'
  import Confirm from './Confirm.svelte'
  import { COLORS, whoColor } from './who.js'
  import { blank, toForm, copyOf, check, payload, DEFAULT_ID } from './agentForm.js'

  let { agents, colors = [], models = [], onBack, onClose, onChanged } = $props()

  let selected = $state(null)
  let draft = $state(null)
  let isNew = $state(false)
  let tools = $state([])
  let skills = $state([])
  let saving = $state(false)
  let error = $state(null)
  let badField = $state('')
  let pendingDelete = $state(null)

  const palette = $derived(colors.length ? colors : COLORS)
  const current = $derived(agents.find((a) => a.id === selected) ?? null)
  const fixed = $derived(draft?.id === DEFAULT_ID && !isNew)
  const takenIds = $derived(agents.map((a) => a.id).filter((id) => id !== current?.id))

  // 一覧は Tier 順に並べる。上位から下位へ読めるようにするため。
  const ordered = $derived([...agents].sort((a, b) => a.tier - b.tier || a.id.localeCompare(b.id)))

  $effect(() => {
    load()
  })

  // 何も選ばれていなければ先頭を開く。空の編集欄を見せても、次に何をすれば
  // よいかが伝わらない。
  $effect(() => {
    if (!selected && !isNew && ordered.length) selected = ordered[0].id
  })

  // 選んだ相手が変わったら編集中の内容を入れ替える。書きかけは持ち越さない。
  $effect(() => {
    const a = agents.find((x) => x.id === selected)
    if (a && !isNew) {
      draft = toForm(a)
      error = null
      badField = ''
    }
  })

  async function load() {
    try {
      tools = await api.listTools()
      skills = await api.listSkills()
    } catch (e) {
      error = e.message
    }
  }

  function startNew() {
    isNew = true
    selected = null
    draft = blank(models[0]?.name ?? '')
    error = null
    badField = ''
  }

  function duplicate() {
    if (!draft) return
    isNew = true
    selected = null
    draft = copyOf(draft)
    error = null
    badField = ''
  }

  function pick(id) {
    isNew = false
    selected = id
  }

  async function save() {
    if (!draft) return
    const bad = check(draft, isNew ? takenIds : [])
    if (bad) {
      error = bad.reason
      badField = bad.field
      return
    }
    saving = true
    error = null
    badField = ''
    try {
      const body = payload(draft)
      const saved = isNew ? await api.createAgent(body) : await api.updateAgent(body.id, body)
      isNew = false
      selected = saved.id
      await onChanged()
    } catch (e) {
      error = e.message
      badField = e.field ?? ''
    } finally {
      saving = false
    }
  }

  async function remove(id) {
    pendingDelete = null
    try {
      await api.deleteAgent(id)
      // 選び直しは一覧が入れ替わったあとに任せる。
      selected = null
      draft = null
      await onChanged()
    } catch (e) {
      error = e.message
    }
  }

  const allTools = $derived(draft?.tools?.includes('*') ?? false)

  function toggleTool(name, on) {
    const set = new Set(draft.tools.filter((t) => t !== '*'))
    if (on) set.add(name)
    else set.delete(name)
    draft.tools = [...set]
  }

  function toggleAllTools(on) {
    draft.tools = on ? ['*'] : []
  }

  const asText = (list) => (list ?? []).join('\n')
  const asList = (text) =>
    text
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean)
</script>

<header>
  <button class="quiet back" onclick={onBack} aria-label="設定へ戻る">
    <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
      <path
        d="M7.5 2 L3.5 6 L7.5 10"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
    設定
  </button>
  <h2>エージェント設定</h2>
  <button class="quiet" onclick={onClose}>閉じる <kbd>Esc</kbd></button>
</header>

<div class="body">
  <nav>
    <ul>
      {#each ordered as a (a.id)}
        <li>
          <button class:on={!isNew && a.id === selected} onclick={() => pick(a.id)}>
            <span class="chip" style:background={whoColor(a.id, () => a.color)}></span>
            <span class="nm">{a.name}</span>
            <span class="tier mono tnum">T{a.tier}</span>
          </button>
        </li>
      {/each}
      {#if isNew}
        <li>
          <button class="on">
            <span class="chip new"></span>
            <span class="nm">新しい相手</span>
          </button>
        </li>
      {/if}
    </ul>
    <button class="add" onclick={startNew}>+ 追加</button>
  </nav>

  <div class="form">
    {#if !draft}
      <p class="hint">左から選ぶか、追加してください。</p>
    {:else}
      {#if error}<p class="err">{error}</p>{/if}

      <div class="row2">
        <label class:bad={badField === 'id'}>
          ID
          <input bind:value={draft.id} disabled={!isNew} placeholder="researcher" />
        </label>
        <label>
          表示名
          <input bind:value={draft.name} placeholder="Researcher" />
        </label>
      </div>
      {#if !isNew}
        <p class="hint">
          ID はファイル名になるため変えられません。変えたいときは複製してください。
        </p>
      {/if}

      <label>
        どういうときに呼ぶか
        <textarea rows="2" bind:value={draft.description}></textarea>
      </label>
      <p class="hint">
        ここに書いた文が、上位エージェントの指示文にそのまま載ります。何ができるかではなく、
        どんなときに任せる相手かを書いてください。
      </p>

      <div class="row2">
        <label class:bad={badField === 'tier'}>
          Tier
          <input type="number" min="0" bind:value={draft.tier} disabled={fixed} />
        </label>
        <label class:bad={badField === 'model'}>
          モデル
          {#if models.length}
            <select bind:value={draft.model}>
              {#each models as m (m.name)}<option value={m.name}>{m.name}</option>{/each}
              {#if !models.some((m) => m.name === draft.model)}
                <option value={draft.model}>{draft.model || '(未選択)'}</option>
              {/if}
            </select>
          {:else}
            <input bind:value={draft.model} placeholder="qwen3:8b" />
          {/if}
        </label>
      </div>
      <p class="hint">
        {#if fixed}
          規定エージェントは会話の入口なので Tier 0 で固定です。削除もできません。
        {:else}
          数字が小さいほど上位です。任せられるのは自分より数字の大きい相手だけで、
          同じ数字どうしは呼べません。
        {/if}
      </p>

      <label>
        指示文
        <textarea rows="5" bind:value={draft.instructions}></textarea>
      </label>

      <fieldset>
        <legend>使えるツール</legend>
        <label class="check">
          <input
            type="checkbox"
            checked={allTools}
            onchange={(e) => toggleAllTools(e.currentTarget.checked)}
          />
          <span>すべて許可する</span>
        </label>
        {#if !allTools}
          <div class="checks">
            {#each tools as t (t.name)}
              <label class="check" title={t.description}>
                <input
                  type="checkbox"
                  checked={draft.tools.includes(t.name)}
                  onchange={(e) => toggleTool(t.name, e.currentTarget.checked)}
                />
                <span class="mono">{t.name}</span>
              </label>
            {/each}
          </div>
        {/if}
      </fieldset>

      <label>
        使えるスキル(1 行に 1 つ。* ですべて)
        <textarea
          rows="2"
          value={asText(draft.skills)}
          onchange={(e) => (draft.skills = asList(e.currentTarget.value))}
        ></textarea>
      </label>
      <p class="hint">読み込めているスキル: {skills.length} 件</p>

      <fieldset>
        <legend>ふるまい</legend>
        <label class="check">
          <input type="checkbox" bind:checked={draft.memory} />
          <span>委譲されたとき、同じ会話での前回のやり取りを引き継ぐ</span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={draft.thinking} />
          <span>モデルの推論を使う(対応するモデルのみ)</span>
        </label>
      </fieldset>

      <fieldset>
        <legend>色</legend>
        <div class="swatches">
          <button
            class="sw auto"
            class:on={!draft.color}
            onclick={() => (draft.color = '')}
            title="ID から自動で決める">自動</button
          >
          {#each palette as c (c)}
            <button
              class="sw"
              class:on={draft.color === c}
              style:background="var(--who-{c})"
              onclick={() => (draft.color = c)}
              title={c}
              aria-label={c}
            ></button>
          {/each}
        </div>
      </fieldset>
    {/if}
  </div>
</div>

<footer>
  {#if draft && !isNew && !fixed}
    <button class="danger" onclick={() => (pendingDelete = current)}>削除</button>
  {/if}
  <span class="grow"></span>
  {#if draft}
    <button onclick={duplicate}>複製</button>
    <button class="primary" onclick={save} disabled={saving}>
      {saving ? '保存中…' : '保存する'}
    </button>
  {/if}
</footer>

{#if pendingDelete}
  <Confirm
    title="このエージェントを削除しますか"
    body={pendingDelete.name}
    note="定義ファイルを削除します。この相手を使っている会話は残りますが、続きは送れなくなります。"
    confirmLabel="削除する"
    onConfirm={() => remove(pendingDelete.id)}
    onCancel={() => (pendingDelete = null)}
  />
{/if}

<style>
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 14px;
    border-bottom: 1px solid var(--border);
  }
  h2 {
    flex: 1;
    margin: 0;
    font-size: 14px;
  }
  .back {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--fg-muted);
  }
  kbd {
    font: 11px var(--mono);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0 3px;
  }

  /* 一覧と編集を並べる。選んでいる相手を見ながら直せるようにするため。 */
  .body {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: 12rem minmax(0, 1fr);
  }
  nav {
    display: flex;
    flex-direction: column;
    min-height: 0;
    border-right: 1px solid var(--border);
    background: var(--rail);
  }
  ul {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    list-style: none;
    margin: 0;
    padding: 6px;
  }
  nav li button {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 5px 7px;
    background: transparent;
    border-color: transparent;
    text-align: left;
  }
  nav li button:hover {
    background: var(--control);
  }
  nav li button.on {
    background: var(--control-hover);
    border-color: var(--border);
  }
  .chip {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
  .chip.new {
    background: transparent;
    border: 1px dashed var(--g9);
  }
  .nm {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tier {
    flex: none;
    font-size: 10px;
    color: var(--g9);
  }
  .add {
    flex: none;
    margin: 0 6px 6px;
    background: transparent;
    color: var(--fg-muted);
  }

  .form {
    min-height: 0;
    overflow-y: auto;
    padding: 14px;
  }
  .row2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0 10px;
  }
  label {
    display: block;
    margin-bottom: 9px;
    font-size: 12px;
    color: var(--fg-muted);
  }
  label :global(input),
  label :global(textarea),
  label :global(select) {
    margin-top: 3px;
  }
  label.bad :global(input),
  label.bad :global(select) {
    border-color: var(--danger-border);
  }
  label.check {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--fg);
  }
  label.check input {
    width: auto;
    margin: 0;
  }
  textarea {
    resize: vertical;
  }
  fieldset {
    border: none;
    border-top: 1px solid var(--border);
    margin: 0 0 12px;
    padding: 10px 0 0;
  }
  legend {
    padding: 0;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    color: var(--fg-muted);
  }
  .checks {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
    gap: 2px 10px;
    margin-top: 6px;
  }

  .swatches {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 6px;
  }
  .sw {
    width: 22px;
    height: 22px;
    padding: 0;
    border-radius: 50%;
    border: 2px solid transparent;
  }
  .sw.on {
    border-color: var(--fg-bright);
  }
  .sw.auto {
    width: auto;
    padding: 0 8px;
    border-radius: var(--radius);
    font-size: 11px;
    color: var(--fg-muted);
  }

  .hint {
    color: var(--fg-muted);
    font-size: 11px;
    margin: -4px 0 10px;
    line-height: 1.7;
  }
  .err {
    color: var(--danger-text);
    font-size: 12px;
    margin: 0 0 10px;
    overflow-wrap: anywhere;
  }

  footer {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px;
    border-top: 1px solid var(--border);
  }
  .grow {
    flex: 1;
  }
</style>
