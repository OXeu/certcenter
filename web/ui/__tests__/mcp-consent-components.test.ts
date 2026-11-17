import { afterEach, beforeEach, expect, it, vi } from 'vite-plus/test'
import { flushPromises, mount } from '@vue/test-utils'
import MCPConsentPage from '../src/MCPConsentPage.vue'
import { createPage } from '../src/page.ts'
import type { PageData } from '../src/data.ts'
import { notify } from '../src/toast.ts'

vi.mock('../src/toast.ts', () => ({ notify: vi.fn() }))
const data = {
  lang: 'en',
  messages: {},
  csrf: 'consent-csrf',
  consent: {
    request: 'pending',
    client: 'Test client',
    resource: 'https://cert.example/mcp',
    redirectUri: 'https://client.example/callback',
  },
} as PageData
const fetchMock = vi.fn()
beforeEach(() => {
  vi.mocked(notify).mockClear()
  vi.stubGlobal('fetch', fetchMock)
  vi.stubGlobal('location', { assign: vi.fn() })
  fetchMock
    .mockReset()
    .mockResolvedValue(
      new Response(
        JSON.stringify({ redirect: 'https://client.example/callback?code=issued&state=state' }),
      ),
    )
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
  }
})
afterEach(() => vi.unstubAllGlobals())

it('requires confirmation and submits a CSRF-bound consent exactly once while busy', async () => {
  let finish!: (response: Response) => void
  fetchMock.mockImplementation(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve
      }),
  )
  const wrapper = mount(MCPConsentPage, { props: { data, page: createPage(data) } })
  expect(wrapper.text()).toContain('Test client')
  expect(wrapper.text()).toContain('https://client.example/callback')
  expect(fetchMock).not.toHaveBeenCalled()
  await wrapper.get('button.btn-primary').trigger('click')
  await flushPromises()
  expect(wrapper.get('dialog').element.open).toBe(true)
  await wrapper.get('dialog form').trigger('submit')
  await wrapper.get('dialog form').trigger('submit')
  await wrapper.get('dialog').trigger('cancel')
  expect(wrapper.get('dialog').element.open).toBe(true)
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(fetchMock).toHaveBeenCalledWith(
    '/oauth/authorize',
    expect.objectContaining({
      method: 'POST',
      headers: expect.objectContaining({ 'X-CSRF-Token': 'consent-csrf' }),
      body: JSON.stringify({ request: 'pending', approve: true }),
    }),
  )
  finish(new Response(JSON.stringify({ redirect: 'https://client.example/callback?code=issued' })))
  await flushPromises()
  expect(location.assign).toHaveBeenCalledWith('https://client.example/callback?code=issued')
  wrapper.unmount()
})

it('allows denial and displays failures only as toasts', async () => {
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify({ error: 'invalid_request' }), { status: 400 }),
  )
  const wrapper = mount(MCPConsentPage, { props: { data, page: createPage(data) } })
  await wrapper.get('button.btn-secondary').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({
    request: 'pending',
    approve: false,
  })
  expect(notify).toHaveBeenCalled()
  expect(wrapper.text()).not.toContain('invalid_request')
  expect(location.assign).not.toHaveBeenCalled()
  expect(wrapper.get('button.btn-secondary').attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})

function cursorConsent() {
  return mount(MCPConsentPage, {
    props: {
      data: {
        ...data,
        consent: { ...data.consent!, redirectUri: 'cursor://anysphere.cursor-mcp/oauth/callback' },
      },
      page: createPage(data),
    },
  })
}

it.each([true, false])('returns a Cursor consent decision: approve=%s', async (approve) => {
  const query = approve ? 'code=issued&state=state' : 'error=access_denied&state=state'
  const redirect = `cursor://anysphere.cursor-mcp/oauth/callback?${query}`
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ redirect })))
  const wrapper = cursorConsent()
  if (approve) {
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    await wrapper.get('dialog form').trigger('submit')
  } else {
    await wrapper.get('button.btn-secondary').trigger('click')
  }
  await flushPromises()
  expect(location.assign).toHaveBeenCalledWith(redirect)
  expect(notify).not.toHaveBeenCalled()
  wrapper.unmount()
})

it.each([
  'cursor://another-app/oauth/callback?code=issued',
  'other-app://anysphere.cursor-mcp/oauth/callback?code=issued',
  'cursor://anysphere.cursor-mcp/other-command?code=issued',
  'cursor://anysphere.cursor-mcp:1234/oauth/callback?code=issued',
  'cursor://user@anysphere.cursor-mcp/oauth/callback?code=issued',
  'cursor://:password@anysphere.cursor-mcp/oauth/callback?code=issued',
  'http://client.example/callback?code=issued',
  '/oauth/client-callback?code=issued',
  'my-app:oauth/callback?code=issued',
])('follows the server-provided callback without URI checks: %s', async (redirect) => {
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ redirect })))
  const wrapper = cursorConsent()
  await wrapper.get('button.btn-primary').trigger('click')
  await flushPromises()
  await wrapper.get('dialog form').trigger('submit')
  await flushPromises()
  expect(location.assign).toHaveBeenCalledWith(redirect)
  expect(notify).not.toHaveBeenCalled()
  wrapper.unmount()
})
