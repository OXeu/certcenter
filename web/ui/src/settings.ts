import { reactive } from 'vue'
import { api } from './core.ts'
import { createForm, type Choice } from './forms.ts'
import { FormValidationError } from './errors.ts'
import { notify } from './toast.ts'
import type { PageContext } from './page.ts'

export const settingsSections = ['general', 'acme', 'dns', 'oauth', 'access', 'database'] as const
export type SettingsSection = (typeof settingsSections)[number]
export type CertificateSettings = {
  server_version: string
  providers_version: string
  restart_required: boolean
  encryption_configured: boolean
  general: { addr: string; public_url: string; session_ttl: string; languages: string[] }
  acme: { directory_url: string; email: string; accept_terms: boolean }
  dns: { zone: string; listen_address: string; nameserver: string; addresses: string[] | null }
  oauth: {
    issuer_url: string
    client_id: string
    scopes: string[] | null
    redirect_url: string
    groups_claim: string
    secret_configured: boolean
  }
  access: {
    allow_registration: boolean
    admin_groups: string[] | null
    mapping: Record<string, string> | null
    bootstrap: { admin_email: string; admin_name: string }
  }
  database: { driver: string; configured: boolean }
}
type Field = {
  key: string
  label: string
  required?: boolean
  secret?: boolean
  help?: string
  multiline?: boolean
  options?: Choice[]
  choices?: Choice[]
}

