// 画面はセッションごとに発言の配列を 1 つだけ持つ。確定した履歴も、ストリームで
// 届くイベントも、この配列に対する操作として表す。描画経路を 1 本に保つため、
// どちらの経路から来た項目も同じ形になる。
//
// 項目の形:
//   { id, kind, status, ... }
//   kind   'user' | 'agent' | 'tool' | 'delegate' | 'notice'
//   status 'streaming' | 'running' | 'awaiting' | 'done' | 'error' | 'denied' | 'stopped'

/** ツールの結果から成否を読み取る。 */
function statusOf(text) {
  if (!text) return 'done'
  // 実行に失敗した旨と拒否された旨は、いずれも本文の先頭にこの形で入る
  // (internal/engine/exec.go)。結果に状態欄が無いため、ここで読み取っている。
  if (text.startsWith('エラー: ')) return 'error'
  if (text.startsWith('利用者がこの実行を拒否しました')) return 'denied'
  return 'done'
}

// 画面だけが持つ項目に付ける識別子。時刻を使うと、同じミリ秒に 2 件届いた
// ときに同じ値になり、描画が同一の項目と誤認して落ちる。
let seq = 0
function localId(prefix) {
  seq += 1
  return prefix + '-' + seq
}

function remove(list, item) {
  const i = list.indexOf(item)
  if (i >= 0) list.splice(i, 1)
}

/** 末尾から探す。追記の対象はほぼ常に末尾付近にある。 */
function findById(list, id) {
  if (!id) return null
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].id === id) return list[i]
  }
  return null
}

/** 同じ親を持つ発言の並びを、表示用の項目へ組み立てる。 */
function buildGroup(byParent, key) {
  const out = []
  // 直前の発言が要求したツール呼び出し。後続の tool の発言と順に対応する。
  let pending = []

  for (const m of byParent.get(key) ?? []) {
    switch (m.role) {
      case 'user':
        out.push({ id: m.id, kind: 'user', status: 'done', text: m.content, time: m.created_at })
        break

      case 'assistant':
        pending = (m.tool_calls ?? []).slice()
        // 本文もエラーも推論も無い発言は、ツールを呼ぶためだけの生成だった。
        // 空の吹き出しを残さない。
        if (m.content || m.error || m.thinking) {
          out.push({
            id: m.id,
            kind: 'agent',
            status: m.error ? 'error' : 'done',
            agentId: m.agent_id,
            model: m.model,
            text: m.content,
            thinking: m.thinking || '',
            error: m.error || '',
            time: m.created_at,
          })
        }
        break

      case 'delegate':
        out.push({
          id: m.id,
          kind: 'delegate',
          status: 'done',
          agentId: m.agent_id,
          task: m.content,
          result: '',
          children: buildGroup(byParent, m.id),
          time: m.created_at,
        })
        break

      case 'tool': {
        const call = pending.shift()
        if (m.tool_name === 'delegate') {
          // 委譲の成果は、対応するまとまりへ畳む。独立した行にすると同じ内容が
          // 2 度出る。
          const target = lastKind(out, 'delegate')
          if (target) {
            target.result = m.content
            target.status = statusOf(m.content)
            break
          }
        }
        out.push({
          id: m.id,
          kind: 'tool',
          status: statusOf(m.content),
          tool: m.tool_name,
          args: call?.arguments ?? null,
          result: m.content,
          time: m.created_at,
        })
        break
      }
    }
  }
  return out
}

function lastKind(list, kind) {
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].kind === kind) return list[i]
  }
  return null
}

/**
 * Transcript は 1 つのセッションの発言の配列を保つ。
 * items には Svelte の状態配列をそのまま渡してよい。
 */
export class Transcript {
  constructor(items = []) {
    this.items = items
    // 委譲の深さごとの容れ物。containers[0] が最上位。
    this.containers = [items]
    // 送信直後の発言。保存された識別子が届いたら差し替える。
    this.lastSent = null
  }

  reset() {
    this.items.length = 0
    this.containers = [this.items]
    this.lastSent = null
  }

  /** 確定履歴を読み込む。組み立て直すので、途中の状態は捨てる。 */
  loadHistory(messages) {
    this.reset()
    const byParent = new Map()
    for (const m of messages ?? []) {
      const key = m.parent_id || ''
      if (!byParent.has(key)) byParent.set(key, [])
      byParent.get(key).push(m)
    }
    for (const item of buildGroup(byParent, '')) this.items.push(item)
  }

