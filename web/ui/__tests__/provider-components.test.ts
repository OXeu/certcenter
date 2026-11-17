import { notify } from '../src/toast.ts'
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
  vi.mocked(notify).mockClear()
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
    expect(workspace.form.open).toBe(false)
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
    expect(body.upstream).not.toHaveProperty('update_resources')
    expect(body.upstream).not.toHaveProperty('delete_old')
  })
  it('saves cloud replacement and cleanup options as booleans', async () => {
    const workspace = createProviderWorkspace(data, createPage(data))
    await workspace.refresh()
    workspace.edit({
      ...provider,
      fields: {
        ...provider.fields,
        update_resources: 'true',
        delete_old: 'false',
        contact_ids: '7,8',
      },
    })
    expect(workspace.form.values.update_resources).toBe('true')
    workspace.form.values.delete_old = 'true'
    await workspace.form.submit()
    const write = fetchMock.mock.calls.find((call) => call[1].method === 'PUT')!
    expect(JSON.parse(write[1].body).upstream).toMatchObject({
      kind: 'aliyun',
      update_resources: true,
      delete_old: true,
      contact_ids: '7,8',
    })
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
    expect(notify).toHaveBeenLastCalledWith('error', expect.any(String), 'settings changed')
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
    expect(workspace.form.open).toBe(false)
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/dns-providers')
    expect(JSON.parse(fetchMock.mock.calls[1][1].body).dns).toEqual({
      kind: 'cloudflare',
      api_token: 'cloudflare-secret',
    })
  })
  it('shows managed CNAME validation without account JSON or credential forms', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({
        version: 'revision',
        delegation_zone: 'auth.example.net',
        providers: [{ name: 'certcenter-acme-dns', fields: { kind: 'acme-dns' } }],
      }),
    })
    const dnsData = { ...data, page: 'dns-providers' as const }
    const wrapper = mount(ProviderPage, { props: { data: dnsData, page: createPage(dnsData) } })
    await flushPromises()
    expect(wrapper.text()).toContain('CNAME validation')
    expect(wrapper.findAll('[data-danger-action]').length).toBe(0)
    expect(wrapper.findAll('button').some((button) => button.text() === 'Edit')).toBe(false)
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Add DNS provider')!
      .trigger('click')
    await flushPromises()
    await wrapper.get('[role="combobox"]').trigger('click')
    expect(wrapper.text()).toContain('Cloudflare')
    expect(wrapper.findAll('textarea')).toHaveLength(0)
    expect(
      wrapper.findAll('[role="option"]').some((option) => option.text().includes('acme-dns')),
    ).toBe(false)
    wrapper.unmount()
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
