<script>
  // セッション固有のエージェントを 1 体だけ編集する。
  //
  // 設定画面のエージェント編集 (AgentSettings) を使い回さないのは、あちらが
  // 一覧と編集を並べた画面全体の作りだからである。ここで要るのは 1 体分の
  // 欄だけで、開くのも会話の上である。検証と送る形 (agentForm.js) は共通の
  // ものをそのまま通す — そこが食い違うと、同じ入力が場所によって通ったり
  // 通らなかったりする (#731906)。
  import * as api from './api.js'
  import { trapFocus } from './focus.js'
  import { COLORS } from './who.js'
  import { blank, toForm, check, payload } from './agentForm.js'

  let { sessionId, agent = null, takenIds = [], models = [], onDone, onCancel } = $props()

  // 開いた時点の写しを持つ。書きかけの上に元の値が戻ってくると、打った分が
  // 消える。関数の中で読むのは、初期値を 1 度だけ取るためである。
  const isNew = $derived(agent == null)
  const seed = () => (agent ? toForm(agent) : blank(models[0]?.name ?? ''))
  let draft = $state(seed())
  let tools = $state([])
  let skills = $state([])
  let saving = $state(false)
  let error = $state(null)
  let badField = $state('')
  let first = $state(null)

  $effect(() => {
    first?.focus()
  })

  $effect(() => {
    load()
  })

  async function load() {
    try {
      tools = await api.listTools()
      skills = await api.listSkills()
    } catch (e) {
      error = e.message
    }
  }

  function toggle(list, name) {
    // "*" と個別の指定は混ぜない。混ざると、外したはずのものが "*" 経由で
    // 通り、定義を読んでも何が許されているのか分からなくなる。
    const has = list.includes(name)
    return has ? list.filter((v) => v !== name) : [...list.filter((v) => v !== '*'), name]
  }

  async function save() {
    const bad = check(draft, isNew ? takenIds : [], { local: true })
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
      const roster = isNew
        ? await api.createSessionAgent(sessionId, body)
        : await api.updateSessionAgent(sessionId, agent.id, body)
      onDone(roster)
    } catch (e) {
      error = e.message
      badField = e.field ?? ''
    } finally {
      saving = false
    }
  }
</script>

<div
  class="veil"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onCancel()
  }}
