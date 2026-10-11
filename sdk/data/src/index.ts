import { PostgrestClient } from '@supabase/postgrest-js'

export type DataClientOptions = {
  url: string
  accessToken: string | (() => string | Promise<string>)
  fetch?: typeof globalThis.fetch
}

/** Application tokens authenticate data access; account-management keys do not. */
export function createDataClient<Database>(options: DataClientOptions) {
  const url = new URL(options.url)
  if (url.username || url.password || url.search || url.hash) throw new Error('Data API URL must not contain credentials, a query or a fragment')
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))) throw new Error('Data API requires HTTPS')
  const base = url.toString().replace(/\/$/, '')
  const endpoint = base.endsWith('/rest/v1') ? base : `${base}/rest/v1`
  const fetcher = options.fetch ?? globalThis.fetch
  const authenticatedFetch: typeof globalThis.fetch = async (input, init) => {
    const token = typeof options.accessToken === 'function' ? await options.accessToken() : options.accessToken
    if (!token || /\s/.test(token)) throw new Error('A valid application access token is required')
    const headers = new Headers(init?.headers)
    headers.set('Authorization', `Bearer ${token}`)
    return fetcher(input, { ...init, headers, credentials: 'omit', redirect: 'error' })
  }
  // The generated contract retains the PostgREST schema shape and supports
  // .schema('api'), typed inserts, filters, relationships and projections.
  return new PostgrestClient<Database>(endpoint, { fetch: authenticatedFetch })
}

export { PostgrestError } from '@supabase/postgrest-js'
