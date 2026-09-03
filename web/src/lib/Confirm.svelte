<script>
  import { trapFocus } from './focus.js'

  // 取り消せない操作の前に挟む。開いた時点で「取りやめ」に焦点を置き、
  // 続けて Enter を押しても実行されないようにする。
  let { title, body = '', note = '', confirmLabel = '実行する', onConfirm, onCancel } = $props()

  let cancelBtn = $state(null)

  $effect(() => {
    cancelBtn?.focus()
  })
</script>

<div
  class="veil"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onCancel()
  }}
>
  <div class="box" role="alertdialog" aria-modal="true" aria-label={title} use:trapFocus>
    <h2>{title}</h2>
    {#if body}<p class="body">{body}</p>{/if}
    {#if note}<p class="note">{note}</p>{/if}
    <div class="acts">
      <button bind:this={cancelBtn} onclick={onCancel}>取りやめ</button>
      <button class="danger" onclick={onConfirm}>{confirmLabel}</button>
    </div>
  </div>
</div>

<style>
  .veil {
    position: fixed;
    inset: 0;
    background: rgb(0 0 0 / 0.55);
    display: grid;
    place-items: center;
    z-index: 20;
  }
  .box {
    width: min(26rem, calc(100vw - 3rem));
    background: var(--g2);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    padding: 16px 18px;
    box-shadow: 0 12px 40px rgb(0 0 0 / 0.5);
  }
  h2 {
    margin: 0 0 8px;
    font-size: 14px;
  }
  .body {
    margin: 0 0 6px;
    color: var(--fg);
    overflow-wrap: anywhere;
  }
  .note {
    margin: 0;
    color: var(--fg-muted);
    font-size: 12px;
  }
  .acts {
    display: flex;
    justify-content: flex-end;
    gap: 7px;
    margin-top: 14px;
  }
  /* 取り消せない操作。白い文字が 4.5:1 を満たす濃さまで落としてある。 */
  .danger {
    background: var(--danger-solid);
    border-color: var(--danger-solid);
    color: #fff;
  }
  .danger:hover {
    background: var(--danger-solid-hover);
    border-color: var(--danger-solid-hover);
    color: #fff;
  }
</style>
