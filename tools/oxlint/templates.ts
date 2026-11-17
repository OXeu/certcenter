import { parse } from '@vue/compiler-dom'
import { overridesDropdownPosition } from './dropdown.ts'
import { formFeedback } from './form-feedback.ts'

export function checkTemplate(source: string, filename = '', script = ''): string[] {
  const errors: string[] = []
  const feedback = formFeedback(script)
  const emoji = /\p{Extended_Pictographic}/u
  /** @param {any} node @param {boolean} dialog */
  function walk(node: any, dialog: boolean = false, field: boolean = false) {
    if (node.type === 5 && feedback(node.content.content, field))
      errors.push('form-feedback-use-toast')
    if (node.type === 2 && emoji.test(node.content)) errors.push('inline-icon')
    if (node.type === 1) {
      const attrs = Object.fromEntries(
        node.props
          .filter((p: any) => p.type === 6)
          .map((p: any) => [p.name, p.value?.content ?? '']),
      )
      dialog ||=
        node.tag === 'dialog' ||
        ['dialog', 'alertdialog'].includes(attrs.role) ||
        attrs['aria-modal'] === 'true'
      if (
        [
          'input',
          'select',
          'textarea',
          'DropdownSelect',
          'dropdown-select',
          'CheckboxField',
          'checkbox-field',
        ].includes(node.tag) &&
        !dialog
      )
        errors.push('input-outside-dialog')
      const element = node.tag.toLowerCase()
      const dynamicTag = node.props.find((p: any) => p.arg?.content === 'is')?.exp?.content
      const input =
        element === 'input' ||
        element === 'component' ||
        attrs.is?.toLowerCase() === 'input' ||
        /^['"]input['"]$/iu.test(dynamicTag ?? '')
      const boundType = node.props.find((p: any) => p.arg?.content === 'type')
      const literalType = boundType?.exp?.content.match(/^(['"])([^'"]*)\1$/u)?.[2]
      const spread = node.props.some((p: any) => p.type === 7 && p.name === 'bind' && !p.arg)
      if (
        input &&
        (attrs.type?.toLowerCase() === 'checkbox' ||
          (boundType && (literalType === undefined || literalType.toLowerCase() === 'checkbox')) ||
          spread)
      )
        errors.push('no-native-checkbox: use CheckboxField instead')
      if (attrs.role === 'checkbox' && filename !== 'web/ui/src/CheckboxField.vue')
        errors.push('checkbox-use-shared-component')
      const classes = (attrs.class ?? '').split(/\s+/u)
      field ||= dialog && classes.includes('field')
      if (/^(?:Alert|Notification|FormFeedback|SubmitResult|SubmissionResult)$/iu.test(node.tag))
        errors.push('form-feedback-use-toast')
      if (
        /\b(?:form|submit|submission)-(?:error|success|result|feedback)\b/u.test(attrs.class ?? '')
      )
        errors.push('form-feedback-use-toast')
      const panel =
        classes.some((name: string) =>
          ['dropdown-panel', 'dropdown-content', 'dropdown-options'].includes(name),
        ) || ['listbox', 'menu'].includes(attrs.role)
      if (panel) {
        if (!['web/ui/src/DropdownMenu.vue', 'web/ui/src/DropdownSelect.vue'].includes(filename))
          errors.push('dropdown-use-shared-component')
        if (!classes.includes('dropdown-panel')) errors.push('dropdown-requires-overlay')
        if (
          overridesDropdownPosition(attrs.class ?? '') ||
          attrs.style !== undefined ||
          spread ||
          node.props.some(
            (p: any) =>
              p.type === 7 &&
              (p.arg?.content === 'class' ||
                (p.arg?.content === 'style' && p.exp?.content !== 'position')),
          )
        )
          errors.push('dropdown-no-position-override')
      }
      if (
        ['select', 'option', 'optgroup', 'selector'].includes(element) ||
        attrs.is?.toLowerCase() === 'select' ||
        /^['"]select['"]$/iu.test(dynamicTag ?? '')
      )
        errors.push('no-native-select: use DropdownSelect instead')
      if (node.tag === 'svg') errors.push('inline-icon')
      if (
        ['alert', 'status', 'log', 'tooltip'].includes(attrs.role) &&
        filename !== 'web/ui/src/ToastViewport.vue'
      )
        errors.push('inline-message')
      if (
        /\b(alert|notice|callout|tooltip|popover|inline-error|toast-container)\b/u.test(
          attrs.class ?? '',
        )
      )
        errors.push('inline-message')
      if (Object.keys(attrs).some((name) => /tooltip|popover|^data-toggle/u.test(name)))
        errors.push('inline-message')
      if (attrs.href?.startsWith('javascript:')) errors.push('unsafe-url')
      if (attrs.target === '_blank' && !attrs.rel?.split(' ').includes('noopener'))
        errors.push('unsafe-link')
      for (const prop of node.props.filter((p: any) => p.type === 7)) {
        if (!['on', 'model', 'for'].includes(prop.name) && feedback(prop.exp?.content ?? '', field))
          errors.push('form-feedback-use-toast')
        if ((prop.exp?.content.length ?? 0) > 50) errors.push('long-expression')
        if (prop.name === 'html') errors.push('unsafe-html')
        if (/document\.|innerHTML|outerHTML|insertAdjacentHTML/u.test(prop.exp?.content ?? ''))
          errors.push('raw-dom')
      }
      if (
        ['button', 'a'].includes(node.tag) &&
        !node.children.length &&
        !attrs['aria-label'] &&
        !node.props.some((p: any) => p.arg?.content === 'aria-label')
      )
        errors.push('unnamed-control')
    }
    for (const child of node.children ?? []) walk(child, dialog, field)
  }
  walk(parse(source))
  return errors
}
