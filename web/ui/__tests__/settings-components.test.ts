import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { flushPromises, mount } from '@vue/test-utils'
import CertificateSettingsPage from '../src/CertificateSettingsPage.vue'
import { createSettingsWorkspace } from '../src/workspace.ts'
import { createPage } from '../src/page.ts'
import { notify } from '../src/toast.ts'
import type { CertificateSettings } from '../src/settings.ts'
import type { PageData } from '../src/data.ts'

vi.mock('../src/toast.ts', () => ({ notify: vi.fn() }))
const data: PageData = {
  page: 'certificate-settings',
  certcenter: true,
  lang: 'en',
  title: 'title.settings',
  publicUrl: '',
  providerName: '',
  nonce: '',
  csrf: 'settings-csrf',
  loggedIn: true,
  isAdmin: true,
  oauthEnabled: true,
  me: null,
  project: null,
  env: '',
  canWrite: true,
  languageLabel: 'English',
  languages: [],
  messages: {},
  flash: null,
}
const settings: CertificateSettings = {
  server_version: 'server-original',
  providers_version: 'providers-original',
  restart_required: false,
  encryption_configured: true,
  general: {
    addr: '127.0.0.1:8081',
    public_url: 'https://cert.example.com',
    session_ttl: '24h0m0s',
    languages: ['en', 'zh-CN'],
  },
  acme: {
    directory_url: 'https://ca.example/directory',
    email: 'admin@example.com',
    accept_terms: false,
  },
  dns: { zone: '', listen_address: '', nameserver: '', addresses: [] },
  oauth: {
    issuer_url: 'https://id.example.com',
    client_id: 'client',
    scopes: ['openid', 'email'],
    redirect_url: '',
    groups_claim: 'groups',
    secret_configured: true,
  },
  access: {
    allow_registration: false,
    admin_groups: ['admins'],
    mapping: { staff: 'user' },
    bootstrap: { admin_email: 'admin@example.com', admin_name: 'Admin' },
  },
  database: { driver: 'sqlite', configured: true },
}
const fetchMock = vi.fn()
const response = (body: unknown, status = 200) => ({
  ok: status < 400,
  status,
  json: async () => structuredClone(body),
})
beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  fetchMock.mockReset().mockResolvedValue(response(settings))
  vi.mocked(notify).mockClear()
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
  }
})
afterEach(() => vi.unstubAllGlobals())

