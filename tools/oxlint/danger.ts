import { parse } from '@vue/compiler-dom'
import { dangerOperations } from '../../web/ui/src/danger.ts'

export function checkDangerTemplate(source: string, filename = ''): string[] {
  const errors: string[] = []
  const trusted = filename === 'web/ui/src/DangerActions.vue'
  const dropdown = filename === 'web/ui/src/DropdownMenu.vue'
  const words =
    /删除|撤销|吊销|拒绝|批准|清空|销毁|退出登录|登出|注销|\b(delete|remove|revoke|reject|approve|review|destroy|purge|(?:log|sign)[\s_-]*(?:out|off))\b|common\.delete|nav\.signOut|projects\.(revoke|approve|reject)/iu
  function walk(node: any, inDropdown = false) {
    if (node.type === 1) {
      const attrs = Object.fromEntries(
        node.props
          .filter((p: any) => p.type === 6)
          .map((p: any) => [p.name, p.value?.content ?? '']),
      )
      const bindings = node.props.filter((p: any) => p.type === 7)
      const marked =
        'data-danger-action' in attrs ||
        bindings.some((p: any) => p.arg?.content === 'data-danger-action')
      if (
        node.tag === 'DangerActions' &&
        !bindings.some(
          (p: any) => p.arg?.content === 'label' && p.exp?.content === "msg('common.more')",
        )
      )
        errors.push('danger-requires-more-trigger')
      const control = ['button', 'a', 'input'].includes(node.tag) || /button$/iu.test(node.tag)
      const dangerous = marked || (control && words.test(node.loc.source))
      const accountSignOut =
        filename === 'web/ui/src/PageHeader.vue' &&
        node.tag === 'button' &&
        attrs['data-danger-action'] === 'signOut'
      if (dangerous && !trusted && !accountSignOut) errors.push('danger-use-shared-actions')
      if (dangerous && (trusted || accountSignOut)) {
        if (!inDropdown) errors.push('danger-requires-dropdown')
        if (!attrs.class?.split(/\s/u).includes('btn-danger')) errors.push('danger-requires-style')
        if (
          !bindings.some(
            (p: any) =>
              p.name === 'on' &&
              p.arg?.content === 'click' &&
              p.exp?.content === (accountSignOut ? 'accountAction.request()' : 'action.request()'),
          )
        )
          errors.push('danger-requires-confirmation')
      }
      if ('data-danger-action' in attrs && !(attrs['data-danger-action'] in dangerOperations))
        errors.push('unknown-danger-operation')
      if (
        !dropdown &&
        (['menu', 'details'].includes(node.tag) ||
          (/menu$/iu.test(node.tag) && node.tag !== 'DropdownMenu') ||
          ['menu', 'menubar'].includes(attrs.role) ||
          /\b(dropdown-content|submenu)\b/u.test(attrs.class ?? '') ||
          ('aria-haspopup' in attrs &&
            !(attrs.role === 'combobox' && attrs['aria-haspopup'] === 'listbox')))
      )
        errors.push('secondary-menu-requires-dropdown')
      inDropdown ||= node.tag === 'DropdownMenu'
    }
    for (const child of node.children ?? []) walk(child, inDropdown)
  }
  walk(parse(source))
  return errors
}
