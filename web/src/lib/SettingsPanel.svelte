<script>
  import * as api from './api.js'
  import { trapFocus } from './focus.js'

  // 設定は会話を差し替えない。重ねて開き、閉じると元の会話がそのまま残る。
  let { agents, onClose } = $props()

  let cfg = $state(null)
  let status = $state(null)
  let skills = $state([])
  let models = $state([])
  let saving = $state(false)
  let error = $state(null)
  let saved = $state(false)
  let closeBtn = $state(null)

  $effect(() => {
    load()
    closeBtn?.focus()
  })

  async function load() {
    try {
      cfg = await api.getConfig()
      status = await api.getStatus()
      skills = await api.listSkills()
      models = await api.listModels().catch(() => [])
    } catch (e) {
      error = e.message
    }
  }

  async function save() {
    saving = true
    error = null
    saved = false
    try {
      cfg = await api.putConfig({
        ollama_base_url: cfg.ollama_base_url,
        default_agent: cfg.default_agent,
        workspace_dir: cfg.workspace_dir,
        agent_paths: cfg.agent_paths,
        skill_paths: cfg.skill_paths,
        max_iterations: cfg.max_iterations,
        max_delegation_depth: cfg.max_delegation_depth,
        script_timeout_sec: cfg.script_timeout_sec,
        require_approval: cfg.require_approval,
      })
      status = await api.getStatus()
      skills = await api.listSkills()
      models = await api.listModels().catch(() => [])
      saved = true
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  const asText = (list) => (list ?? []).join('\n')
  const asList = (text) =>
    text
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean)

  const problems = $derived([
    ...(status?.agent_errors ?? []).map((e) => `エージェント ${e.path}: ${e.reason}`),
    ...(status?.skill_errors ?? []).map((e) => `スキル ${e.path}: ${e.reason}`),
    ...(status?.skill_conflicts ?? []).map(
      (c) => `スキル名 ${c.name} が重複。${c.winner} を使い、${c.shadows} は無視しています`,
    ),
  ])
</script>

<div class="veil" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="sheet" role="dialog" aria-modal="true" aria-label="設定" use:trapFocus>
    <header>
      <h2>設定</h2>
      <button bind:this={closeBtn} class="quiet" onclick={onClose}>閉じる <kbd>Esc</kbd></button>
    </header>

    <div class="scroll">
      {#if error}<p class="err">{error}</p>{/if}

      {#if cfg}
        <fieldset>
          <legend>接続</legend>
          <label>
            Ollama の接続先
            <input bind:value={cfg.ollama_base_url} />
          </label>
          {#if status && !status.provider_ok}
            <p class="err">接続できません。Ollama を起動してから保存し直してください。</p>
          {:else if models.length}
            <p class="hint">利用できるモデル: {models.map((m) => m.name).join(', ')}</p>
          {/if}
        </fieldset>

        <fieldset>
          <legend>既定のエージェント</legend>
          <select bind:value={cfg.default_agent}>
            {#each agents as a (a.id)}
              <option value={a.id}>{a.name} ({a.id})</option>
            {/each}
          </select>
          <p class="hint">新しい会話を始めたときに選ばれます。</p>
        </fieldset>

        <fieldset>
          <legend>探索パス</legend>
          <label>
            エージェント定義(1 行に 1 つ)
            <textarea
              rows="3"
              value={asText(cfg.agent_paths)}
              onchange={(e) => (cfg.agent_paths = asList(e.currentTarget.value))}
            ></textarea>
          </label>
          <label>
            スキル(先に書いたパスが優先されます)
            <textarea
              rows="3"
              value={asText(cfg.skill_paths)}
              onchange={(e) => (cfg.skill_paths = asList(e.currentTarget.value))}
            ></textarea>
          </label>
          <label>
            作業ディレクトリ(ファイル操作はこの中に限られます)
            <input bind:value={cfg.workspace_dir} />
          </label>
        </fieldset>

        <fieldset>
          <legend>実行</legend>
          <label class="check">
            <input type="checkbox" bind:checked={cfg.require_approval} />
            <span>ファイルの書き込みとスクリプトの実行の前に確認する</span>
          </label>
          <div class="nums">
            <label>1 ターンのツール呼び出し上限
              <input type="number" bind:value={cfg.max_iterations} /></label>
            <label>委譲の深さの上限
              <input type="number" bind:value={cfg.max_delegation_depth} /></label>
            <label>スクリプトの実行時間の上限(秒)
              <input type="number" bind:value={cfg.script_timeout_sec} /></label>
          </div>
        </fieldset>

        <fieldset>
          <legend>読み込み結果</legend>
          <p class="hint">エージェント {agents.length} 件 / スキル {skills.length} 件</p>
          {#each problems as p}
            <p class="err">{p}</p>
          {:else}
            <p class="hint">読み込みに失敗したものはありません。</p>
          {/each}
        </fieldset>
      {/if}
    </div>

    <footer>
      {#if saved}<span class="hint">保存して読み直しました。</span>{/if}
      <button class="primary" onclick={save} disabled={saving || !cfg}>
        {saving ? '保存中…' : '保存して読み直す'}
      </button>
    </footer>
  </div>
</div>

<style>
  .veil {
    position: fixed;
    inset: 0;
    background: rgb(0 0 0 / 0.45);
    display: flex;
    justify-content: flex-end;
    z-index: 10;
  }
  .sheet {
    width: min(34rem, 100vw);
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--surface);
    border-left: 1px solid var(--border-strong);
    box-shadow: -12px 0 40px rgb(0 0 0 / 0.45);
  }
  header {
    display: flex;
    align-items: center;
    padding: 9px 14px;
    border-bottom: 1px solid var(--border);
  }
  h2 { flex: 1; margin: 0; font-size: 14px; }
  kbd {
    font: 11px var(--mono);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0 3px;
  }
  .scroll { flex: 1; min-height: 0; overflow-y: auto; padding: 14px; }

  fieldset {
    border: none;
    border-bottom: 1px solid var(--border);
    margin: 0 0 14px;
    padding: 0 0 14px;
  }
  legend {
    padding: 0;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    color: var(--fg-muted);
    margin-bottom: 8px;
  }
  label {
    display: block;
    margin-bottom: 9px;
    font-size: 12px;
    color: var(--fg-muted);
  }
  label :global(input),
  label :global(textarea) { margin-top: 3px; }
  label.check {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--fg);
  }
  label.check input { width: auto; margin: 0; }
  .nums {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    gap: 0 10px;
    margin-top: 10px;
  }
  textarea { resize: vertical; font: 12px var(--mono); }

  .hint { color: var(--fg-muted); font-size: 12px; margin: 4px 0 0; }
  .err { color: var(--danger-text); font-size: 12px; margin: 4px 0 0; overflow-wrap: anywhere; }

  footer {
    display: flex;
    align-items: center;
    gap: 10px;
    justify-content: flex-end;
    padding: 10px 14px;
    border-top: 1px solid var(--border);
  }
</style>