export function createSettingsWorkspace(page: PageContext) {
  const state = reactive({ busy: false, settings: null as CertificateSettings | null })
  const form = createForm(page)
  const base = '/api/v1/certificate-settings'
  const choice = (key: string): Field => ({
    ...field(key),
    choices: [{ value: 'true', label: page.msg('cert.enabled') }],
  })
  const secret = (key: string): Field => ({
    ...field(key),
    secret: true,
    help: 'providers.keepSecret',
  })
  const multiline = (key: string): Field => ({ ...field(key), multiline: true })
  const fields: Record<SettingsSection, Field[]> = {
    general: [
      field('addr', true),
      field('public_url', true),
      { ...field('session_ttl', true), help: 'settings.durationHelp' },
      {
        ...field('languages'),
        choices: [
          { value: 'en', label: page.msg('settings.english') },
          { value: 'zh-CN', label: page.msg('settings.chinese') },
        ],
      },
    ],
    acme: [field('directory_url', true), field('email', true), choice('accept_terms')],
    dns: [field('zone'), field('listen_address'), field('nameserver'), multiline('addresses')],
    oauth: [
      field('issuer_url'),
      field('client_id'),
      secret('client_secret'),
      multiline('scopes'),
      { ...field('redirect_url'), help: 'settings.redirectHelp' },
      field('groups_claim'),
    ],
    access: [
      choice('allow_registration'),
      multiline('admin_groups'),
      { ...multiline('mapping'), help: 'settings.mappingHelp' },
      field('admin_email', true),
      field('admin_name', true),
    ],
    database: [
      {
        ...field('driver', true),
        options: [
          { value: 'sqlite', label: 'SQLite' },
          { value: 'mysql', label: 'MySQL' },
        ],
      },
      secret('dsn'),
    ],
  }
  async function refresh() {
    if (state.busy || form.open) return
    state.busy = true
    try {
      state.settings = (await api(base)) as CertificateSettings
    } catch (err) {
      page.report(err)
    } finally {
      state.busy = false
    }
  }
  function valuesFor(
    section: SettingsSection,
    settings: CertificateSettings,
  ): Record<string, string> {
    if (section === 'access') {
      const access = settings.access
      return {
        admin_email: access.bootstrap.admin_email,
        admin_name: access.bootstrap.admin_name,
        admin_groups: (access.admin_groups ?? []).join('\n'),
        mapping: Object.entries(access.mapping ?? {})
          .map(([group, role]) => `${group}=${role}`)
          .join('\n'),
      }
    }
    return Object.fromEntries(
      fields[section].map((entry) => {
        const value = (settings[section] as Record<string, unknown>)[entry.key]
        return [
          entry.key,
          entry.secret ? '' : Array.isArray(value) ? value.join('\n') : String(value ?? ''),
        ]
      }),
    )
  }
  function edit(section: SettingsSection) {
    const settings = state.settings
    if (!settings) return
    const version = section === 'acme' ? settings.providers_version : settings.server_version
    form.show(
      page.msg(`settings.${section}`),
      fields[section],
      valuesFor(section, settings),
      async () => {
        const response = (await api(`${base}/${section}`, {
          method: 'PUT',
          csrf: page.csrf,
          body: { version, values: payload(section) },
        })) as CertificateSettings
        state.settings = response
        notify(
          'success',
          page.msg(response.restart_required ? 'settings.savedRestart' : 'toast.saved'),
        )
      },
    )
    if (section === 'general') form.selections.languages = [...settings.general.languages]
    if (section === 'acme')
      form.selections.accept_terms = settings.acme.accept_terms ? ['true'] : []
    if (section === 'access')
      form.selections.allow_registration = settings.access.allow_registration ? ['true'] : []
  }
  function payload(section: SettingsSection): Record<string, unknown> {
    const v = form.values
    switch (section) {
      case 'general':
        if (!form.selections.languages.length)
          throw new FormValidationError(page.msg('settings.languageRequired'))
        return {
          addr: v.addr.trim(),
          public_url: v.public_url.trim(),
          session_ttl: v.session_ttl.trim(),
          languages: [...form.selections.languages],
        }
      case 'acme':
        return {
          directory_url: v.directory_url.trim(),
          email: v.email.trim(),
          accept_terms: form.selections.accept_terms.includes('true'),
        }
      case 'dns':
        return {
          zone: v.zone.trim(),
          listen_address: v.listen_address.trim(),
          nameserver: v.nameserver.trim(),
          addresses: split(v.addresses),
        }
      case 'oauth':
        return {
          issuer_url: v.issuer_url.trim(),
          client_id: v.client_id.trim(),
          client_secret: v.client_secret,
          scopes: split(v.scopes),
          redirect_url: v.redirect_url.trim(),
          groups_claim: v.groups_claim.trim(),
        }
      case 'access':
        return {
          allow_registration: form.selections.allow_registration.includes('true'),
          admin_groups: split(v.admin_groups),
          mapping: mapping(v.mapping, page),
          bootstrap: { admin_email: v.admin_email.trim(), admin_name: v.admin_name.trim() },
        }
      case 'database':
        return { driver: v.driver, dsn: v.dsn }
    }
  }
  function rows(section: SettingsSection) {
    const settings = state.settings
    if (!settings) return []
    const values = valuesFor(section, settings)
    return fields[section].map((entry) => {
      let value = values[entry.key] ?? ''
      if (entry.key === 'accept_terms')
        value = page.msg(settings.acme.accept_terms ? 'cert.yes' : 'cert.no')
      if (entry.key === 'allow_registration')
        value = page.msg(settings.access.allow_registration ? 'cert.yes' : 'cert.no')
      if (entry.secret) {
        const configured =
          entry.key === 'dsn' ? settings.database.configured : settings.oauth.secret_configured
        value = page.msg(configured ? 'settings.configured' : 'settings.notConfigured')
      }
      return { label: entry.label, value: value || page.msg('settings.notConfigured') }
    })
  }
  return { state, form, refresh, edit, rows }
}

function split(value: string) {
  return value
    .split(/[\s,]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}
function mapping(value: string, page: PageContext) {
  const result: Record<string, string> = Object.create(null) as Record<string, string>
  for (const line of value
    .split('\n')
    .map((entry) => entry.trim())
    .filter(Boolean)) {
    const divider = line.lastIndexOf('=')
    const group = line.slice(0, divider).trim()
    const role = line.slice(divider + 1).trim()
    if (divider < 1 || !group || !['admin', 'user'].includes(role) || Object.hasOwn(result, group))
      throw new FormValidationError(page.msg('settings.invalidMapping'))
    result[group] = role
  }
  return result
}

function field(key: string, required = false): Field {
  return { key, label: `settings.${key}`, required }
}
