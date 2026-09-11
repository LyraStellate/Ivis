// エージェント編集の入力そのものを扱う。画面から切り離しておくと、
// 既定値と検証をそのまま試験できる。

/** 利用者が作れる最小の Tier。0 は規定エージェントだけが持つ。 */
export const MIN_TIER = 1

/** 規定エージェントの ID。削除できず、Tier も変えられない。 */
export const DEFAULT_ID = 'general'

/** 空の入力。新しいエージェントの出発点になる。 */
export function blank(model = '') {
  return {
    id: '',
    name: '',
    description: '',
    model,
    instructions: '',
    tier: MIN_TIER,
    tools: [],
    skills: ['*'],
    memory: false,
    thinking: false,
    unconfined: false,
    color: '',
    options: null,
  }
}

/** 定義を入力の形へ写す。読み込み元や固定の印は編集の対象ではない。 */
export function toForm(a) {
  return {
    id: a.id,
    name: a.name ?? '',
    description: a.description ?? '',
    model: a.model ?? '',
    instructions: a.instructions ?? '',
    tier: a.tier ?? MIN_TIER,
    tools: [...(a.tools ?? [])],
    skills: [...(a.skills ?? [])],
    memory: !!a.memory,
    thinking: !!a.thinking,
    unconfined: !!a.unconfined,
    color: a.color ?? '',
    options: a.options ?? null,
  }
}

/** 複製の出発点。中身はそのままに、ID と名前だけ空ける。 */
export function copyOf(form) {
  return { ...toForm(form), id: '', name: form.name ? form.name + ' の複製' : '' }
}

/**
 * 送る前に直せる誤りを見つける。サーバー側でも同じことを断るが、往復を
 * 待たずに指せると、どこが悪いのかがその場で分かる。
 * 問題が無ければ null を返す。
 */
export function check(form, existingIds = [], opts = {}) {
  const id = (form.id ?? '').trim()
  if (!id) return { field: 'id', reason: 'ID を入力してください' }
  if (!/^[A-Za-z0-9._-]+$/.test(id)) {
    return { field: 'id', reason: 'ID に使えるのは英数字と - _ . だけです' }
  }
  if (id.startsWith('.')) return { field: 'id', reason: 'ID を . で始めることはできません' }
  if (existingIds.includes(id)) return { field: 'id', reason: 'その ID は既に使われています' }
  if (!(form.model ?? '').trim()) return { field: 'model', reason: 'モデルを選んでください' }

  const tier = Number(form.tier)
  if (!Number.isInteger(tier)) return { field: 'tier', reason: 'Tier は整数で指定してください' }
  if (tier < 0) return { field: 'tier', reason: 'Tier に負の数は指定できません' }
  // チームエージェントは 0 も使える。共通で 0 を規定エージェントだけに
  // 絞っているのは、会話の入口が 2 つある状態を作れないようにするためで、
  // チームには規定エージェントという入口が無い (#731906)。
  if (!opts.team) {
    if (id === DEFAULT_ID) {
      if (tier !== 0) return { field: 'tier', reason: '規定エージェントの Tier は 0 で固定です' }
    } else if (tier < MIN_TIER) {
      return { field: 'tier', reason: 'Tier は 1 以上です。0 は規定エージェントだけが持てます' }
    }
  }
  return null
}

/** 送る形へ整える。空白だけの項目は空として送る。 */
export function payload(form) {
  return {
    id: (form.id ?? '').trim(),
    name: (form.name ?? '').trim(),
    description: (form.description ?? '').trim(),
    model: (form.model ?? '').trim(),
    instructions: form.instructions ?? '',
    tier: Number(form.tier),
    tools: form.tools ?? [],
    skills: form.skills ?? [],
    memory: !!form.memory,
    thinking: !!form.thinking,
    unconfined: !!form.unconfined,
    color: form.color ?? '',
    options: form.options ?? null,
  }
}
