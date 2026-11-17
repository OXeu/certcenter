import { it, expect, vi } from 'vite-plus/test'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { toast } from '@yusufceng1z/ding'
import ToastViewport from '../src/ToastViewport.vue'
import DialogPanel from '../src/DialogPanel.vue'
import { createForm } from '../src/forms.ts'
import { createCopy } from '../src/i18n.ts'
import { describeError } from '../src/errors.ts'
import { api } from '../src/core.ts'
import { mountToasts, notify } from '../src/toast.ts'

vi.mock('@yusufceng1z/ding', async (original) => ({
  ...(await original<typeof import('@yusufceng1z/ding')>()),
  bindToast: vi.fn(() => vi.fn()),
}))

it('shows API failure toasts above the open modal and keeps values available for retry', async () => {
  const { msg } = createCopy('zh-CN')
  const form = createForm({
    msg,
    csrf: 'csrf',
    describe: (err) => describeError(msg, err),
    report: (err) => {
      notify('error', msg('error.title'), describeError(msg, err))
    },
  })
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
  }
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce({
      ok: false,
      status: 400,
      json: async () => ({ error: 'certificate policy is disabled' }),
    })
    .mockResolvedValueOnce({ ok: true, status: 204 })
  vi.stubGlobal('fetch', fetchMock)
  mountToasts('zh-CN', 'test-nonce')
  const wrapper = mount(
    defineComponent({
      components: { DialogPanel, ToastViewport },
      setup: () => ({ form, msg }),
      template:
        '<div><ToastViewport /><DialogPanel v-if="form.open" :form="form" :msg="msg" /></div>',
    }),
    { attachTo: document.body },
  )
  try {
    form.show('Issue', [{ key: 'name', label: 'common.name' }], { name: 'keep' }, async () => {
      await api('/issue', { method: 'POST' })
    })
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(form.open).toBe(true)
    expect(form.busy).toBe(false)
    expect(form.values.name).toBe('keep')
    expect(wrapper.get('dialog .tst-viewport').text()).toContain('certificate policy is disabled')
    expect(wrapper.get('form').text()).not.toContain('certificate policy is disabled')
    expect(document.body.querySelectorAll('.tst-viewport')).toHaveLength(1)
    expect(document.head.querySelector('style[data-toast]')?.getAttribute('nonce')).toBe('test-nonce')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(form.open).toBe(false)
    expect(wrapper.find('dialog').exists()).toBe(false)
    expect(document.body.querySelector(':scope > .tst-viewport')).not.toBeNull()
  } finally {
    toast.dismiss(undefined, { immediate: true })
    await nextTick()
    wrapper.unmount()
    vi.unstubAllGlobals()
  }
})
