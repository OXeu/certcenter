export type ApiInit = { method?: string; body?: unknown; csrf?: string }

/**
 * Calls the JSON API and turns `{ error }` failures into an Error whose cause
 * is the HTTP status (0 for a network failure).
 * @param {string} path
 * @param {ApiInit} [init]
 * @returns {Promise<any>}
 */
export async function api(path: string, init: ApiInit = {}): Promise<any> {
  const method = init.method ?? 'GET'
  /** @type {Record<string, string>} */
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'X-CSRF-Token': init.csrf ?? '',
  }
  if (init.body !== undefined) headers['Content-Type'] = 'application/json'
  const resp = await fetch(path, {
    method,
    headers,
    credentials: 'same-origin',
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
  })
  if (resp.status === 204) return null
  /** @type {any} */
  const data: any = await resp.json().catch(() => ({}))
  if (!resp.ok) {
    const detail = typeof data?.error === 'string' ? data.error.trim() : ''
    throw new ApiError(resp.status, detail)
  }
  return data
}

/**
 * @param {string | null | undefined} iso
 * @returns {string}
 */
export function timeOnly(iso: string | null | undefined): string {
  if (!iso || iso.startsWith('0001-')) return ''
  return new Date(iso).toLocaleString()
}
import { ApiError } from './errors.ts'
