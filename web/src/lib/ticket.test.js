// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'

// 一覧は注記の数だけを返し、本文は返さない。1 件を開くときは引き直す。
const full = {
  number: 1,
  title: '設計の確認',
  body: 'やること',
  assignee: 'hand',
  due: '',
  status: '新規',
  priority: '中',
  author: 'boss',
  note_count: 2,
  notes: [
    { id: 'n1', number: 1, author: 'boss', body: '状態: 新規 → 進行中', auto: true,
      created_at: '2026-09-09T09:00:00Z' },
    { id: 'n2', number: 1, author: 'hand', body: '調べた結果', auto: false,
      created_at: '2026-09-09T09:05:00Z' },
  ],
  created_at: '2026-09-09T09:00:00Z',
  updated_at: '2026-09-09T09:05:00Z',
}

// 一覧に載る形。notes は落ちている (omitempty)。
const row = { ...full, notes: undefined }

const getTicket = vi.fn(async () => full)

vi.mock('./api.js', () => ({
  getTicket: (...a) => getTicket(...a),
  createTicket: vi.fn(async () => ({ ...row, number: 2, title: '新しい仕事', note_count: 0 })),
  deleteTicket: vi.fn(async () => null),
  patchTicket: vi.fn(async () => full),
  addTicketNote: vi.fn(async () => full),
  listTools: vi.fn(async () => []),
  listSkills: vi.fn(async () => []),
  enableMember: vi.fn(async () => null),
  copyAgent: vi.fn(async () => null),
  getRoster: vi.fn(async () => null),
  deleteTeamAgent: vi.fn(async () => null),
  createTeamAgent: vi.fn(async () => null),
  updateTeamAgent: vi.fn(async () => null),
}))

const { default: TeamPanel } = await import('./TeamPanel.svelte')

const roster = { members: [], available: [], errors: [] }

function render() {
  const target = document.createElement('div')
  document.body.appendChild(target)
  const app = mount(TeamPanel, {
    target,
    props: {
      sessionId: 's1',
      roster,
      tickets: [row],
      models: [],
      colorOf: () => '',
      onRoster: () => {},
      onTickets: async () => {},
      width: 264,
      bounds: { min: 200, max: 620, base: 264 },
      onResize: () => {},
      onSizing: () => {},
    },
  })
  flushSync()
  return { target, app }
}

async function settle() {
  for (let i = 0; i < 8; i++) {
    await Promise.resolve()
  }
  flushSync()
}

describe('チケットを開く', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    getTicket.mockClear()
  })

  // 一覧の行をそのまま渡すと、注記が 2 件あると札に出ているのに、開いた先で
  // 「まだありません」になる。開く前に 1 件を引き直す。
  it('開くときに 1 件を引き直し、注記を出す', async () => {
    const { target, app } = render()
    expect(target.textContent).toContain('注記 2')

    target.querySelector('.card .open').click()
    await settle()

    expect(getTicket).toHaveBeenCalledWith('s1', 1)
    const box = document.querySelector('[role="dialog"]')
    expect(box).not.toBe(null)
    expect(box.textContent).toContain('調べた結果')
    expect(box.textContent).toContain('状態: 新規 → 進行中')
    expect(box.textContent).not.toContain('まだありません')
    unmount(app)
  })

  // 自動で残した注記と、人が書いたものを見分けられること。
  it('自動の注記に印が付く', async () => {
    const { app } = render()
    document.querySelector('.card .open').click()
    await settle()

    const notes = [...document.querySelectorAll('.note')]
    expect(notes).toHaveLength(2)
    expect(notes[0].classList.contains('auto')).toBe(true)
    expect(notes[1].classList.contains('auto')).toBe(false)
    unmount(app)
  })
})
