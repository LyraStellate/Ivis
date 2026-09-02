<script>
  import { renderMarkdown } from './markdown.js'

  let { text = '' } = $props()

  // 生成中はトークンごとに解析し直さず、一定の間隔で描画を更新する。
  let shown = $state('')
  let lastAt = 0

  $effect(() => {
    const next = text ?? ''
    if (next === shown) return
    const wait = Math.max(0, 100 - (Date.now() - lastAt))
    const id = setTimeout(() => {
      lastAt = Date.now()
      shown = next
    }, wait)
    return () => clearTimeout(id)
  })

  const html = $derived(renderMarkdown(shown))

  let el = $state(null)

  // コードブロックに言語名とコピーの手段を添える。描き直すたびに付け直す。
  $effect(() => {
    html
    if (!el) return
    for (const pre of el.querySelectorAll('pre')) {
      if (pre.previousElementSibling?.classList.contains('codebar')) continue
      const code = pre.querySelector('code')
      const lang =
        [...(code?.classList ?? [])].find((c) => c.startsWith('language-'))?.slice(9) ?? ''

      const bar = document.createElement('div')
      bar.className = 'codebar'

      const name = document.createElement('span')
      name.textContent = lang
      bar.appendChild(name)

      const copy = document.createElement('button')
      copy.type = 'button'
      copy.textContent = 'コピー'
      copy.addEventListener('click', async () => {
        try {
          await navigator.clipboard.writeText(code?.textContent ?? '')
          copy.textContent = 'コピーしました'
          setTimeout(() => (copy.textContent = 'コピー'), 1200)
        } catch {
          copy.textContent = 'コピーできません'
        }
      })
      bar.appendChild(copy)

      pre.parentNode.insertBefore(bar, pre)
    }
  })
</script>

<div class="md" bind:this={el}>{@html html}</div>

<style>
  .md {
    line-height: 1.75;
    overflow-wrap: anywhere;
  }
  .md :global(> :first-child) { margin-top: 0; }
  .md :global(> :last-child) { margin-bottom: 0; }

  .md :global(h1),
  .md :global(h2),
  .md :global(h3) {
    font-size: 14px;
    margin: 1.2em 0 0.5em;
    line-height: 1.5;
  }
  .md :global(h1) { font-size: 15px; }
  .md :global(p) { margin: 0.6em 0; }
  .md :global(ul), .md :global(ol) { margin: 0.6em 0; padding-left: 1.4em; }
  .md :global(li) { margin: 0.2em 0; }
  .md :global(blockquote) {
    margin: 0.6em 0;
    padding-left: 0.9em;
    border-left: 2px solid var(--border);
    color: var(--fg-muted);
  }
  .md :global(a) { color: var(--accent-text); }
  .md :global(hr) { border: none; border-top: 1px solid var(--border); margin: 1.2em 0; }

  .md :global(code) {
    background: var(--n3);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0 3px;
  }
  .md :global(pre) {
    margin: 0;
    background: var(--n1);
    border: 1px solid var(--border);
    border-top: none;
    border-radius: 0 0 var(--radius) var(--radius);
    padding: 9px 11px;
    overflow-x: auto;
  }
  .md :global(pre code) {
    background: none;
    border: none;
    padding: 0;
  }
  .md :global(.codebar) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 0.7em;
    padding: 3px 6px 3px 11px;
    background: var(--n2);
    border: 1px solid var(--border);
    border-radius: var(--radius) var(--radius) 0 0;
    font: 11px var(--mono);
    color: var(--fg-muted);
  }
  .md :global(.codebar button) {
    font: 11px var(--font);
    background: transparent;
    border-color: transparent;
    color: var(--fg-muted);
    padding: 1px 6px;
  }
  .md :global(.codebar button:hover) {
    background: var(--control-hover);
    color: var(--fg);
  }

  .md :global(table) {
    border-collapse: collapse;
    margin: 0.6em 0;
    display: block;
    overflow-x: auto;
  }
  .md :global(th), .md :global(td) {
    border: 1px solid var(--border);
    padding: 3px 8px;
    text-align: left;
  }
  .md :global(th) { background: var(--n3); }
</style>
