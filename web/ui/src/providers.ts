import { reactive } from 'vue'
import { api } from './core.ts'
import { createForm } from './forms.ts'
import { dangerAction } from './danger.ts'
import { notify } from './toast.ts'
import type { PageData } from './data.ts'
import type { PageContext } from './page.ts'

export type Provider = { name: string; fields: Record<string, string> }
type Registry = { version: string; providers: Provider[]; delegation_zone?: string }

export function createProviderWorkspace(data: PageData, page: PageContext) {
  const dns = data.page === 'dns-providers'
  const base = dns ? '/api/v1/dns-providers' : '/api/v1/upstreams'
  const state = reactive({
    busy: false,
    version: '',
    delegationZone: '',
    providers: [] as Provider[],
  })
  const form = createForm(page)
  const kinds = dns ? ['alidns', 'tencentcloud', 'cloudflare'] : ['aliyun', 'tencentcloud', 'ssh']
  async function load() {
    const result = (await api(base)) as Registry
    state.version = result.version
    state.delegationZone = result.delegation_zone ?? ''
    state.providers = result.providers
  }
  async function refresh() {
    if (state.busy || form.open) return
    state.busy = true
    try {
      await load()
    } catch (err) {
      page.report(err)
    } finally {
      state.busy = false
    }
  }
  function edit(provider?: Provider) {
    const version = state.version
    const cloud = dns ? ['alidns', 'tencentcloud'] : ['aliyun', 'tencentcloud']
    const makeField = (
      key: string,
      visibleKinds: string[],
      required = true,
      secret = false,
      multiline = false,
    ) => ({
      key,
      label: `providers.${key}`,
      kinds: visibleKinds,
      help: secret && provider ? 'providers.keepSecret' : '',
      required: required && !(secret && provider),
      secret,
      multiline,
    })
    const fields = [
      ...(!provider ? [{ key: 'name', label: 'providers.name', required: true }] : []),
      {
        key: 'kind',
        label: 'providers.kind',
        required: true,
        readonly: Boolean(provider),
        options: kinds.map((value) => ({ value, label: page.msg(`providers.${value}`) })),
      },
      makeField('access_key', cloud, true, true),
      makeField('secret_key', cloud, true, true),
      makeField('region', cloud, false),
      ...(dns
        ? [
            {
              ...makeField('api_token', ['cloudflare'], true, true),
              help: provider ? 'providers.keepSecret' : 'providers.cloudflareHelp',
            },
          ]
        : [
            makeField('address', ['ssh']),
            makeField('user', ['ssh']),
            makeField('private_key', ['ssh'], true, true, true),
            makeField('host_key', ['ssh']),
            makeField('certificate_path', ['ssh']),
            makeField('key_path', ['ssh']),
            makeField('reload_command', ['ssh'], false),
          ]),
    ]
    const values = Object.fromEntries(
      fields.map((field) => [field.key, provider?.fields[field.key] ?? '']),
    )
    values.kind = provider?.fields.kind ?? kinds[0]
    form.show(
      page.msg(provider ? 'common.edit' : dns ? 'providers.addDNS' : 'providers.addUpstream'),
      fields,
      values,
      async () => {
        const settings: Record<string, unknown> = { kind: form.values.kind }
        for (const field of fields) {
          if ('kinds' in field && field.kinds.includes(form.values.kind)) {
            const value = form.values[field.key]
            settings[field.key] = value
          }
        }
        const name = provider?.name ?? form.values.name.trim()
        await api(provider ? `${base}/${encodeURIComponent(name)}` : base, {
          method: provider ? 'PUT' : 'POST',
          csrf: page.csrf,
          body: { version, name, [dns ? 'dns' : 'upstream']: settings },
        })
        await load()
        notify('success', page.msg('toast.saved'))
      },
    )
  }
  function editable(provider: Provider) {
    return provider.fields.kind !== 'acme-dns'
  }
  function actionsFor(provider: Provider) {
    if (provider.name === 'certcenter-acme-dns') return []
    const version = state.version
    return [
      dangerAction(
        form,
        dns ? 'deleteDNSProvider' : 'deleteUpstream',
        page.msg('common.delete'),
        provider.name,
        async () => {
          await api(`${base}/${encodeURIComponent(provider.name)}`, {
            method: 'DELETE',
            csrf: page.csrf,
            body: { version },
          })
          await load()
          notify('success', page.msg('toast.saved'))
        },
      ),
    ]
  }
  return { state, form, refresh, edit, actionsFor, editable }
}