>
  <div
    class="box"
    role="dialog"
    aria-modal="true"
    aria-label={isNew ? 'このチームのエージェントを作る' : draft.id + ' を編集'}
    use:trapFocus
  >
    <h2>{isNew ? 'このチームのエージェント' : draft.id}</h2>
    <p class="lede">
      この会話の中だけに居ます。共通の一覧には出ず、会話を消せば一緒に消えます。
    </p>

    <div class="grid">
      {#if isNew}
        <label class="f">
          <span>ID</span>
          <input
            bind:this={first}
            bind:value={draft.id}
            class:bad={badField === 'id'}
            placeholder="reviewer"
          />
        </label>
      {/if}
      <label class="f">
        <span>名前</span>
        <input bind:value={draft.name} placeholder="レビュー担当" />
      </label>
      <label class="f">
        <span>Tier</span>
        <input type="number" bind:value={draft.tier} min="0" class:bad={badField === 'tier'} />
      </label>
      <label class="f">
        <span>モデル</span>
        {#if models.length}
          <select bind:value={draft.model} class:bad={badField === 'model'}>
            {#each models as m (m.name)}<option value={m.name}>{m.name}</option>{/each}
          </select>
        {:else}
          <input bind:value={draft.model} class:bad={badField === 'model'} />
        {/if}
      </label>
      <label class="f">
        <span>色</span>
        <select bind:value={draft.color}>
          <option value="">自動</option>
          {#each COLORS as c}<option value={c}>{c}</option>{/each}
        </select>
      </label>
    </div>

    <!-- 説明は宛先を選ぶ判断材料として、ほかのメンバーの指示文に載る。 -->
    <label class="f wide">
      <span>説明 — ほかのメンバーが「誰に頼むか」を決めるときに読みます</span>
      <input bind:value={draft.description} placeholder="コードの誤りを探す" />
    </label>

    <label class="f wide">
      <span>指示文</span>
      <textarea bind:value={draft.instructions} rows="6"></textarea>
    </label>

    <div class="picks">
      <div>
        <p class="cap">ツール</p>
        <div class="chips">
          <button class:on={draft.tools.includes('*')} onclick={() => (draft.tools = ['*'])}>
            すべて
          </button>
          {#each tools as t (t.name)}
            <button
              class:on={draft.tools.includes(t.name)}
              title={t.description}
              onclick={() => (draft.tools = toggle(draft.tools, t.name))}
            >
              {t.name}{#if t.team_only}<span class="only">チーム</span>{/if}
            </button>
          {/each}
        </div>
      </div>
      <div>
        <p class="cap">スキル</p>
        <div class="chips">
          <button class:on={draft.skills.includes('*')} onclick={() => (draft.skills = ['*'])}>
            すべて
          </button>
          {#each skills as s (s.name)}
            <button
              class:on={draft.skills.includes(s.name)}
              onclick={() => (draft.skills = toggle(draft.skills, s.name))}
            >
              {s.name}
            </button>
          {/each}
        </div>
      </div>
    </div>

    <div class="flags">
      <label><input type="checkbox" bind:checked={draft.thinking} /> 推論を使う</label>
      <label><input type="checkbox" bind:checked={draft.unconfined} /> 作業場所の外へ出られる</label>
    </div>

    {#if error}<p class="err">{error}</p>{/if}

    <div class="acts">
      <button onclick={onCancel}>取りやめ</button>
      <button class="primary" onclick={save} disabled={saving}>
        {saving ? '保存中' : '保存'}
      </button>
    </div>
  </div>
</div>

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
    width: min(40rem, calc(100vw - 3rem));
    max-height: calc(100vh - 3rem);
    overflow-y: auto;
    background: var(--g2);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    padding: 16px 18px;
    box-shadow: 0 12px 40px rgb(0 0 0 / 0.5);
  }
  h2 {
    margin: 0 0 4px;
    font-size: 14px;
  }
  .lede {
    margin: 0 0 12px;
    color: var(--fg-muted);
    font-size: 12px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
    gap: 8px;
  }
  .f {
    display: grid;
    gap: 3px;
    margin-bottom: 8px;
    min-width: 0;
  }
  .f span {
    color: var(--fg-dim);
    font-size: 11px;
  }
  .wide { display: grid; }
  .bad { border-color: var(--danger-border); }

  /* 1fr は minmax(auto, 1fr) なので、子に min-width: 0 が無いと、折り返せない
     ツール名の長さが列の下限になる。囲みが狭いときにそこからはみ出す。 */
  .picks {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
    margin-bottom: 10px;
  }
  .picks > div { min-width: 0; }
  .cap {
    margin: 0 0 4px;
    color: var(--fg-dim);
    font-size: 11px;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    max-height: 8rem;
    overflow-y: auto;
  }
  .chips button {
    max-width: 100%;
    padding: 1px 7px;
    border-radius: 999px;
    font-size: 11px;
    color: var(--fg-dim);
    /* 長いツール名でも列の幅に収める。切れても、何の札かは頭で分かる。 */
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chips button.on {
    border-color: var(--accent-line);
    color: var(--accent-line);
  }
  /* チーム専用のツールは、直列の会話では渡らない。許可はできるので、
     選べなくはせずに印だけ添える。 */
  .only {
    margin-left: 4px;
    color: var(--g9);
    font-size: 9px;
  }

  .flags {
    display: flex;
    gap: 14px;
    font-size: 12px;
    color: var(--fg-muted);
  }
  .flags label {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }
  .err {
    margin: 10px 0 0;
    color: var(--danger-text);
    font-size: 12px;
  }
  .acts {
    display: flex;
    justify-content: flex-end;
    gap: 7px;
    margin-top: 14px;
  }
</style>
