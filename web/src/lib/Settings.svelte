<script>
  import * as api from './api.js'

  let { agents, onClose } = $props()

  let cfg = $state(null)
  let skills = $state([])
  let status = $state(null)
  let models = $state([])
  let saving = $state(false)
  let error = $state(null)

  $effect(() => {
    load()
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
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  const pathsText = (list) => (list ?? []).join('\n')
  const parsePaths = (text) => text.split('\n').map((s) => s.trim()).filter(Boolean)
</script>

<header>
  <h2>設定</h2>
  <button onclick={onClose}>閉じる</button>
</header>

<div class="scroll">
  {#if error}<p class="err">{error}</p>{/if}

  {#if cfg}
    <section>
      <h3>接続</h3>
      <label>
        Ollama の接続先
        <input bind:value={cfg.ollama_base_url} />
      </label>
      {#if status && !status.provider_ok}
        <p class="err">接続できません: {status.provider_error}</p>
      {:else if models.length}
        <p class="hint">利用可能なモデル: {models.map((m) => m.name).join(', ')}</p>
      {/if}
    </section>

    <section>
      <h3>既定のエージェント</h3>
      <select bind:value={cfg.default_agent}>
        {#each agents as a (a.id)}
          <option value={a.id}>{a.name} ({a.id})</option>
        {/each}
      </select>
    </section>

    <section>
      <h3>探索パス</h3>
      <label>
        エージェント定義 (1 行に 1 つ)
        <textarea rows="3" value={pathsText(cfg.agent_paths)}
          onchange={(e) => (cfg.agent_paths = parsePaths(e.currentTarget.value))}></textarea>
      </label>
      <label>
        スキル (先に書いたパスが優先されます)
        <textarea rows="3" value={pathsText(cfg.skill_paths)}
          onchange={(e) => (cfg.skill_paths = parsePaths(e.currentTarget.value))}></textarea>
      </label>
      <label>
        作業ディレクトリ (ファイル操作の境界)
        <input bind:value={cfg.workspace_dir} />
      </label>
    </section>

    <section>
      <h3>実行</h3>
      <label class="row">
        <input type="checkbox" bind:checked={cfg.require_approval} />
        ツールの実行前に承認を求める
      </label>
      <label>1 ターンのツール呼び出し上限
        <input type="number" bind:value={cfg.max_iterations} /></label>
      <label>委譲の深さの上限
        <input type="number" bind:value={cfg.max_delegation_depth} /></label>
      <label>スクリプトの実行時間の上限 (秒)
        <input type="number" bind:value={cfg.script_timeout_sec} /></label>
    </section>

    <section>
      <h3>読み込み結果</h3>
      <p class="hint">スキル {skills.length} 件 / エージェント {agents.length} 件</p>
      {#each status?.agent_errors ?? [] as e}
        <p class="err">エージェント {e.path}: {e.reason}</p>
      {/each}
      {#each status?.skill_errors ?? [] as e}
        <p class="err">スキル {e.path}: {e.reason}</p>
      {/each}
      {#each status?.skill_conflicts ?? [] as c}
        <p class="err">スキル名 {c.name} が重複: {c.winner} を使い、{c.shadows} は無視しています</p>
      {/each}
    </section>

    <div class="acts">
      <button onclick={save} disabled={saving}>{saving ? '保存中...' : '保存して再読込'}</button>
    </div>
  {/if}
</div>

<style>
  header {
    display: flex;
    align-items: center;
    padding: 0.7rem 1rem;
    border-bottom: 1px solid var(--line);
  }
  h2 { flex: 1; margin: 0; font-size: 1rem; }
  h3 { margin: 0 0 0.5rem; font-size: 0.9rem; color: var(--fg-dim); }
  .scroll { flex: 1; overflow-y: auto; padding: 1rem; min-height: 0; }
  section {
    max-width: 44rem;
    margin-bottom: 1.5rem;
    padding-bottom: 1.2rem;
    border-bottom: 1px solid var(--line);
  }
  label { display: block; margin-bottom: 0.7rem; color: var(--fg-dim); font-size: 0.88em; }
  label input, label textarea { margin-top: 0.25rem; color: var(--fg); font-size: 14px; }
  label.row { display: flex; align-items: center; gap: 0.5rem; }
  label.row input { width: auto; margin: 0; }
  .hint { color: var(--fg-dim); font-size: 0.85em; }
  .err { color: var(--danger); font-size: 0.88em; }
  .acts { max-width: 44rem; }
</style>
