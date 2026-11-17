import { checkTemplate as commonTemplate } from '@oxeu/center-common/tools/oxlint/templates.ts'
import { formFeedback } from './form-feedback.ts'

export function checkTemplate(source: string, filename = '', script = ''): string[] {
  return commonTemplate(source, filename, formFeedback(script))
}
