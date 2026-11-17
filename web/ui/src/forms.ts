import { reactive } from 'vue'
import { FormValidationError } from './errors.ts'

export type ScopeDraft = { id: number; project: string; permissions: string[] }
export type Choice = { value: string; label: string }

/** @param {import('./page.ts').PageContext} page */
export function createForm(page: import('./page.ts').PageContext) {
  return reactive({
    open: false,
    danger: false,
    title: '',
    busy: false,
    error: '',
    token: '',
    scopes: null as ScopeDraft[] | null,
    scopeProjects: [] as Choice[],
    scopePermissions: [] as Choice[],
    globalPermissions: [] as string[],
    nextScopeId: 0,
    fields: /** @type {any[]} */ ([]) as any[],
    values: /** @type {Record<string, string>} */ ({}) as Record<string, string>,
    selections: {} as Record<string, string[]>,
    action: /** @type {() => Promise<void>} */ (async () => {}) as () => Promise<void>,
    /** @param {string} title @param {any[]} fields @param {Record<string,string>} values @param {() => Promise<void>} action */
    show(
      title: string,
      fields: any[],
      values: Record<string, string>,
      action: () => Promise<void>,
    ) {
      this.danger = false
      this.title = title
      this.fields = fields
      this.values = { ...values }
      this.selections = Object.fromEntries(
        fields.filter((field) => field.choices).map((field) => [field.key, []]),
      )
      this.action = action
      this.error = ''
      this.token = ''
      this.scopes = null
      this.open = true
    },
    addScope() {
      if (this.busy || this.scopes === null) return
      const project = this.scopeProjects.find(
        (option) => !this.scopes?.some((scope) => scope.project === option.value),
      )
      if (project)
        this.scopes.push({ id: this.nextScopeId++, project: project.value, permissions: [] })
    },
    permissionsFor(scope: ScopeDraft) {
      return this.scopePermissions.filter(
        (permission) => scope.project === '*' || !this.globalPermissions.includes(permission.value),
      )
    },
    limitScope(scope: ScopeDraft) {
      const allowed = new Set(this.permissionsFor(scope).map((permission) => permission.value))
      scope.permissions = scope.permissions.filter((permission) => allowed.has(permission))
    },
    excludeScope(id: number) {
      if (this.busy || this.scopes === null) return
      this.scopes = this.scopes.filter((scope) => scope.id !== id)
    },
    async submit() {
      if (this.busy) return
      this.busy = true
      this.error = ''
      try {
        for (const field of this.fields) {
          if (
            field.required &&
            field.options &&
            !field.options.some((option: Choice) => option.value === this.values[field.key])
          )
            throw new FormValidationError(
              page.msg('form.selectRequired', { Label: page.msg(field.label) }),
            )
        }
        await this.action()
        if (!this.token) this.open = false
      } catch (err) {
        this.error = page.describe(err)
      } finally {
        this.busy = false
      }
    },
  })
}
