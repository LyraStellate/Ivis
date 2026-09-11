<script>
  // 連絡の記録を図にする (#512740)。
  //
  // 列 = ターン、行 = エージェント。左から右へ流れる。1 ターンめに動いた
  // 相手が出した矢印の宛先が 2 ターンめに動き、そこからさらに 3 ターンめが
  // 伸びる。矢印は必ず隣の列へ進むので、戻る線は無い — 報告は同じ行の後ろの
  // 列に現れる。
  //
  // 図の部品は入れない。ここで要るのは「格子に置いて線を引く」だけで、その
  // ために依存を 1 つ増やす理由が無い。Gauge.svelte と同じく、値から幾何を
  // 組んで SVG を直に書く。
  import { whoColor } from './who.js'

  let { flow, colorOf } = $props()

  const COL = 62 // 列の幅
  const ROW = 26 // 行の高さ
  const R = 7 // 升の丸の半径
  const PAD = 16

  let picked = $state('')
  let panX = $state(0)
  let box = $state(null)
  // 右端に貼り付いているか。新しい列が出たら追う。左へ掘ったら離す。
  let follow = $state(true)

  const cols = $derived(flow?.cols ?? [])
  const nodes = $derived(flow?.nodes ?? [])
  const arrows = $derived(flow?.arrows ?? [])

  // 行はエージェントで固定する。利用者が一番上、以下は名簿順。動いていない
  // ターンの升は空のまま — 縦を見れば誰か、横を見ればいつかが読める。
  const rows = $derived.by(() => {
    const seen = new Map()
    for (const n of nodes) {
      if (!seen.has(n.id)) seen.set(n.id, { id: n.id, name: n.name, tier: n.tier, user: n.user })
    }
    return [...seen.values()].sort((a, b) => {
      if (a.user !== b.user) return a.user ? -1 : 1
      if (a.tier !== b.tier) return a.tier - b.tier
      return a.id < b.id ? -1 : 1
    })
  })

  const rowAt = $derived(new Map(rows.map((r, i) => [r.id, i])))
  const byKey = $derived(new Map(nodes.map((n) => [n.key, n])))

  const width = $derived(PAD * 2 + Math.max(1, cols.length) * COL)
  const height = $derived(PAD * 2 + Math.max(1, rows.length) * ROW)

  // ラウンドの切れ目。列の意味が変わるところに 1 本引く。
  const seams = $derived(
    cols.filter((c, i) => i > 0 && c.round !== cols[i - 1].round).map((c) => c.col),
  )

  const detail = $derived(byKey.get(picked))
  const inbox = $derived(arrows.filter((a) => a.to_node === picked))
  const outbox = $derived(arrows.filter((a) => a.from_node === picked))

  function x(col) {
    return PAD + col * COL + COL / 2
  }
  function y(id) {
    return PAD + (rowAt.get(id) ?? 0) * ROW + ROW / 2
  }
  function nx(n) {
    return x(n.col)
  }
  function ny(n) {
    return y(n.id)
  }

  function name(id) {
    return rows.find((r) => r.id === id)?.name || id || 'あなた'
  }

  // 隣の列へ渡す線。行が離れているほど水平に膨らませて、間の升を避ける。
  function path(a) {
    const s = byKey.get(a.from_node)
    const d = byKey.get(a.to_node)
    if (!s || !d) return ''
    const [x1, y1, x2, y2] = [nx(s) + R, ny(s), nx(d) - R, ny(d)]
    const bend = Math.max(12, Math.abs(x2 - x1) / 2)
    return `M ${x1} ${y1} C ${x1 + bend} ${y1} ${x2 - bend} ${y2} ${x2} ${y2}`
  }

  function cls(a) {
    if (a.relation === '報告') return 'report'
    if (a.relation === '依頼') return 'request'
    return 'order'
  }

  // 新しい列が出たら右端へ寄せる。見たいのはほぼ常にいまのターンである。
  $effect(() => {
    cols.length
    if (follow) panX = Math.max(0, width - (box?.clientWidth ?? width))
  })

  // 背景を掴むと横へ動く。掴んでいる間だけ window に listener を張るのは、
  // 速く動かしたときに枠の外へ出て取りこぼすのを防ぐため — 幅の掴み手
  // (TeamPanel) と同じ形にしてある。
  function grabPan(ev) {
    if (ev.button !== 0) return
    ev.preventDefault()
    const from = ev.clientX
    const start = panX
    const limit = Math.max(0, width - (box?.clientWidth ?? width))
    const move = (e) => {
      panX = Math.min(limit, Math.max(0, start - (e.clientX - from)))
      follow = panX >= limit - 1
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      document.body.style.userSelect = ''
    }
    document.body.style.userSelect = 'none'
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up, { once: true })
  }

  export function latest() {
    follow = true
    panX = Math.max(0, width - (box?.clientWidth ?? width))
  }
</script>

