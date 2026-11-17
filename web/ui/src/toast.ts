import { injectStyles, toast } from '@yusufceng1z/ding'

import { createCopy } from './i18n.ts'

let mounted = false

/**
 * Configures the shared Vue toast viewport and its CSP-compatible stylesheet.
 * @param {string} lang
 * @param {string} [nonce]
 * @param {import('./theme.ts').Theme} [theme]
 * @returns {void}
 */
export function mountToasts(
  lang: string,
  nonce: string = '',
  theme: import('./theme.ts').Theme = 'dark',
): void {
  const { msg } = createCopy(lang)
  if (!mounted) {
    mounted = true
    injectStyles(document, { nonce })
  }
  toast.configure({
    labels: { region: msg('toast.region'), dismiss: msg('toast.dismiss') },
    position: 'bottom-right',
    held: 'queue',
    theme,
  })
}

/**
 * Shows a message as a toast instead of embedding it in the page.
 * @param {'success' | 'error' | 'warning' | 'info' | 'loading'} type
 * @param {string} title
 * @param {string} [description]
 * @returns {string} the toast id
 */
export function notify(
  type: 'success' | 'error' | 'warning' | 'info' | 'loading',
  title: string,
  description?: string,
): string {
  if (!mounted) mountToasts('en')
  const options = description ? { description } : {}
  if (type === 'loading') return toast.loading(title, options)
  return toast[type](title, options)
}

/** @param {import('./theme.ts').Theme} theme */
export function setToastTheme(theme: import('./theme.ts').Theme) {
  toast.configure({ theme })
}
