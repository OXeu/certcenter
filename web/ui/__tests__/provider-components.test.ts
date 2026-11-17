import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { flushPromises, mount } from '@vue/test-utils'
import ProviderPage from '../src/ProviderPage.vue'
import { createProviderWorkspace } from '../src/workspace.ts'
import { createPage } from '../src/page.ts'
import type { PageData } from '../src/data.ts'

vi.mock('../src/toast.ts', () => ({ notify: vi.fn() }))
const data: PageData = {
  page: 'upstreams',
  certcenter: true,
  lang: 'en',
  title: 'cert.upstreams',
  publicUrl: '',
  providerName: '',
  nonce: '',
  csrf: 'provider-csrf',
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
const provider = { name: 'cloud', fields: { kind: 'aliyun', region: 'cn-hangzhou' } }
const fetchMock = vi.fn()
beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  fetchMock.mockReset().mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => ({ version: 'revision', providers: [provider] }),
  })
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
  }
})
afterEach(() => vi.unstubAllGlobals())

describe('provider management', () => {
  it('creates an SSH upstream using only its relevant fields', async () => {
    const workspace = createProviderWorkspace(data, createPage(data))
    await workspace.refresh()
    workspace.edit()
    Object.assign(workspace.form.values, {
      name: 'nginx',
      kind: 'ssh',
      address: 'host:22',
      user: 'deploy',
      private_key: 'key',
      host_key: 'host-public',
      certificate_path: '/cert.pem',
      key_path: '/key.pem',
      access_key: 'irrelevant',
    })
    await workspace.form.submit()
    expect(workspace.form.error).toBe('')
    const write = fetchMock.mock.calls.find((call) => call[1].method === 'POST')!
    expect(write[0]).toBe('/api/v1/upstreams')
    expect(write[1].headers['X-CSRF-Token']).toBe('provider-csrf')
    const body = JSON.parse(write[1].body)
    expect(body).toMatchObject({
      version: 'revision',
      name: 'nginx',
      upstream: { kind: 'ssh', address: 'host:22', private_key: 'key' },
    })
    expect(body.upstream).not.toHaveProperty('access_key')
  })
  it('retains blank credentials on edit and keeps stale changes for retry', async () => {
    const workspace = createProviderWorkspace(data, createPage(data))
    await workspace.refresh()
    workspace.edit(provider)
    expect(workspace.form.values.secret_key).toBe('')
    expect(workspace.form.fields.find((field) => field.key === 'secret_key').required).toBe(false)
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 409,
      json: async () => ({ error: 'settings changed' }),
    })
    await workspace.form.submit()
    expect(workspace.form.open).toBe(true)
    expect(workspace.form.error).not.toBe('')
    const write = fetchMock.mock.calls[1]
    expect(write[0]).toBe('/api/v1/upstreams/cloud')
    expect(JSON.parse(write[1].body)).toMatchObject({
      version: 'revision',
      upstream: { secret_key: '', access_key: '' },
    })
  })
  it('requires confirmation before deleting an upstream', async () => {
    const workspace = createProviderWorkspace(data, createPage(data))
    await workspace.refresh()
    workspace.actionsFor(provider)[0].request()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(workspace.form.danger).toBe(true)
    await workspace.form.submit()
    expect(fetchMock.mock.calls[1][1].method).toBe('DELETE')
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ version: 'revision' })
  })
  it('creates Cloudflare with an API token and no unrelated credentials', async () => {
    const dnsData = { ...data, page: 'dns-providers' as const }
    const workspace = createProviderWorkspace(dnsData, createPage(dnsData))
    await workspace.refresh()
    workspace.edit()
    Object.assign(workspace.form.values, {
      kind: 'cloudflare',
      name: 'cf',
      api_token: 'cloudflare-secret',
    })
    await workspace.form.submit()
    expect(workspace.form.error).toBe('')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/dns-providers')
    expect(JSON.parse(fetchMock.mock.calls[1][1].body).dns).toEqual({
      kind: 'cloudflare',
      api_token: 'cloudflare-secret',
    })
  })
  it('validates acme-dns JSON without exposing supplied credentials in errors', async () => {
    const dnsData = { ...data, page: 'dns-providers' as const }
    const workspace = createProviderWorkspace(dnsData, createPage(dnsData))
    await workspace.refresh()
    workspace.edit()
    Object.assign(workspace.form.values, {
      kind: 'acme-dns',
      name: 'delegated',
      api_base: 'https://auth.example.com',
      accounts: '{"password":"sensitive',
    })
    await workspace.form.submit()
    expect(workspace.form.error).toBe('Enter registrations as a JSON object.')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    workspace.form.values.accounts =
      '{"example.com":{"username":"user","password":"password","subdomain":"sub","fulldomain":"sub.example.com"}}'
    await workspace.form.submit()
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/dns-providers')
    expect(JSON.parse(fetchMock.mock.calls[1][1].body).dns.accounts['example.com'].username).toBe(
      'user',
    )
  })
  it('shows provider fields in a dialog and changes them with the type selector', async () => {
    const wrapper = mount(ProviderPage, { props: { data, page: createPage(data) } })
    await flushPromises()
    expect(wrapper.text()).toContain('cloud')
    expect(wrapper.findAll('input, textarea, [role="combobox"]')).toHaveLength(0)
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Add upstream')!
      .trigger('click')
    await flushPromises()
    const dialog = wrapper.get('dialog')
    expect(dialog.element.open).toBe(true)
    expect(dialog.findAll('input[type="password"]')).toHaveLength(2)
    await dialog.get('[role="combobox"]').trigger('click')
    await dialog
      .findAll('[role="option"]')
      .find((option) => option.text() === 'SSH / SFTP')!
      .trigger('click')
    expect(dialog.text()).toContain('SSH host:port')
    expect(dialog.text()).not.toContain('Access key secret')
    wrapper.unmount()
  })
})
