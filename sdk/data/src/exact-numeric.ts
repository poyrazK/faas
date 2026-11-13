/** An exact JSON number, distinct from a string containing decimal text. */
export class ExactNumber {
  readonly source: string
  constructor(source: string) {
    if (typeof source !== 'string' || !/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/.test(source)) throw new TypeError('Use a JSON numeric literal')
    this.source = source
    Object.freeze(this)
  }
  toString(): string { return this.source }
  valueOf(): never { throw new TypeError('ExactNumber requires an explicit conversion') }
  toJSON(): unknown {
    const raw = (JSON as typeof JSON & { rawJSON?: (source: string) => unknown }).rawJSON
    if (!raw) throw new TypeError('Use stringifyExactJSON to serialize exact numbers')
    return raw(this.source)
  }
}

const jsonTokens = /"(?:[^"\\]|\\[\s\S])*"|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/g

/** Decode numeric literals, including JSON columns, without rounding. */
export function parseExactJSON(source: string): unknown {
  // Temporary tags use a key absent from every decoded source string, so an
  // application's object or string can never be mistaken for a number.
  const strings = new Set<string>()
  source.replace(jsonTokens, token => { if (token[0] === '"') strings.add(JSON.parse(token) as string); return token })
  let key = '$gregale.exact.number'
  while (strings.has(key)) key += '_'
  const transformed = source.replace(jsonTokens, token => token[0] === '"' ? token : `{${JSON.stringify(key)}:${JSON.stringify(token)}}`)
  return JSON.parse(transformed, (_name, value: unknown) => value && typeof value === 'object' && Object.hasOwn(value, key)
    ? new ExactNumber((value as Record<string, string>)[key]!) : value) as unknown
}

/** Serialize exact values as numbers, including on runtimes without rawJSON. */
export function stringifyExactJSON(value: unknown): string | undefined {
  const ancestors = new Set<object>()
  const encode = (item: unknown, key: string): string | undefined => {
    if (item instanceof ExactNumber) return item.source
    if (item && typeof item === 'object' && 'toJSON' in item && typeof item.toJSON === 'function') item = item.toJSON(key) as unknown
    if (item instanceof ExactNumber) return item.source
    if (item instanceof Number || item instanceof String || item instanceof Boolean) item = item.valueOf()
    if (!item || typeof item !== 'object') return JSON.stringify(item)
    if (ancestors.has(item)) throw new TypeError('Cannot serialize cyclic JSON')
    ancestors.add(item)
    try {
      if (Array.isArray(item)) return '[' + Array.from(item, (v, i) => encode(v, String(i)) ?? 'null').join(',') + ']'
      const fields: string[] = []
      for (const name of Object.keys(item)) {
        const encoded = encode((item as Record<string, unknown>)[name], name)
        if (encoded !== undefined) fields.push(JSON.stringify(name) + ':' + encoded)
      }
      return '{' + fields.join(',') + '}'
    } finally { ancestors.delete(item) }
  }
  return encode(value, '')
}

interface ExactBuilder {
  fetch: typeof globalThis.fetch
  body?: unknown
  headers?: Headers
  isMaybeSingle?: boolean
  then: PromiseLike<{ data: unknown; error: unknown }>['then']
}

/** Keep postgrest-js's builders and inference; decode from original response
 * bytes and serialize bodies before its ordinary JSON encoder sees them. */
export function exactClient<T extends object>(client: T): T {
  const proxies = new WeakMap<object, object>()
  const wrap = <V extends object>(target: V): V => {
    const existing = proxies.get(target)
    if (existing) return existing as V
    const proxy = new Proxy(target, { get(object, property, receiver) {
      const value: unknown = Reflect.get(object, property, receiver)
      if (property === 'fetch') return value
      if (property === 'then' && typeof value === 'function') {
        return (fulfilled?: (value: unknown) => unknown, rejected?: (reason: unknown) => unknown) => {
          const original = object as unknown as ExactBuilder
          const builder = Object.assign(Object.create(Object.getPrototypeOf(object)) as ExactBuilder, original)
          if (original.headers) builder.headers = new Headers(original.headers)
          let data: unknown, captured = false
          const body = original.body === undefined ? undefined : stringifyExactJSON(original.body)
          if (original.body !== undefined) builder.body = {}
          builder.fetch = async (input, init) => {
            const response = await original.fetch(input, { ...init, ...(body !== undefined ? { body } : {}) })
            captured = false
            if (!response.ok || response.status === 204 || init?.method === 'HEAD' || !/\bjson\b/i.test(response.headers.get('content-type') ?? '')) return response
            const source = await response.text()
            if (source) { data = parseExactJSON(source); captured = true }
            return new Response(source || null, { status: response.status, statusText: response.statusText, headers: response.headers })
          }
          return Promise.resolve(builder).then(response => {
            if (response.error || !captured || response.data === null) return response
            const exact = original.isMaybeSingle && Array.isArray(data) ? data[0] ?? null : data
            return { ...response, data: exact }
          }).then(fulfilled, rejected)
        }
      }
      if (typeof value !== 'function') return value
      return (...args: unknown[]) => {
        const rpcOptions = args[2] as { get?: boolean; head?: boolean } | undefined
        if (property === 'rpc' && args[1] && typeof args[1] === 'object' && (rpcOptions?.get || rpcOptions?.head)) {
          args[1] = Object.fromEntries(Object.entries(args[1] as Record<string, unknown>).map(([name, arg]) => [name, arg instanceof ExactNumber ? arg.source : arg && typeof arg === 'object' && !Array.isArray(arg) ? stringifyExactJSON(arg) : arg]))
        }
        const result: unknown = Reflect.apply(value, object, args)
        return result && typeof result === 'object' ? wrap(result) : result
      }
    } })
    proxies.set(target, proxy)
    return proxy
  }
  return wrap(client)
}