{#if cols.length === 0}
  <p class="empty">まだ誰も動いていません。</p>
{:else}
  <div class="wrap">
    <!-- 見出しは横に動かさない。動かすと、どの行が誰かを見失う。 -->
    <div class="names" style:padding-top="{PAD + 14}px">
      {#each rows as r (r.id)}
        <div class="who" style:height="{ROW}px">
          <span class="dot" style:background={whoColor(r.id, colorOf)}></span>
          <span class="nm">{r.name || r.id}</span>
        </div>
      {/each}
    </div>

    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="grid" bind:this={box} onpointerdown={grabPan}>
      <div class="scroll" style:transform="translateX({-panX}px)" style:width="{width}px">
        <div class="heads">
          {#each cols as c (c.col)}
            <span class="head" class:seam={seams.includes(c.col)} style:width="{COL}px">
              T{c.turn}
            </span>
          {/each}
        </div>
        <svg {width} {height} role="img" aria-label="連絡の流れ">
          <defs>
            <marker
              id="fm-open"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" fill="context-stroke" />
            </marker>
            <marker
              id="fm-done"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="5"
              markerHeight="5"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" fill="context-stroke" opacity="0.55" />
            </marker>
          </defs>

          {#each seams as s (s)}
            <line class="seam" x1={PAD + s * COL} y1="0" x2={PAD + s * COL} y2={height} />
          {/each}

          {#each arrows as a (a.id)}
            <path
              d={path(a)}
              class="edge {cls(a)}"
              class:open={a.open}
              marker-end="url(#{a.open ? 'fm-open' : 'fm-done'})"
            />
          {/each}

          {#each nodes as n (n.key)}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <g
              class="node"
              class:on={picked === n.key}
              onpointerdown={(e) => {
                e.stopPropagation()
                picked = picked === n.key ? '' : n.key
              }}
            >
              <circle cx={nx(n)} cy={ny(n)} r={R} fill={whoColor(n.id, colorOf)} />
              {#if n.waiting}
                <circle class="owe" cx={nx(n) + R - 1} cy={ny(n) - R + 1} r="3.5" />
                <text class="oweN" x={nx(n) + R - 1} y={ny(n) - R + 1}>{n.waiting}</text>
              {/if}
            </g>
          {/each}
        </svg>
      </div>
    </div>
  </div>

  {#if detail}
    <div class="detail">
      <p class="dhead">
        {detail.name || detail.id}<span class="dturn">{detail.turn} ターンめ</span>
      </p>
      {#if inbox.length}
        <p class="dsub">受けたもの</p>
        <ul>
          {#each inbox as a (a.id)}
            <li class:open={a.open}>
              <b>{name(a.from)}</b> から{a.relation || '連絡'}{a.open ? ' (返していない)' : ''}
              <span>{a.body}</span>
            </li>
          {/each}
        </ul>
      {/if}
      {#if outbox.length}
        <p class="dsub">出したもの</p>
        <ul>
          {#each outbox as a (a.id)}
            <li class:open={a.open}>
              <b>{name(a.to)}</b> へ{a.relation || '連絡'}{a.open ? ' (返事待ち)' : ''}
              <span>{a.body}</span>
            </li>
          {/each}
        </ul>
      {/if}
      {#if inbox.length === 0 && outbox.length === 0}
        <p class="empty">やり取りはありません。</p>
      {/if}
    </div>
  {/if}
{/if}

<style>
  .wrap {
    display: flex;
    align-items: flex-start;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--g4);
    overflow: hidden;
  }
  /* 見出しは動かない。どの行が誰かを見失わないための固定枠である。 */
  .names {
    flex: none;
    padding-left: 6px;
    padding-right: 4px;
    border-right: 1px solid var(--border);
  }
  .who {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 10px;
    color: var(--fg-dim);
    white-space: nowrap;
  }
  .dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    flex: none;
  }
  .nm {
    max-width: 62px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .grid {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    touch-action: none;
    cursor: grab;
  }
  .grid:active {
    cursor: grabbing;
  }
  .heads {
    display: flex;
    padding-left: 16px;
  }
  .head {
    flex: none;
    text-align: center;
    font-size: 9px;
    color: var(--fg-dim);
    line-height: 14px;
  }
  .head.seam {
    border-left: 1px solid var(--border-strong);
  }
  svg {
    display: block;
  }
  line.seam {
    stroke: var(--border-strong);
    stroke-dasharray: 2 3;
  }

  .edge {
    fill: none;
    stroke-width: 1;
    opacity: 0.35;
  }
  /* 目が拾うのは、まだ返っていないものである。 */
  .edge.open {
    stroke-width: 1.6;
    opacity: 1;
  }
  .edge.order {
    stroke: var(--who-blue);
  }
  .edge.report {
    stroke: var(--who-green);
  }
  .edge.request {
    stroke: var(--who-amber);
  }

  .node circle {
    stroke: var(--g4);
    stroke-width: 1.5;
  }
  .node:hover circle {
    stroke: var(--border-hover);
  }
  .node.on circle {
    stroke: var(--accent-line);
    stroke-width: 2;
  }
  .owe {
    fill: var(--danger-surface);
    stroke: none !important;
  }
  .oweN {
    fill: var(--danger-text);
    font-size: 6px;
    text-anchor: middle;
    dominant-baseline: middle;
  }

  .detail {
    margin-top: 6px;
  }
  .dhead {
    margin: 0 0 2px;
    font-size: 11px;
    color: var(--fg-bright);
  }
  .dturn {
    margin-left: 6px;
    font-size: 10px;
    color: var(--fg-dim);
  }
  .dsub {
    margin: 6px 0 2px;
    font-size: 10px;
    color: var(--fg-dim);
  }
  .detail ul {
    margin: 0;
    padding-left: 14px;
  }
  .detail li {
    font-size: 11px;
    line-height: 1.5;
    color: var(--fg-dim);
  }
  .detail li.open {
    color: var(--fg);
  }
  .detail li span {
    display: block;
    color: var(--fg-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .empty {
    margin: 4px 0;
    font-size: 11px;
    color: var(--fg-dim);
  }
</style>
