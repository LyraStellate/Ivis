<script>
  // 文脈の残量。書く場所のすぐ隣に置く。あと何を送れるかは、書く前に
  // 知りたいことだからである。
  let { tokens = 0, limit = 0 } = $props()

  // 分母が分からないときは何も出さない。割合を推定で出すと嘘になる。
  const ratio = $derived(limit > 0 ? Math.min(tokens / limit, 1) : null)
  const pct = $derived(ratio == null ? 0 : Math.round(ratio * 100))

  // 半径 7 の円周。弧の長さで量を表す。
  const C = 2 * Math.PI * 7
  const dash = $derived(ratio == null ? 0 : C * ratio)

  // 余裕がある間は目立たせない。残りが少なくなってはじめて色で伝える。
  const tone = $derived(ratio == null ? '' : ratio >= 0.9 ? 'full' : ratio >= 0.7 ? 'near' : '')
  const label = $derived(
    ratio == null ? '' : `文脈 ${tokens.toLocaleString()} / ${limit.toLocaleString()} トークン (${pct}%)`,
  )
</script>

{#if ratio != null}
  <span class="gauge {tone}" title={label} aria-label={label} role="img">
    <svg viewBox="0 0 18 18" width="18" height="18" aria-hidden="true">
      <circle class="track" cx="9" cy="9" r="7" fill="none" stroke-width="2.5" />
      <circle
        class="arc"
        cx="9"
        cy="9"
        r="7"
        fill="none"
        stroke-width="2.5"
        stroke-linecap="round"
        stroke-dasharray="{dash} {C}"
        transform="rotate(-90 9 9)"
      />
    </svg>
    <span class="pct mono tnum">{pct}%</span>
  </span>
{/if}

<style>
  .gauge {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--g9);
  }
  .track {
    stroke: var(--g6);
  }
  .arc {
    stroke: var(--g9);
    transition: stroke-dasharray var(--dur) var(--ease);
  }
  .pct {
    font-size: 10px;
  }

  /* 残りが少なくなったら段を上げる。まず前へ出し、いよいよのときだけ
     警告の色を使う。最初から色を付けると、余裕がある間も急かして見える。 */
  .near .arc { stroke: var(--fg-bright); }
  .near .pct { color: var(--fg-bright); }
  .full .arc { stroke: var(--danger-text); }
  .full .pct { color: var(--danger-text); }
</style>
