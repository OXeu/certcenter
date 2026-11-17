import { beforeEach, afterEach, describe, expect, it, vi } from 'vite-plus/test'
import { flushPromises, mount } from '@vue/test-utils'
import { notify } from '../src/toast.ts'
import CertificatePage from '../src/CertificatePage.vue'
import { createCertificateWorkspace } from '../src/workspace.ts'
import { createPage } from '../src/page.ts'
import type { PageData } from '../src/data.ts'

vi.mock('../src/toast.ts', () => ({ notify: vi.fn() }))

const data: PageData = {
  page: 'certificates',
  certcenter: true,
  domain: 'example.com',
  lang: 'en',
  title: 'example.com',
  publicUrl: '',
  providerName: '',
  nonce: '',
  csrf: 'csrf',
  loggedIn: true,
  isAdmin: true,
  oauthEnabled: true,
  me: { id: 1, name: 'Admin', email: 'admin@example.com', role: 'admin' },
  project: null,
  env: '',
  canWrite: true,
  languageLabel: 'English',
  languages: [],
  messages: {},
  flash: null,
}
const policy = {
  id: 'main',
  names: ['example.com', '*.example.com'],
  dns_provider: 'dns',
  enabled: true,
  auto_renew: true,
  renew_before_days: 30,
  auto_deploy: true,
  upstreams: ['cloud'],
}
const detail = {
  domain: { name: 'example.com', owner: 'admin@example.com', certificates: [policy] },
  version: 'original',
  certificates: [
    {
      id: 1,
      policy_id: 'main',
      names: policy.names,
      fingerprint: 'fingerprint',
      not_before: '2026-11-01T00:00:00Z',
      not_after: '2027-02-01T00:00:00Z',
    },
  ],
  jobs: [],
  events: [],
  dns_providers: ['dns', 'second-dns'],
  upstreams: ['cloud', 'ssh'],
  delegations: [],
}
const fetchMock = vi.fn()
beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  fetchMock
    .mockReset()
    .mockResolvedValue({ ok: true, status: 200, json: async () => structuredClone(detail) })
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
  }
})
afterEach(() => {
  vi.unstubAllGlobals()
})

describe('certificate management', () => {
  it('persists policy options with CSRF and the revision read before editing', async () => {
    const workspace = createCertificateWorkspace(data, createPage(data))
    await workspace.refresh()
    workspace.editPolicy(policy)
    workspace.form.values.auto_renew = 'false'
    workspace.form.values.renew_before_days = '15'
    await workspace.form.submit()
    const write = fetchMock.mock.calls.find((call) => call[1].method === 'PUT')!
    expect(write[0]).toBe('/api/v1/domains/example.com')
    expect(write[1].headers['X-CSRF-Token']).toBe('csrf')
    expect(JSON.parse(write[1].body)).toMatchObject({
      version: 'original',
      certificates: [{ id: 'main', auto_renew: false, renew_before_days: 15 }],
    })
    expect(workspace.form.open).toBe(false)
  })
  it('defers external operations until the shared confirmation is submitted', async () => {
    const workspace = createCertificateWorkspace(data, createPage(data))
    await workspace.refresh()
    const actions = workspace.actionsFor(policy)
    actions[1].request()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(workspace.form.danger).toBe(true)
    await workspace.form.submit()
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/domains/example.com/certificates/main/deploy')
    expect(fetchMock.mock.calls[1][1].method).toBe('POST')
  })
  it('retains a stale edit and reports its error instead of overwriting a policy', async () => {
    const workspace = createCertificateWorkspace(data, createPage(data))
    await workspace.refresh()
    workspace.editPolicy(policy)
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 409,
      json: async () => ({ error: 'settings changed' }),
    })
    await workspace.form.submit()
    expect(workspace.form.open).toBe(true)
    expect(workspace.form.error).not.toBe('')
  })
  it('renders certificate expiry and edits only inside the shared native dialog', async () => {
    const wrapper = mount(CertificatePage, { props: { data, page: createPage(data) } })
    await flushPromises()
    expect(wrapper.text()).toContain('example.com')
    expect(wrapper.text()).toContain('2027')
    expect(wrapper.find('a[href="/domains"]').exists()).toBe(false)
    expect(wrapper.findAll('input, textarea')).toHaveLength(0)
    const edit = wrapper.findAll('button').find((button) => button.text() === 'Edit')!
    await edit.trigger('click')
    await flushPromises()
    expect(wrapper.find('dialog').element.open).toBe(true)
    expect(wrapper.findAll('dialog input, dialog textarea').length).toBeGreaterThan(0)
    const dns = wrapper.get('[role="combobox"][aria-label="DNS provider"]')
    await dns.trigger('click')
    await wrapper
      .findAll('[role="option"]')
      .find((option) => option.text() === 'second-dns')!
      .trigger('click')
    await wrapper.get('[role="checkbox"][aria-label="ssh"]').trigger('click')
    await wrapper.get('dialog form').trigger('submit')
    await flushPromises()
    const write = fetchMock.mock.calls.find((call) => call[1].method === 'PUT')!
    expect(JSON.parse(write[1].body).certificates[0]).toMatchObject({
      dns_provider: 'second-dns',
      upstreams: ['cloud', 'ssh'],
    })
    wrapper.unmount()
  })
  it('explains how to configure DNS when no account exists', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ ...detail, dns_providers: [] }),
    })
    const workspace = createCertificateWorkspace(data, createPage(data))
    await workspace.refresh()
    workspace.editPolicy()
    expect(workspace.form.open).toBe(false)
    expect(notify).toHaveBeenCalledWith('info', expect.stringContaining('DNS providers page'))
  })
})

it('checks generated CNAME records with CSRF and shows the result', async () => {
  const record = { name: '_acme-challenge.example.com', target: 'random.auth.example.net' }
  fetchMock.mockResolvedValueOnce({
    ok: true,
    status: 200,
    json: async () => ({ ...detail, delegations: [record] }),
  })
  const wrapper = mount(CertificatePage, { props: { data, page: createPage(data) } })
  await flushPromises()
  expect(wrapper.text()).toContain(record.target)
  expect(wrapper.text()).toContain('Not checked')
  fetchMock.mockResolvedValueOnce({
    ok: true,
    status: 200,
    json: async () => ({ delegations: [{ ...record, verified: true }] }),
  })
  await wrapper
    .findAll('button')
    .find((button) => button.text() === 'Check CNAME')!
    .trigger('click')
  await flushPromises()
  expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/domains/example.com/delegations/check')
  expect(fetchMock.mock.calls[1][1].headers['X-CSRF-Token']).toBe('csrf')
  expect(wrapper.text()).toContain('CNAME configured')
  wrapper.unmount()
})
