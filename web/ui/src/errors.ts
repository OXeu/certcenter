/** @type {Record<string, string>} */
const statusMessages: Record<string, string> = {
  400: 'error.badRequest',
  401: 'error.unauthorized',
  403: 'error.forbidden',
  404: 'error.notFound',
  409: 'error.conflict',
  500: 'error.500',
  502: 'error.502',
}

export class FormValidationError extends Error {}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly detail: string = '',
  ) {
    super(detail || `HTTP ${status}`, { cause: status })
  }
}

/**
 * @param {unknown} err
 * @returns {string}
 */
export function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/**
 * @param {unknown} err
 * @returns {number}
 */
function codeOf(err: unknown): number {
  const code = err instanceof Error ? err.cause : undefined
  return typeof code === 'number' ? code : 0
}

/**
 * Preserves API error details and translates failures without a server message.
 * @param {(id: string) => string} msg
 * @param {unknown} err
 * @returns {string}
 */
export function describeError(msg: (id: string) => string, err: unknown): string {
  if (err instanceof FormValidationError) return err.message
  if (err instanceof ApiError && err.detail) return err.detail
  const code = codeOf(err)
  const key = code === 0 ? 'error.network' : statusMessages[code]
  if (key === undefined) return messageOf(err)
  const known = msg(key)
  return known === key ? messageOf(err) : known
}
