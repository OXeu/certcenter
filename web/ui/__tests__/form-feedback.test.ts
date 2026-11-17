import { describe, it, expect } from 'vite-plus/test'
import { checkTemplate } from '../../../tools/oxlint/templates.ts'

describe('submission feedback uses toasts', () => {
  it.each([
    '<dialog><p v-if="form.error" class="field">{{ form.error }}</p></dialog>',
    '<p>{{ error }}</p>',
    '<p>{{ form["error"] }}</p>',
    '<p v-show="submitSuccess">Saved</p>',
    '<p v-text="response.message" />',
    '<div>{{ submitResult }}</div>',
    '<span>{{ msg("toast.saved") }}</span>',
    '<Feedback :message="form.error" />',
    '<FormFeedback />',
    '<p class="form-success">Saved</p>',
    '<dialog><p class="field">{{ field.error }}</p><p>{{ error }}</p></dialog>',
  ])('rejects %s', (template) => {
    expect(checkTemplate(template)).toContain('form-feedback-use-toast')
  })
  it('follows computed aliases', () => {
    expect(
      checkTemplate('<p>{{ caption }}</p>', '', 'const caption = computed(() => form.error)'),
    ).toContain('form-feedback-use-toast')
  })
  it.each([
    '<dialog><p class="field">{{ field.help }}</p></dialog>',
    '<dialog><p v-if="field.error" class="field">{{ field.error }}</p></dialog>',
    '<td>{{ event.message }}</td>',
    '<span class="badge">{{ job.status }}</span>',
    '<dialog><code>{{ form.token }}</code></dialog>',
    '<button @click="page.report(error)">Report</button>',
  ])('allows field guidance and stored records: %s', (template) => {
    expect(checkTemplate(template)).toEqual([])
  })
})
