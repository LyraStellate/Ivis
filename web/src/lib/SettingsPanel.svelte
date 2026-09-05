<script>
  import * as api from './api.js'
  import { trapFocus } from './focus.js'
  import AgentSettings from './AgentSettings.svelte'

  // 設定は会話を差し替えない。重ねて開き、閉じると元の会話がそのまま残る。
  // 画面の中央に浮かせる。右端は今後の右パネルのための場所として空けておく。
  let { agents, onClose, onAgentsChanged } = $props()

  // 設定とエージェント設定は同じ窓を差し替える。窓を 2 つ開くと、どちらが
  // 手前かを利用者が管理することになる。
  let view = $state('settings')

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
      // 古い設定ファイルには discord が無い。束ねる先が無いと入力できない。
      cfg.discord ??= { enabled: false, token: '' }
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
        listen: cfg.listen,
        ollama_base_url: cfg.ollama_base_url,
        default_agent: cfg.default_agent,
        workspace_dir: cfg.workspace_dir,
        agent_paths: cfg.agent_paths,
        skill_paths: cfg.skill_paths,
        max_iterations: cfg.max_iterations,
        context_tokens: cfg.context_tokens,
        max_delegation_depth: cfg.max_delegation_depth,
        script_timeout_sec: cfg.script_timeout_sec,
        command_timeout_sec: cfg.command_timeout_sec,
        require_approval: cfg.require_approval,
        auto_approve: cfg.auto_approve ?? [],
        search_backend: cfg.search_backend,
        search_api_key: cfg.search_api_key ?? '',
        discord_enabled: cfg.discord?.enabled ?? false,
        discord_token: cfg.discord?.token ?? '',
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

  // ループバック以外へ束ねようとしているか。認証を持たない以上、これは
  // 利用者が知ったうえで選ぶことであって、黙って通してよい設定ではない。
  const openToNetwork = $derived.by(() => {
    const host = (cfg?.listen ?? '').replace(/:[^:]*$/, '').replace(/[[\]]/g, '')
    if (!host) return false
    return !(host === '127.0.0.1' || host === 'localhost' || host === '::1')
  })

  // 確認を求めるツールと、その既定。名前を手で書かせると、綴りを間違えた
  // ときに黙って確認され続ける。
  const guarded = $derived((status?.tools ?? []).filter((t) => t.approval))
  const autoAll = $derived((cfg?.auto_approve ?? []).includes('*'))

  function skips(name) {
    return autoAll || (cfg?.auto_approve ?? []).includes(name)
  }

  function toggleSkip(name, on) {
    const set = new Set((cfg.auto_approve ?? []).filter((n) => n !== '*'))
    if (on) set.add(name)
    else set.delete(name)
    cfg.auto_approve = [...set]
  }

  function toggleSkipAll(on) {
    cfg.auto_approve = on ? ['*'] : []
  }

  const problems = $derived([
    ...(status?.agent_errors ?? []).map((e) => `エージェント ${e.path}: ${e.reason}`),
    ...(status?.skill_errors ?? []).map((e) => `スキル ${e.path}: ${e.reason}`),
    ...(status?.skill_conflicts ?? []).map(
      (c) => `スキル名 ${c.name} が重複。${c.winner} を使い、${c.shadows} は無視しています`,
    ),
  ])
</script>

