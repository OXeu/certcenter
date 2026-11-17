import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { flushPromises, mount } from '@vue/test-utils'
import PageHeader from '../src/PageHeader.vue'
import { notify } from '../src/toast.ts'
import { createCopy } from '../src/i18n.ts'
import type { PageData } from '../src/data.ts'

vi.mock('../src/toast.ts', () => ({ notify: vi.fn() }))

const data: PageData = {
  page: 'login',
  title: 'login.title',
  lang: 'en',
  publicUrl: '',
  providerName: '',
  nonce: '',
  csrf: 'sign-out-csrf',
  loggedIn: true,
  isAdmin: false,
  oauthEnabled: true,
  me: { id: 1, name: 'Reader', email: '', role: 'user' },
  project: null,
  env: '',
  canWrite: false,
  languageLabel: 'English',
  languages: [{ tag: 'en', label: 'English' }],
  messages: {},
  flash: null,
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 204 }))
  vi.stubGlobal('location', { assign: vi.fn() })
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
  }
})
afterEach(() => vi.unstubAllGlobals())

function header(lang = 'en') {
  return mount(PageHeader, {
    props: {
      data: { ...data, lang },
      msg: createCopy(lang).msg,
      title: 'Sign in',
      subtitle: '',
      theme: 'light',
      headerOnly: true,
    },
    attachTo: document.body,
  })
}
async function requestSignOut(wrapper: ReturnType<typeof header>) {
  const account = wrapper.get('button[aria-label^="Reader "]')
  if (account.attributes('aria-expanded') === 'false') await account.trigger('click')
  expect(wrapper.find('.dropdown-content button[aria-controls]').exists()).toBe(false)
  await wrapper.get('[data-danger-action="signOut"]').trigger('click')
  await vi.dynamicImportSettled()
  await flushPromises()
}

describe('sign-out confirmation', () => {
  it.each([
    ['en', 'Sign out: Reader'],
    ['zh-CN', '退出登录: Reader'],
  ])('requires confirmation and allows cancellation in %s', async (lang, title) => {
    const wrapper = header(lang)
    expect(wrapper.find('[data-danger-action="signOut"]').exists()).toBe(false)
    await requestSignOut(wrapper)
    const dialog = wrapper.get('dialog')
    expect(dialog.element.open).toBe(true)
    expect(dialog.attributes('aria-label')).toBe(title)
    expect(dialog.get('button[type="submit"]').classes()).toContain('btn-danger')
    expect(fetch).not.toHaveBeenCalled()
    await dialog.get('button[type="button"]').trigger('click')
    expect(wrapper.find('dialog').exists()).toBe(false)
    expect(fetch).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(wrapper.get('button[aria-label^="Reader "]').element)
    wrapper.unmount()
  })

  it('sends one CSRF-protected request after confirmation and prevents cancellation while busy', async () => {
    let finish!: (response: Response) => void
    vi.mocked(fetch).mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve
        }),
    )
    const wrapper = header()
    await requestSignOut(wrapper)
    const dialog = wrapper.get('dialog')
    await dialog.get('form').trigger('submit')
    await dialog.get('form').trigger('submit')
    await dialog.trigger('cancel')
    expect(dialog.element.open).toBe(true)
    expect(dialog.get('button[type="button"]').attributes('disabled')).toBeDefined()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith(
      '/logout',
      expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
        headers: expect.objectContaining({ 'X-CSRF-Token': 'sign-out-csrf' }),
      }),
    )
    finish(new Response(null, { status: 204 }))
    await flushPromises()
    expect(location.assign).toHaveBeenCalledWith('/login')
    expect(wrapper.find('dialog').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps the dialog open after a failed request and allows retrying', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response('{}', { status: 500 }))
    const wrapper = header()
    await requestSignOut(wrapper)
    await wrapper.get('dialog form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('dialog').element.open).toBe(true)
    expect(notify).toHaveBeenLastCalledWith('error', expect.any(String), 'Server error, try again.')
    expect(wrapper.find('dialog .field').exists()).toBe(false)
    expect(location.assign).not.toHaveBeenCalled()
    await wrapper.get('dialog form').trigger('submit')
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(location.assign).toHaveBeenCalledWith('/login')
    wrapper.unmount()
  })
})
