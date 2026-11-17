import { reactive } from 'vue'
import { api } from './core.ts'
import { createForm } from './forms.ts'
import { dangerAction } from './danger.ts'
import { notify } from './toast.ts'
import type { PageData } from './data.ts'
import type { PageContext } from './page.ts'

export type Policy = {
  id: string
  names: string[]
  dns_provider: string
  enabled: boolean
  auto_renew: boolean
  renew_before_days: number
  auto_deploy: boolean
  upstreams: string[]
}
export type Certificate = {
  id: number
  policy_id: string
  names: string[]
  fingerprint: string
  not_before: string
  not_after: string
}
export type Job = {
  id: number
  policy_id: string
  action: string
  status: string
  created_at: string
}
export type Event = {
  id: number
  job_id: number
  policy_id: string
  stage: string
  target: string
  message: string
  created_at: string
}
type DomainView = {
  domain: { name: string; owner: string; certificates: Policy[] | null }
  version: string
  certificates: Certificate[]
  jobs: Job[]
  events: Event[]
  dns_providers: string[]
  upstreams: string[]
  delegations: { name: string; target: string }[]
}

export function createCertificateWorkspace(data: PageData, page: PageContext) {
  const state = reactive({
    busy: false,
    checking: false,
    checks: {} as Record<string, boolean>,
    domains: [] as { name: string; owner: string; count: number }[],
    detail: null as DomainView | null,
    tab: 'policies',
  })
  const form = createForm(page)
  const base = `/api/v1/domains/${encodeURIComponent(data.domain ?? '')}`
  async function refresh() {
    if (state.busy || form.open) return
    state.busy = true
    try {
      if (data.page === 'domains') {
        const response = (await api('/api/v1/domains')) as { domains: typeof state.domains }
        state.domains = response.domains
      } else state.detail = (await api(base)) as DomainView
    } catch (err) {
      page.report(err)
    } finally {
      state.busy = false
    }
  }
  function boolean(key: string, label: string) {
    return {
      key,
      label,
      options: [
        { value: 'true', label: page.msg('cert.yes') },
        { value: 'false', label: page.msg('cert.no') },
      ],
    }
  }
  function createDomain() {
    form.show(
      page.msg('cert.addDomain'),
      [field('name', 'cert.domain'), field('owner', 'cert.owner')],
      { name: '', owner: data.me?.email ?? '' },
      async () => {
        const created = (await api('/api/v1/domains', {
          method: 'POST',
          csrf: page.csrf,
          body: { ...form.values, certificates: [] },
        })) as { name: string }
        location.assign(`/domains/${encodeURIComponent(created.name)}`)
      },
    )
  }
  function editPolicy(policy?: Policy) {
    const detail = state.detail
    if (!detail) return
    const original = detail.domain.certificates ?? []
    const version = detail.version
    const options = detail.dns_providers.map((value) => ({ value, label: providerLabel(value) }))
    if (!options.length) {
      notify('info', page.msg('cert.noDNSProviders'))
      return
    }
    form.show(
      page.msg(policy ? 'common.edit' : 'cert.addCertificate'),
      [
        ...(policy ? [] : [field('id', 'cert.policy')]),
        { ...field('names', 'cert.names'), multiline: true },
        { key: 'dns_provider', label: 'cert.dnsProvider', options, required: true },
        boolean('enabled', 'cert.enabled'),
        boolean('auto_renew', 'cert.autoRenew'),
        field('renew_before_days', 'cert.renewBefore'),
        boolean('auto_deploy', 'cert.autoDeploy'),
        {
          key: 'upstreams',
          label: 'cert.upstreams',
          choices: detail.upstreams.map((value) => ({ value, label: value })),
        },
      ],
      {
        id: policy?.id ?? '',
        names: policy?.names.join('\n') ?? detail.domain.name,
        dns_provider:
          policy?.dns_provider ??
          options.find((option) => option.value === 'certcenter-acme-dns')?.value ??
          options[0]?.value ??
          '',
        enabled: String(policy?.enabled ?? true),
        auto_renew: String(policy?.auto_renew ?? false),
        renew_before_days: String(policy?.renew_before_days ?? 30),
        auto_deploy: String(policy?.auto_deploy ?? false),
        upstreams: policy?.upstreams?.join(', ') ?? '',
      },
      async () => {
        const values = form.values
        const updated: Policy = {
          id: policy?.id ?? values.id.trim(),
          names: split(values.names),
          dns_provider: values.dns_provider,
          enabled: values.enabled === 'true',
          auto_renew: values.auto_renew === 'true',
          renew_before_days: Number(values.renew_before_days),
          auto_deploy: values.auto_deploy === 'true',
          upstreams: [...form.selections.upstreams],
        }
        const certificates = policy
          ? original.map((entry) => (entry.id === policy.id ? updated : entry))
          : [...original, updated]
        await api(base, { method: 'PUT', csrf: page.csrf, body: { version, certificates } })
        state.detail = (await api(base)) as DomainView
        notify('success', page.msg('toast.saved'))
      },
    )
    form.selections.upstreams = [...(policy?.upstreams ?? [])]
  }
  async function checkDelegations() {
    if (state.checking) return
    state.checking = true
    try {
      const result = (await api(`${base}/delegations/check`, {
        method: 'POST',
        csrf: page.csrf,
      })) as { delegations: { name: string; target: string; verified: boolean }[] }
      state.checks = Object.fromEntries(
        result.delegations.map((record) => [record.name + record.target, record.verified]),
      )
      notify('info', page.msg('cert.delegationChecked'))
    } catch (err) {
      page.report(err)
    } finally {
      state.checking = false
    }
  }
  function providerLabel(name: string) {
    return name === 'certcenter-acme-dns' ? page.msg('providers.acme-dns') : name
  }
  function delegationStatus(record: { name: string; target: string }) {
    const checked = state.checks[record.name + record.target]
    return page.msg(
      checked === undefined ? 'cert.notChecked' : checked ? 'cert.cnameReady' : 'cert.cnamePending',
    )
  }
  function actionsFor(policy: Policy) {
    const makeAction = (action: 'issue' | 'deploy') =>
      dangerAction(
        form,
        action === 'issue' ? 'issueCertificate' : 'deployCertificate',
        page.msg(action === 'issue' ? 'cert.issue' : 'cert.deploy'),
        policy.id,
        async () => {
          await api(`${base}/certificates/${encodeURIComponent(policy.id)}/${action}`, {
            method: 'POST',
            csrf: page.csrf,
          })
          state.detail = (await api(base)) as DomainView
          notify('success', page.msg('cert.queued'))
        },
      )
    return [makeAction('issue'), ...(policy.upstreams?.length ? [makeAction('deploy')] : [])]
  }
  function pending(policy: Policy) {
    return (
      !policy.enabled ||
      state.busy ||
      Boolean(
        state.detail?.jobs.some(
          (job) => job.policy_id === policy.id && ['queued', 'running'].includes(job.status),
        ),
      )
    )
  }
  function latest(policy: Policy) {
    return state.detail?.certificates.find((certificate) => certificate.policy_id === policy.id)
  }
  function validity(policy: Policy) {
    const cert = latest(policy)
    return cert ? new Date(cert.not_after).toLocaleString(data.lang) : page.msg('cert.notIssued')
  }
  function status(policy: Policy) {
    const cert = latest(policy)
    if (!cert) return page.msg('cert.notIssued')
    const remaining = new Date(cert.not_after).getTime() - Date.now()
    if (remaining <= 0) return page.msg('cert.expired')
    return page.msg('cert.daysLeft', { Days: String(Math.ceil(remaining / 86_400_000)) })
  }
  return {
    state,
    form,
    refresh,
    createDomain,
    editPolicy,
    actionsFor,
    pending,
    validity,
    status,
    checkDelegations,
    delegationStatus,
    providerLabel,
  }
}

function split(value: string) {
  return value
    .split(/[\s,]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function field(key: string, label: string, required = true) {
  return { key, label, required }
}
