import { beforeEach, afterEach, it, expect, vi } from 'vite-plus/test'
import { api } from '../src/core.ts'
import { describeError, FormValidationError } from '../src/errors.ts'
import { createCopy } from '../src/i18n.ts'

const fetchMock = vi.fn()
const { msg } = createCopy('zh-CN')
beforeEach(() => vi.stubGlobal('fetch', fetchMock))
afterEach(() => vi.unstubAllGlobals())

it.each([400, 401, 403, 404, 409, 422, 500, 502, 503])(
  'preserves a JSON error for HTTP %i',
  async (status) => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status,
      json: async () => ({ error: 'certificate policy is disabled' }),
    })
    const error = await api('/test').catch((err: unknown) => err)
    expect(describeError(msg, error)).toBe('certificate policy is disabled')
  },
)

it.each([{}, null, { error: '' }, { error: '  ' }, { error: { message: 'invalid' } }])(
  'falls back to a translated status for %j',
  async (payload) => {
    fetchMock.mockResolvedValueOnce({ ok: false, status: 400, json: async () => payload })
    const error = await api('/test').catch((err: unknown) => err)
    expect(describeError(msg, error)).toBe(msg('error.badRequest'))
  },
)

it('handles non-JSON responses and network failures', async () => {
  fetchMock.mockResolvedValueOnce({
    ok: false,
    status: 502,
    json: async () => {
      throw new SyntaxError('HTML response')
    },
  })
  expect(describeError(msg, await api('/test').catch((err: unknown) => err))).toBe(msg('error.502'))
  fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'))
  expect(describeError(msg, await api('/test').catch((err: unknown) => err))).toBe(
    msg('error.network'),
  )
  expect(describeError(msg, new FormValidationError('请选择 DNS 提供商'))).toBe('请选择 DNS 提供商')
})
