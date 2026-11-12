/** Lossless mode represents every JSON number as its original decimal text. */
export type NumericMode = 'number' | 'lossless'

/** Decode numbers without first passing them through IEEE 754. Includes JSON columns. */
export function parseLosslessJSON(source: string): unknown {
  // Tokenize strings first so digits in identifiers, escapes and string values
  // cannot be mistaken for numbers. JSON.parse still validates the grammar.
  return JSON.parse(source.replace(/"(?:[^"\\]|\\[\s\S])*"|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/g,
    token => token[0] === '"' ? token : JSON.stringify(token)))
}

const losslessFetchers = new WeakSet<Function>()
export function usesLosslessNumbers(query: { fetch?: unknown }): boolean {
  return typeof query.fetch === 'function' && losslessFetchers.has(query.fetch)
}

export function numericFetch(send: typeof globalThis.fetch, mode: NumericMode = 'number'): typeof globalThis.fetch {
  if (mode !== 'number' && mode !== 'lossless') throw new TypeError('numericMode must be number or lossless')
  if (mode === 'number') return send
  const fetcher: typeof globalThis.fetch = async (input, init) => {
    const response = await send(input, init)
    // PostgREST reads response.text() and parses it internally. Convert only
    // successful JSON data, keeping error codes, CSV and empty bodies intact.
    if (!response.ok || response.status === 204 || init?.method === 'HEAD' || !/\bjson\b/i.test(response.headers.get('content-type') ?? '')) return response
    const source = await response.text()
    if (!source) return new Response(null, response)
    const body = JSON.stringify(parseLosslessJSON(source))
    const headers = new Headers(response.headers)
    for (const name of ['content-length', 'content-encoding', 'etag']) headers.delete(name)
    return new Response(body, { status: response.status, statusText: response.statusText, headers })
  }
  losslessFetchers.add(fetcher)
  return fetcher
}
