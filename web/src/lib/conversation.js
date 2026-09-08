// 画面はセッションごとに発言の配列を 1 つだけ持つ。確定した履歴も、ストリームで
// 届くイベントも、この配列に対する操作として表す。描画経路を 1 本に保つため、
// どちらの経路から来た項目も同じ形になる。
//
// 項目の形:
//   { id, kind, status, ... }
//   kind   'user' | 'agent' | 'tool' | 'delegate' | 'summary' | 'notice' | 'team'
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

/** 入れ子の中まで見て、条件に合う最初の項目を返す。 */
function findDeep(list, hit) {
  for (let i = list.length - 1; i >= 0; i--) {
    const it = list[i]
    if (hit(it)) return it
    if (it.children) {
      const found = findDeep(it.children, hit)
      if (found) return found
    }
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

      // 圧縮の区切り。ここより前はモデルへ渡らないが、画面には残る。どこで
      // 切り替わったのかが見えないと、答えが変わった理由が読めない。
      case 'summary':
        out.push({
          id: m.id,
          kind: 'summary',
          status: 'done',
          agentId: m.agent_id,
          text: m.content,
          time: m.created_at,
        })
        break

      // チームのメッセージ。宛先と 3 つの内訳を持つ (#640275)。
      case 'team':
        out.push({
          id: m.id,
          kind: 'team',
          status: 'done',
          agentId: m.agent_id,
          to: m.to_agent_id,
          why: m.why ?? '',
          did: m.did ?? '',
          decision: m.decision ?? '',
          text: m.content,
          time: m.created_at,
        })
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
    // 送信直後の発言が持つ局所の識別子。保存された識別子が届いたら差し替える。
    // 要素そのものを持ち越さないのは、items が状態配列だからである。配列へ
    // 入れる前の参照を書き換えても、画面はそれを知らない。
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
    const id = localId('sent')
    this.items.push({
      id,
      kind: 'user',
      status: 'done',
      text,
      time: new Date().toISOString(),
    })
    this.lastSent = id
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
        it.questionId = ''
        if (it.children) walk(it.children)
      }
    }
    walk(this.items)
    this.containers = [this.items]
  }

  containerAt(depth) {
    return this.containers[depth] ?? this.items
  }

  /**
   * 返事を返したことを、その場で画面へ反映する。
   *
   * 許可も答えも別の要求で送るため、経過のストリームには何も現れない。走り
   * 出したことは結果が返るまで誰も知らせてくれず、待っている間ずっと「承認待ち」
   * のまま止まって見える。ここで進めておく。
   *
   * 時計はこの瞬間から始める。待たせた分は道具の所要時間ではない。
   */
  responded(id) {
    const it = findDeep(this.items, (x) => x.approvalId === id || x.questionId === id)
    if (!it) return
    it.approvalId = ''
    it.questionId = ''
    it.status = 'running'
    it.startedAt = Date.now()
  }

  /** ストリームのイベントを 1 件反映する。 */
  apply(ev) {
    const depth = ev.depth ?? 0
    const box = this.containerAt(depth)

    switch (ev.type) {
      // 送った本文は画面が先に置いている。保存された識別子を受け取って
      // 差し替え、その発言を指す操作 (巻き戻し) をすぐ使えるようにする。
      case 'user_saved': {
        // 配列から引き直してから書き換える。他の分岐と同じ経路を通す。
        const it = findById(this.items, this.lastSent)
        if (it && ev.message_id) it.id = ev.message_id
        this.lastSent = null
        break
      }

      // 繋ぎ直したときは、それまでの経過がもう一度流れてくる。同じ識別子で
      // 2 度目が来たら、押し直すのではなく空へ戻して組み立て直す。押すと
      // 同じ発言が並び、戻さないと本文が二重になる。
      case 'message_start': {
        const seen = findDeep(this.items, (x) => x.id === ev.message_id)
        if (seen) {
          seen.status = 'streaming'
          seen.text = ''
          seen.thinking = ''
          seen.error = ''
          break
        }
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
      }

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

      case 'tool_call': {
        const seen = findDeep(this.items, (x) => x.id === ev.tool_call_id)
        if (seen) {
          seen.status = 'running'
          seen.result = ''
          break
        }
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
      }

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

      // 問いは承認と別に持つ。返すものが可否ではなく文なので、同じ入れ物に
      // すると画面はどちらを待っているのか判別できない。
      case 'question': {
        const it = findById(box, ev.tool_call_id)
        if (it) {
          it.status = 'awaiting'
          it.questionId = ev.question?.id ?? ''
          it.question = ev.question?.text ?? ''
          it.choices = ev.question?.choices ?? []
          // 答えを待った分が混ざるため、所要時間は出さない。
          it.startedAt = 0
        }
        break
      }

      case 'tool_result': {
        const it = findById(box, ev.tool_call_id)
        if (!it) break
        it.approvalId = ''
        it.questionId = ''
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

      // メンバーが送った 1 通。手番の切り替わりはこれで見える。
      case 'team_message': {
        if (findDeep(this.items, (x) => x.id === ev.message_id)) break
        this.items.push({
          id: ev.message_id,
          kind: 'team',
          status: 'done',
          agentId: ev.agent_id,
          to: ev.to,
          relation: ev.relation ?? '',
          decision: ev.decision ?? '',
          why: ev.why ?? '',
          did: ev.did ?? '',
          text: ev.text ?? '',
          time: new Date().toISOString(),
        })
        break
      }

      // 手番の入れ替わり。会話の項目は増やさない。誰が動いているかは末尾の
      // 待っている行が出す。ここで行を足すと、送ったメッセージと二重になる。
      case 'turn_start':
      case 'turn_end':
        break

      // 一覧が古くなった、という知らせ。会話の項目にはならない。
      case 'changed':
        break

      // 失敗ではない知らせ。圧縮したことなど、会話の見え方が変わったこと。
      //
      // 繋ぎ直しで同じ知らせが 2 度流れることがある。文と種類が同じものが
      // 既に末尾に居るなら置き直さない。
      case 'notice': {
        const last = this.items[this.items.length - 1]
        if (last?.kind === 'notice' && last.text === (ev.text ?? '')) break
        box.push({ id: localId('notice'), kind: 'notice', status: 'done', text: ev.text ?? '' })
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
