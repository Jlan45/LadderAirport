/** Copy text on HTTPS and on plain-HTTP panel deployments. */
export async function copyText(text: string): Promise<void> {
  if (!text) throw new Error('没有可复制的内容')

  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // Clipboard API is commonly blocked on plain HTTP; use the selection fallback.
    }
  }

  const input = document.createElement('textarea')
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null
  input.value = text
  input.readOnly = true
  input.setAttribute('aria-hidden', 'true')
  Object.assign(input.style, {
    position: 'fixed',
    inset: '0 auto auto -9999px',
    opacity: '0',
    pointerEvents: 'none',
  })
  document.body.appendChild(input)
  let copied = false
  try {
    input.select()
    input.setSelectionRange(0, input.value.length)
    copied = document.execCommand('copy')
  } finally {
    input.remove()
    try {
      active?.focus({ preventScroll: true })
    } catch {
      // The original element may have disappeared while copying.
    }
  }
  if (!copied) throw new Error('浏览器拒绝了复制操作')
}