  /** 送信した本文を先に置く。応答を待つ間、何を送ったかが見えるようにする。 */
  pushUser(text) {
    this.containers = [this.items]
    const item = {
      id: localId('sent'),
      kind: 'user',
      status: 'done',
      text,
      time: new Date().toISOString(),
    }
    this.items.push(item)
    this.lastSent = item
  }

  /**
   * 生成中の項目をすべて確定させる。中断や切断のあとに呼ぶ。
   * 途中で止まったツールは失敗ではない。止まったこととして残す。
   */
  settle() {
    const walk = (list) => {
      for (const it of list) {
        if (it.status === 'streaming' || it.status === 'running' || it.status === 'awaiting') {
          it.status = it.kind === 'agent' ? 'done' : 'stopped'
        }
        it.approvalId = ''
        if (it.children) walk(it.children)
      }
    }
    walk(this.items)
    this.containers = [this.items]
  }

  containerAt(depth) {
    return this.containers[depth] ?? this.items
  }

  /** ストリームのイベントを 1 件反映する。 */
  apply(ev) {
    const depth = ev.depth ?? 0
    const box = this.containerAt(depth)

    switch (ev.type) {
      // 送った本文は画面が先に置いている。保存された識別子を受け取って
      // 差し替え、その発言を指す操作 (巻き戻し) をすぐ使えるようにする。
      case 'user_saved':
        if (this.lastSent && ev.message_id) this.lastSent.id = ev.message_id
        this.lastSent = null
        break

      case 'message_start':
        box.push({
          id: ev.message_id,
          kind: 'agent',
          time: new Date().toISOString(),
          status: 'streaming',
          agentId: ev.agent_id,
          text: '',
          thinking: '',
          error: '',
        })
        break

      case 'delta': {
        const it = findById(box, ev.message_id)
        if (it) it.text += ev.text ?? ''
        break
      }

      // 推論は本文と別に溜める。混ぜると結論と過程の区別がつかなくなる。
      case 'thinking': {
        const it = findById(box, ev.message_id)
        if (it) it.thinking += ev.text ?? ''
        break
      }

      case 'message_end': {
        const it = findById(box, ev.message_id)
        if (!it) break
        it.status = 'done'
        if (!it.text && !it.error && !it.thinking) remove(box, it)
        break
      }

      case 'tool_call':
        box.push({
          id: ev.tool_call_id,
          kind: 'tool',
          status: 'running',
          tool: ev.tool,
          args: ev.args ?? null,
          result: '',
          startedAt: Date.now(),
        })
        break

      case 'approval_request': {
        const it = findById(box, ev.tool_call_id)
        if (it) {
          it.status = 'awaiting'
          it.approvalId = ev.approval?.id ?? ''
          // 承認を待った分が混ざるため、所要時間は出さない。
          it.startedAt = 0
        }
        break
      }

      case 'tool_result': {
        const it = findById(box, ev.tool_call_id)
        if (!it) break
        it.approvalId = ''
        if (it.startedAt) {
          it.ms = Date.now() - it.startedAt
          it.startedAt = 0
        }
        if (it.kind === 'delegate') {
          // 成果は delegate_end で受け取り済み。ここでは成否だけ確定する。
          it.status = statusOf(ev.result)
          break
        }
        it.result = ev.result ?? ''
        it.status = statusOf(it.result)
        break
      }

      case 'delegate_start': {
        // 直前に置いた delegate のツール行を、子を抱えるまとまりへ引き上げる。
        // 委譲が断られた場合は delegate_start が来ないので、ツール行のまま残る。
        const it = findById(box, ev.tool_call_id)
        if (!it) break
        it.kind = 'delegate'
        it.status = 'running'
        it.agentId = ev.agent_id
        it.task = ev.text ?? ''
        it.result = ''
        it.children = []
        this.containers[depth + 1] = it.children
        break
      }

      case 'delegate_end': {
        const it = findById(box, ev.tool_call_id)
        if (it) it.result = ev.result ?? ''
        this.containers.length = depth + 1
        break
      }

      case 'error': {
        const it = findById(box, ev.message_id)
        if (it) {
          it.error = ev.error ?? ''
          it.status = 'error'
          break
        }
        box.push({ id: localId('notice'), kind: 'notice', status: 'error', text: ev.error ?? '' })
        break
      }
    }
  }
}