describe('editable certificate settings', () => {
  it('edits CNAME DNS settings in a dialog without a ConfigCenter link', async () => {
    const wrapper = mount(CertificateSettingsPage, { props: { page: createPage(data) } })
    await flushPromises()
    expect(wrapper.findAll('input, textarea, [role="combobox"]')).toHaveLength(0)
    expect(wrapper.find('a').exists()).toBe(false)
    await wrapper.get('section[aria-label="CNAME validation"] button').trigger('click')
    await flushPromises()
    const dialog = wrapper.get('dialog')
    expect(dialog.element.open).toBe(true)
    await dialog.get('[id$="-zone"]').setValue('auth.example.com')
    await dialog.get('[id$="-listen_address"]').setValue('0.0.0.0:53')
    await dialog.get('[id$="-nameserver"]').setValue('ns.example.com')
    await dialog.get('[id$="-addresses"]').setValue('192.0.2.1\n2001:db8::1')
    fetchMock.mockResolvedValueOnce(
      response({
        ...settings,
        server_version: 'updated',
        restart_required: true,
        dns: {
          zone: 'auth.example.com',
          listen_address: '0.0.0.0:53',
          nameserver: 'ns.example.com',
          addresses: ['192.0.2.1', '2001:db8::1'],
        },
      }),
    )
    await dialog.get('form').trigger('submit')
    await flushPromises()
    const write = fetchMock.mock.calls[1]
    expect(write[0]).toBe('/api/v1/certificate-settings/dns')
    expect(write[1].headers['X-CSRF-Token']).toBe('settings-csrf')
    expect(JSON.parse(write[1].body)).toEqual({
      version: 'server-original',
      values: {
        zone: 'auth.example.com',
        listen_address: '0.0.0.0:53',
        nameserver: 'ns.example.com',
        addresses: ['192.0.2.1', '2001:db8::1'],
      },
    })
    expect(wrapper.find('dialog').exists()).toBe(false)
    expect(wrapper.text()).toContain('auth.example.com')
    expect(wrapper.text()).toContain('Restart required')
    expect(notify).toHaveBeenCalledWith(
      'success',
      'Saved. Restart the service to apply pending settings.',
    )
    wrapper.unmount()
  })
  it('requires an explicit ACME terms choice and uses the provider version', async () => {
    const wrapper = mount(CertificateSettingsPage, { props: { page: createPage(data) } })
    await flushPromises()
    await wrapper.get('section[aria-label="Certificate authority"] button').trigger('click')
    await flushPromises()
    const terms = wrapper.get('dialog [role="checkbox"]')
    expect(terms.attributes('aria-checked')).toBe('false')
    await terms.trigger('click')
    await wrapper.get('dialog form').trigger('submit')
    await flushPromises()
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toMatchObject({
      version: 'providers-original',
      values: { accept_terms: true },
    })
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/certificate-settings/acme')
    wrapper.unmount()
  })
  it('opens blank secret fields and submits only the selected section', async () => {
    const workspace = createSettingsWorkspace(createPage(data))
    await workspace.refresh()
    workspace.edit('oauth')
    expect(workspace.form.values.client_secret).toBe('')
    workspace.form.values.client_id = 'new-client'
    await workspace.form.submit()
    const body = JSON.parse(fetchMock.mock.calls[1][1].body)
    expect(body.values.client_secret).toBe('')
    expect(body.values.client_id).toBe('new-client')
    expect(body).not.toHaveProperty('acme')
    expect(body.values).not.toHaveProperty('encryption_key')
    workspace.edit('database')
    expect(workspace.form.values.dsn).toBe('')
  })
  it.each([409, 503])(
    'retains a failed edit and allows cancellation after HTTP %i',
    async (status) => {
      const workspace = createSettingsWorkspace(createPage(data))
      await workspace.refresh()
      workspace.edit('general')
      workspace.form.values.addr = '127.0.0.1:9090'
      fetchMock.mockResolvedValueOnce(response({ error: 'settings unavailable' }, status))
      await workspace.form.submit()
      expect(workspace.form.open).toBe(true)
      expect(workspace.form.busy).toBe(false)
      expect(workspace.form.values.addr).toBe('127.0.0.1:9090')
      expect(notify).toHaveBeenLastCalledWith('error', expect.any(String), 'settings unavailable')
      expect(workspace.state.settings?.general.addr).toBe('127.0.0.1:8081')
    },
  )
  it('validates languages and group mappings before sending a request', async () => {
    const workspace = createSettingsWorkspace(createPage(data))
    await workspace.refresh()
    workspace.edit('general')
    workspace.form.selections.languages = []
    await workspace.form.submit()
    expect(notify).toHaveBeenLastCalledWith(
      'error',
      expect.any(String),
      'Select at least one language.',
    )
    expect(fetchMock).toHaveBeenCalledTimes(1)
    workspace.edit('access')
    workspace.form.values.mapping = 'staff=owner'
    await workspace.form.submit()
    expect(notify).toHaveBeenLastCalledWith(
      'error',
      expect.any(String),
      expect.stringContaining('group=admin'),
    )
    expect(fetchMock).toHaveBeenCalledTimes(1)
    workspace.form.values.mapping = 'staff=admin\nreaders=user'
    workspace.form.selections.allow_registration = ['true']
    await workspace.form.submit()
    expect(JSON.parse(fetchMock.mock.calls[1][1].body).values).toMatchObject({
      allow_registration: true,
      mapping: { staff: 'admin', readers: 'user' },
    })
  })
  it('prevents duplicate saves and cancellation while busy', async () => {
    const wrapper = mount(CertificateSettingsPage, { props: { page: createPage(data) } })
    await flushPromises()
    await wrapper.get('section[aria-label="Service"] button').trigger('click')
    await flushPromises()
    let finish!: (value: unknown) => void
    fetchMock.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const dialog = wrapper.get('dialog')
    await dialog.get('form').trigger('submit')
    await dialog.get('form').trigger('submit')
    await dialog.trigger('cancel')
    expect(dialog.element.open).toBe(true)
    expect(dialog.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    finish(response(settings))
    await flushPromises()
    expect(wrapper.find('dialog').exists()).toBe(false)
    wrapper.unmount()
  })
})