<div class="veil" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div
    class="sheet"
    class:wide={view === 'agents'}
    role="dialog"
    aria-modal="true"
    aria-label="設定"
    use:trapFocus
  >
    {#if view === 'agents'}
      <AgentSettings
        {agents}
        {models}
        colors={status?.colors ?? []}
        onBack={() => (view = 'settings')}
        onClose={onClose}
        onChanged={onAgentsChanged}
      />
    {:else}
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
          <label>
            待ち受けアドレス
            <input bind:value={cfg.listen} placeholder="127.0.0.1:8317" />
          </label>
          <p class="hint">
            この端末だけで使うなら <span class="mono">127.0.0.1:8317</span>。他の端末から
            開くなら <span class="mono">0.0.0.0:8317</span> ですべての経路に開くか、
            <span class="mono">100.x.y.z:8317</span> のように VPN のアドレスだけに絞ります。
            変更は再起動後に効きます。
          </p>
          {#if openToNetwork}
            <p class="err">
              いま入れているアドレスは、この端末の外から届きます。Ivis に認証は無く、
              届く相手はファイルの読み書きとスクリプトの実行を頼めます。
            </p>
          {/if}
          {#if status && !status.provider_ok}
            <p class="err">接続できません。Ollama を起動してから保存し直してください。</p>
          {:else if models.length}
            <p class="hint">利用できるモデル: {models.map((m) => m.name).join(', ')}</p>
          {/if}
        </fieldset>

        <fieldset>
          <legend>Discord</legend>
          <label class="check">
            <input type="checkbox" bind:checked={cfg.discord.enabled} />
            <span>Discord と連携する</span>
          </label>
          <label>
            ボットのトークン
            <input type="password" bind:value={cfg.discord.token} placeholder="(未設定)" />
          </label>
          {#if cfg.discord.enabled}
            {#if status?.discord_ok}
              <p class="hint">接続しています。チャンネルでボットをメンションすると答えます。</p>
            {:else if status?.discord_error}
              <p class="err">接続できません: {status.discord_error}</p>
            {:else}
              <p class="hint">まだ接続していません。トークンを入れて保存してください。</p>
            {/if}
          {/if}
          <p class="hint">
            開発者ポータルで <span class="mono">Message Content Intent</span> を有効にしてください。
            有効にしないと、メンションは届いても本文が空で来ます。招待には
            メッセージの送信・履歴の閲覧に加えて
            <span class="mono">applications.commands</span> が要ります。無いと
            <span class="mono">/stop</span> を登録できません。
          </p>
          <p class="hint">
            チャンネルごとに 1 つの会話ができ、一覧の先頭に並びます。返事は
            この画面と同じ経路で作られるので、ツールもコマンドも動きます。
            メンションできる相手は誰でも依頼でき、走っている生成は
            <span class="mono">/stop</span> で誰でも止められます。
          </p>
        </fieldset>

        <fieldset>
          <legend>エージェント</legend>
          <!-- 定義そのものを直す場所へは、設定から入る。同じ窓が切り替わる。 -->
          <button class="nav" onclick={() => (view = 'agents')}>
            <span>エージェント設定</span>
            <span class="count">{agents.length} 件</span>
            <span class="chev">›</span>
          </button>
          <label>
            新しい会話を始める相手
            <select bind:value={cfg.default_agent}>
              {#each agents as a (a.id)}
                <option value={a.id}>{a.name} ({a.id})</option>
              {/each}
            </select>
          </label>
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
          <legend>検索</legend>
          <label>
            取得元
            <select bind:value={cfg.search_backend}>
              {#each status?.search_backends ?? [] as b (b)}
                <option value={b}>{b}</option>
              {/each}
            </select>
          </label>
          <label>
            API キー
            <input type="password" bind:value={cfg.search_api_key} placeholder="(不要な取得元では空のまま)" />
          </label>
          <p class="hint">
            duckduckgo は鍵が要らない代わりに、先方の作りが変わると壊れます。壊れたら
            鍵のある取得元へ移してください。
          </p>
        </fieldset>

        <fieldset>
          <legend>実行</legend>
          <label class="check">
            <input type="checkbox" bind:checked={cfg.require_approval} />
            <span>確認を求めるツールの実行前に確認する</span>
          </label>
          <p class="hint">
            文脈長は 1 回の生成でモデルへ渡せる量です。小さいと、途中で古い側から
            捨てられて応答が途中で終わります。大きいほど記憶に載る量が増えます。
            エージェント側の options に num_ctx があれば、そちらが優先されます。
          </p>
          <div class="nums">
            <label>1 ターンのツール呼び出し上限
              <input type="number" bind:value={cfg.max_iterations} /></label>
            <label>文脈長 (トークン)
              <input type="number" bind:value={cfg.context_tokens} /></label>
            <label>委譲の深さの上限
              <input type="number" bind:value={cfg.max_delegation_depth} /></label>
            <label>スクリプトの実行時間の上限(秒)
              <input type="number" bind:value={cfg.script_timeout_sec} /></label>
            <label>コマンドの実行時間の上限(秒)
              <input type="number" bind:value={cfg.command_timeout_sec} /></label>
          </div>
        </fieldset>

        <fieldset>
          <legend>確認しないツール</legend>
          <p class="hint">
            ここで外したツールは、確認なしで実行されます。読み取りだけのツールは
            もともと確認しません。
          </p>
          <label class="check">
            <input type="checkbox" checked={autoAll}
              onchange={(e) => toggleSkipAll(e.currentTarget.checked)} />
            <span>すべて確認しない</span>
          </label>
          {#if !autoAll}
            <div class="checks">
              {#each guarded as t (t.name)}
                <label class="check" title={t.description}>
                  <input type="checkbox" checked={skips(t.name)}
                    onchange={(e) => toggleSkip(t.name, e.currentTarget.checked)} />
                  <span class="mono">{t.name}</span>
                </label>
              {/each}
            </div>
          {/if}
          {#if autoAll}
            <p class="err">
              コマンドの実行もファイルの削除も、確認なしで走ります。取り消せません。
            </p>
          {/if}
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
    {/if}
  </div>
</div>

<style>
  .veil {
    position: fixed;
    inset: 0;
    background: rgb(0 0 0 / 0.5);
    display: grid;
    place-items: center;
    padding: 24px;
    z-index: 10;
  }
  .sheet {
    width: min(46rem, 100%);
    /* 高さを画面いっぱいにしない。上下に地が見えていないと、脇へ寄せた板に
       見えて、閉じれば元へ戻るものだと伝わらない。 */
    max-height: min(42rem, 100%);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background: var(--g2);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    box-shadow: 0 18px 50px rgb(0 0 0 / 0.55);
  }
  /* エージェント設定は一覧と編集を並べるので、その分だけ広げる。 */
  .sheet.wide {
    width: min(62rem, 100%);
    max-height: min(46rem, 100%);
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

  /* 別の画面へ入る行。設定の項目と同じ幅に置き、押せることを右の印で示す。 */
  .nav {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 9px;
    text-align: left;
  }
  .nav .count { color: var(--fg-muted); font-size: 11px; }
  .nav .chev { margin-left: auto; color: var(--g9); }

  .checks {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
    gap: 2px 10px;
    margin-top: 6px;
  }

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
