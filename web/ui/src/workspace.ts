import { dangerAction, dangerOperations } from './danger.ts'
import { reactive } from 'vue'
import { api } from './core.ts'
import { createForm } from './forms.ts'
import { notify } from './toast.ts'
export { createProviderWorkspace } from './providers.ts'
export { createSettingsWorkspace } from './settings.ts'

/** @param {string} value */
const enc = (value: string) => encodeURIComponent(value)
/** @param {string} key @param {string} label @param {boolean} [required] */
const field = (key: string, label: string, required: boolean = true) => ({ key, label, required })

/** @param {import('./data.ts').PageData} data @param {import('./page.ts').PageContext} page */
export function createWorkspace(
  data: import('./data.ts').PageData,
  page: import('./page.ts').PageContext,
) {
  const form = createForm(page)
  const state = reactive({
    rows: /** @type {any[]} */ ([]) as any[],
    environments: /** @type {any[]} */ ([]) as any[],
    tab: data.page === 'keys' ? 'keys' : 'items',
    view: 'all',
    env: data.env,
    busy: false,
    scopeProjects: [] as import('./forms.ts').Choice[],
    scopePermissions: [] as import('./forms.ts').Choice[],
    globalPermissions: [] as string[],
  })
  const base = '/api/v1/projects/' + encodeURIComponent(data.project?.slug ?? '')
  const path = () =>
    state.tab === 'keys'
      ? '/api/v1/keys'
      : data.page === 'settings'
        ? '/api/v1/settings'
        : data.page === 'projects'
          ? '/api/v1/projects'
          : data.page === 'users'
            ? '/api/v1/users'
            : state.tab === 'items'
              ? `${base}/environments/${enc(state.env)}/items`
              : `${base}/${state.tab}`
  /** @returns {Promise<void>} */
  async function load(): Promise<void> {
    state.busy = true
    try {
      if (data.page === 'project')
        state.environments = (await api(`${base}/environments`)).environments
      if (state.tab === 'keys') {
        const options = (await api('/api/v1/key-scopes')) as {
          domains?: { name: string }[]
          all_domains?: boolean
          projects: { slug: string; name: string }[]
          permissions: string[]
          global_permissions: string[]
          all_projects: boolean
        }
        const projects = data.certcenter
          ? (options.domains ?? []).map((d) => ({ slug: d.name, name: d.name }))
          : (options.projects ?? [])
        state.scopeProjects = projects.map((p) => ({
          value: p.slug,
          label: data.certcenter ? p.name : `${p.name} (${p.slug})`,
        }))
        if (data.certcenter ? options.all_domains : options.all_projects)
          state.scopeProjects.push({
            value: '*',
            label: page.msg(data.certcenter ? 'keys.allDomains' : 'keys.allProjects'),
          })
        state.globalPermissions = options.global_permissions ?? []
        state.scopePermissions = (options.permissions ?? []).map((value) => ({
          value,
          label: page.msg(`scope.${value}`),
        }))
      }
      const query =
        data.page === 'projects'
          ? '?view=' + state.view
          : data.page === 'project' && state.tab === 'keys'
            ? '?project=' + enc(data.project!.slug)
            : state.tab === 'proposals'
              ? '?status=pending'
              : ''
      const result = await api(path() + query)
      const collection = ['project', 'keys'].includes(data.page) ? state.tab : data.page
      state.rows = result[collection] ?? []
    } finally {
      state.busy = false
    }
  }
  /** @returns {Promise<void>} */
  async function refresh(): Promise<void> {
    try {
      await load()
    } catch (err) {
      page.report(err)
    }
  }
  /** @param {string} tab */
  async function select(tab: string) {
    if (state.busy) return
    state.tab = tab
    state.rows = []
    await refresh()
  }
  const roles = () => ({
    key: 'role',
    label: 'dialog.role',
    options: ['user', 'admin'].map((value) => ({
      value,
      label: page.msg(`role.${value}`),
    })),
  })
  /** @param {string} url @param {string} method @param {unknown} [body] */
  async function mutate(url: string, method: string, body?: unknown) {
    const result = await api(url, { method, body, csrf: page.csrf })
    notify('success', page.msg('toast.saved'))
    await refresh()
    return result
  }
  /** @param {any} [row] */
  function edit(row?: any) {
    if (data.page === 'settings') {
      form.show(
        page.msg('common.edit'),
        [
          {
            key: 'allowed',
            label: 'settings.registration',
            options: [
              { value: 'true', label: page.msg('settings.allowed') },
              { value: 'false', label: page.msg('settings.closed') },
            ],
          },
        ],
        { allowed: String(row.value) },
        async () => {
          await mutate(path(), 'PUT', { allow_registration: form.values.allowed === 'true' })
        },
      )
      return
    }
    if (state.tab === 'keys') {
      editKey(row)
      return
    }
    const editing = Boolean(row)
    let fields,
      values,
      url = path(),
      method = 'POST'
    if (data.page === 'projects') {
      fields = [
        ...(editing ? [] : [field('slug', 'common.slug')]),
        field('name', 'common.name'),
        field('description', 'common.description', false),
      ]
      values = row ?? { slug: '', name: '', description: '' }
      if (editing) {
        url += '/' + enc(row.slug)
        method = 'PATCH'
      }
    } else if (data.page === 'users') {
      fields = [roles()]
      values = { role: row.role }
      url += '/' + row.id
      method = 'PATCH'
    } else {
      fields = [
        field('key', 'dialog.itemKey'),
        { ...field('value', 'dialog.itemValue', false), multiline: true },
        field('description', 'common.description', false),
      ]
      values = row ?? { key: '', value: '', description: '' }
      method = 'PUT'
    }
    form.show(page.msg(editing ? 'common.edit' : 'common.create'), fields, values, async () => {
      const target = method === 'PUT' ? url + '/' + enc(row?.key ?? form.values.key) : url
      const result = await mutate(target, method, form.values)
      if (result?.token) form.token = result.token
    })
  }
  function editKey(row?: {
    id: number
    name: string
    scopes: { project?: string; domain?: string; permissions: string[] }[]
  }) {
    form.show(
      page.msg(row ? 'common.edit' : 'common.create'),
      [field('name', 'dialog.keyName')],
      { name: row?.name ?? '' },
      async () => {
        const scopes = (form.scopes ?? []).map(({ project, permissions }) => ({
          project,
          permissions: [...permissions],
        }))
        if (scopes.some((scope) => !scope.project || !scope.permissions.length))
          throw new Error(
            page.msg(data.certcenter ? 'keys.domainScopeRequired' : 'keys.scopeRequired'),
            { cause: 422 },
          )
        if (new Set(scopes.map((scope) => scope.project)).size !== scopes.length)
          throw new Error(
            page.msg(data.certcenter ? 'keys.duplicateDomain' : 'keys.duplicateProject'),
            { cause: 422 },
          )
        const url = row ? `/api/v1/keys/${row.id}` : '/api/v1/keys'
        const grants = data.certcenter
          ? scopes.map(({ project, permissions }) => ({ domain: project, permissions }))
          : scopes
        const result = await mutate(url, row ? 'PATCH' : 'POST', {
          name: form.values.name,
          scopes: grants,
        })
        if (result?.token) form.token = result.token
      },
    )
    form.scopeLabel = data.certcenter ? 'keys.scopeDomain' : 'keys.scopeProject'
    form.scopeHelp = data.certcenter ? 'keys.domainScopeHelp' : 'keys.scopeHelp'
    form.scopeProjects = [...state.scopeProjects]
    form.scopePermissions = [...state.scopePermissions]
    form.globalPermissions = [...state.globalPermissions]
    form.scopes = (row?.scopes ?? []).map((scope) => ({
      project: (data.certcenter ? scope.domain : scope.project) ?? '',
      id: form.nextScopeId++,
      permissions: [...scope.permissions],
    }))
    if (!row) {
      form.addScope()
      if (data.project && form.scopes[0]) form.scopes[0].project = data.project.slug
    }
  }
  function removal(row: any) {
    const id =
      data.page === 'users'
        ? 'deleteUser'
        : data.page === 'projects'
          ? 'deleteProject'
          : state.tab === 'keys'
            ? 'revokeKey'
            : 'deleteItem'
    const url = path() + '/' + enc(row.slug ?? row.key ?? String(row.id))
    return dangerAction(
      form,
      id,
      page.msg(dangerOperations[id]),
      row.name ?? row.key ?? row.slug,
      async () => {
        await mutate(url, 'DELETE')
      },
    )
  }
  function proposal(row: any, approve: boolean) {
    const id = approve ? 'approveProposal' : 'rejectProposal'
    return dangerAction(form, id, page.msg(dangerOperations[id]), row.key, async () => {
      await mutate(`${base}/proposals/${row.id}/review`, 'POST', { approve })
    })
  }
  function remove(row: any) {
    removal(row).request()
  }
  function review(row: any, approve: boolean) {
    proposal(row, approve).request()
  }
  async function toggleView() {
    state.view = state.view === 'all' ? 'mine' : 'all'
    await refresh()
  }
  function rotation(row: any) {
    return dangerAction(form, 'rotateKey', page.msg('projects.rotate'), row.name, async () => {
      const result = await mutate(path() + '/' + row.id + '/rotate', 'POST')
      form.token = result.token
    })
  }
  function actionsFor(row: any) {
    if (state.tab === 'proposals') return [proposal(row, true), proposal(row, false)]
    if (state.tab === 'keys') return [rotation(row), removal(row)]
    return [removal(row)]
  }
  /** @returns {void} */
  function environment(): void {
    form.show(
      page.msg('projects.environment'),
      [
        {
          key: 'env',
          label: 'projects.environment',
          options: state.environments.map((e) => ({ value: e.slug, label: e.name })),
        },
      ],
      { env: state.env },
      async () => {
        state.env = form.values.env
        state.rows = []
        await load()
      },
    )
  }
  /** @returns {void} */
  function newEnvironment(): void {
    form.show(
      page.msg('common.newEnvironment'),
      [field('slug', 'common.slug'), field('name', 'common.name')],
      { slug: '', name: '' },
      async () => {
        await mutate(`${base}/environments`, 'POST', form.values)
      },
    )
  }
  return {
    toggleView,
    actionsFor,
    state,
    form,
    refresh,
    select,
    edit,
    remove,
    review,
    environment,
    newEnvironment,
  }
}
export { createCertificateWorkspace } from './certificates.ts'
