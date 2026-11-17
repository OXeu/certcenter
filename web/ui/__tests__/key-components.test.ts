import { beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { flushPromises, mount } from '@vue/test-utils'
import App from '../src/App.vue'
import { notify } from '../src/toast.ts'
import type { PageData } from '../src/data.ts'

vi.mock('../src/toast.ts', () => ({
  notify: vi.fn(),
  mountToasts: vi.fn(),
  setToastTheme: vi.fn(),
}))

const data: PageData = {
  page: 'keys',
  title: 'title.keys',
  lang: 'en',
  publicUrl: '',
  providerName: '',
  nonce: '',
  csrf: 'csrf',
  loggedIn: true,
  isAdmin: false,
  oauthEnabled: true,
  me: { id: 2, name: 'Owner', email: 'owner@example.com', role: 'user' },
  project: null,
  env: '',
  canWrite: true,
  languageLabel: 'English',
  languages: [],
  messages: {},
  flash: null,
}
const fetchMock = vi.fn()
beforeEach(() => {
  localStorage.clear()
  vi.stubGlobal('fetch', fetchMock)
  fetchMock.mockReset().mockImplementation(async (url, options) => ({
    ok: true,
    status: 200,
    json: async () =>
      url === '/api/v1/key-scopes'
        ? {
            projects: [
              { slug: 'billing', name: 'Billing' },
              { slug: 'shipping', name: 'Shipping' },
            ],
            permissions: ['config:read', 'config:write', 'config:delete'],
            all_projects: false,
          }
        : options?.method === 'POST'
          ? { token: 'cc_once' }
          : {
              keys: [
                {
                  id: 7,
                  name: 'Service',
                  prefix: 'cc_example',
                  scopes: [{ project: 'billing', permissions: ['config:read'] }],
                },
              ],
            },
  }))
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
  }
})

async function settled() {
  await vi.dynamicImportSettled()
  await flushPromises()
}

describe('API key scope editor', () => {
  it('offers global permissions only for wildcard scopes and clears them when switching projects', async () => {
    fetchMock.mockImplementationOnce(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        projects: [{ slug: 'billing', name: 'Billing' }],
        permissions: ['config:read', 'users:read', 'settings:write'],
        global_permissions: ['users:read', 'settings:write'],
        all_projects: true,
      }),
    }))
    const wrapper = mount(App, { props: { data: { ...data, isAdmin: true } } })
    await settled()
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Edit')!
      .trigger('click')
    await settled()
    const dialog = wrapper.get('dialog')
    expect(dialog.find('[role="checkbox"][value="users:read"]').exists()).toBe(false)
    await dialog.get('[role="combobox"]').trigger('click')
    await dialog
      .findAll('[role="option"]')
      .find((option) => option.text() === 'All projects')!
      .trigger('click')
    await dialog.get('[role="checkbox"][value="users:read"]').trigger('click')
    await dialog.get('[role="checkbox"][value="settings:write"]').trigger('click')
    await dialog.get('[role="combobox"]').trigger('click')
    await dialog
      .findAll('[role="option"]')
      .find((option) => option.text() === 'Billing (billing)')!
      .trigger('click')
    expect(dialog.find('[role="checkbox"][value="users:read"]').exists()).toBe(false)
    await dialog.get('form').trigger('submit')
    await settled()
    const request = fetchMock.mock.calls.find((call) => call[1]?.method === 'PATCH')!
    expect(JSON.parse(request[1].body).scopes).toEqual([
      { project: 'billing', permissions: ['config:read'] },
    ])
    wrapper.unmount()
  })

  it('edits multiple project scopes with independent permissions', async () => {
    const wrapper = mount(App, { props: { data } })
    await settled()
    expect(wrapper.get('tbody').text()).toContain('billing: Read configuration')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Edit')!
      .trigger('click')
    await settled()
    const dialog = wrapper.get('dialog')
    expect(dialog.find('select').exists()).toBe(false)
    expect(dialog.get('[role="combobox"]').text()).toBe('Billing (billing)')
    expect(dialog.get('[role="checkbox"][value="config:read"]').attributes('aria-checked')).toBe(
      'true',
    )
    await dialog.get('[role="checkbox"][value="config:write"]').trigger('click')
    expect(dialog.get('[role="checkbox"][value="config:delete"]').attributes('aria-checked')).toBe(
      'false',
    )
    await dialog
      .findAll('button')
      .find((button) => button.text() === 'Add scope')!
      .trigger('click')
    const scopes = dialog.findAll('fieldset')
    expect(scopes).toHaveLength(2)
    expect(scopes[1].get('[role="combobox"]').text()).toBe('Shipping (shipping)')
    await scopes[1].get('[role="checkbox"][value="config:read"]').trigger('click')
    await dialog.get('form').trigger('submit')
    await settled()
    const request = fetchMock.mock.calls.find((call) => call[1]?.method === 'PATCH')!
    expect(request[0]).toBe('/api/v1/keys/7')
    expect(JSON.parse(request[1].body)).toEqual({
      name: 'Service',
      scopes: [
        { project: 'billing', permissions: ['config:read', 'config:write'] },
        { project: 'shipping', permissions: ['config:read'] },
      ],
    })
    wrapper.unmount()
  })

  it('validates empty grants and hides wildcard projects for regular users', async () => {
    const wrapper = mount(App, { props: { data } })
    await settled()
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Create')!
      .trigger('click')
    await settled()
    const dialog = wrapper.get('dialog')
    await dialog.get('input:not([type="checkbox"])').setValue('Reader')
    await dialog.get('[role="combobox"]').trigger('click')
    expect(dialog.findAll('[role="option"]').map((option) => option.text())).toEqual([
      'Billing (billing)',
      'Shipping (shipping)',
    ])
    await dialog.get('[role="combobox"]').trigger('keydown', { key: 'Escape' })
    await dialog.get('form').trigger('submit')
    await settled()
    expect(notify).toHaveBeenLastCalledWith(
      'error',
      expect.any(String),
      'Choose a project and at least one permission for each scope.',
    )
    expect(dialog.text()).not.toContain(
      'Choose a project and at least one permission for each scope.',
    )
    expect(fetchMock.mock.calls.some((call) => call[1]?.method === 'POST')).toBe(false)
    await dialog.get('[role="checkbox"][value="config:read"]').trigger('click')
    await dialog.get('form').trigger('submit')
    await settled()
    expect(dialog.text()).toContain('cc_once')
    expect(dialog.element.open).toBe(true)
    expect(dialog.findAll('[role="checkbox"]')).toHaveLength(0)
    wrapper.unmount()
  })
})
