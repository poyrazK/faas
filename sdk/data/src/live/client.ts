import { LIVE_TYPES, LiveProtocolError, LiveResult, parseLiveServerFrame, type LiveColumn, type LiveErrorFrame, type LiveServerFrame } from './protocol.js'

/** The socket a live connection runs over: an app's realtime endpoint. */
export interface LiveSocket {
  readonly readyState: number
  addEventListener(type: 'open' | 'message' | 'close' | 'error', listener: (event: unknown) => void): void
  removeEventListener(type: 'open' | 'message' | 'close' | 'error', listener: (event: unknown) => void): void
  send(data: string): void
  close(): void
}

export type LiveStatus = 'connecting' | 'live' | 'stale' | 'closed'

export type LiveQueryState<Row> = Readonly<{
  status: LiveStatus
  /** Rows in the query's order when it has ORDER BY, otherwise in a stable order. */
  rows: ReadonlyArray<Row>
  /** Stable row identities, aligned with rows (for list keys). */
  keys: ReadonlyArray<string>
  columns: ReadonlyArray<LiveColumn>
  /** The snapshot the rows reflect (pg_snapshot text), once live. */
  snapshot: string | null
  error: LiveErrorFrame | undefined
}>

export type LiveQueryOptions<Row> = {
  /** A sealed query from the app's backend. */
  sealed: string
  /** Fetches a fresh seal for the same query: before expiry, and to resume after a reconnect. */
  refresh?: () => Promise<string>
  /** Rows rendered on the server, shown until the live result arrives. */
  initialRows?: ReadonlyArray<Row>
  /** Turns a row's PostgreSQL text values into a row; defaults to parseLiveRow. */
  parse?: (values: ReadonlyArray<string | null>, columns: ReadonlyArray<LiveColumn>) => Row
}

export type LiveConnectionOptions = {
  /** Opens an authenticated socket to the app's realtime endpoint (called again on every reconnect). */
  connect: () => LiveSocket | Promise<LiveSocket>
  /** Delay before reconnect attempt n (from 1); default capped exponential backoff with jitter. */
  reconnectDelayMs?: (attempt: number) => number
  /** Renew this long before a seal expires; default 15 seconds or a fifth of its lifetime. */
  renewEarlyMs?: number
  /** Clock and timers, for tests. */
  clock?: { now(): number; setTimeout(fn: () => void, ms: number): unknown; clearTimeout(handle: unknown): void }
}

export class LiveQueryClosedError extends Error {
  constructor(readonly frame: LiveErrorFrame | undefined) {
    super(frame ? `live query closed: ${frame.code}: ${frame.message}` : 'live query closed')
    this.name = 'LiveQueryClosedError'
  }
}

const defaultClock = {
  now: () => Date.now(),
  setTimeout: (fn: () => void, ms: number): unknown => globalThis.setTimeout(fn, ms),
  clearTimeout: (h: unknown) => globalThis.clearTimeout(h as ReturnType<typeof globalThis.setTimeout>),
}

/** Parses common PostgreSQL types; everything else stays text. */
export function parseLiveRow(values: ReadonlyArray<string | null>, columns: ReadonlyArray<LiveColumn>): Record<string, unknown> {
  const row: Record<string, unknown> = {}
  columns.forEach((c, i) => { row[c.name] = parseLiveValue(values[i] ?? null, c.type) })
  return row
}

export function parseLiveValue(value: string | null, type: string): unknown {
  if (value === null) return null
  switch (type) {
    case 'bool': return value === 't' || value === 'true'
    case 'int2': case 'int4': case 'oid': return Number(value)
    case 'float4': case 'float8': return value === 'NaN' ? NaN : value === 'Infinity' ? Infinity : value === '-Infinity' ? -Infinity : Number(value)
    case 'json': case 'jsonb': return JSON.parse(value)
    default: return value // int8 and numeric keep their precision as text
  }
}

let nextID = 0

/**
 * One connection to an app's realtime endpoint, carrying any number of live
 * queries. It reconnects with backoff and resumes each query from the
 * version it holds.
 */
export class LiveConnection {
  private readonly options: LiveConnectionOptions
  private readonly clock: NonNullable<LiveConnectionOptions['clock']>
  private readonly queries = new Map<string, LiveQueryHandle<unknown>>()
  private socket: LiveSocket | undefined
  private open = false
  private closed = false
  private attempt = 0
  private reconnectTimer: unknown
  private readonly listeners = {
    open: () => this.onOpen(),
    message: (event: unknown) => this.onMessage(event),
    close: () => this.onClose(),
    error: () => {},
  }

  constructor(options: LiveConnectionOptions) {
    this.options = options
    this.clock = options.clock ?? defaultClock
    void this.connect()
  }

  /** Subscribes to a sealed query. */
  query<Row = Record<string, unknown>>(options: LiveQueryOptions<Row>): LiveQuery<Row> {
    if (this.closed) throw new Error('live connection is closed')
    const handle = new LiveQueryHandle<Row>(this, `q${++nextID}`, options)
    this.queries.set(handle.id, handle as LiveQueryHandle<unknown>)
    if (this.open) handle.attach()
    return handle
  }

  /** Closes every query and the socket. */
  close(): void {
    this.closed = true
    this.clock.clearTimeout(this.reconnectTimer)
    for (const q of this.queries.values()) q.finish(undefined)
    this.queries.clear()
    this.detach()?.close()
  }

  /** @internal */
  send(frame: Record<string, unknown>): void {
    if (this.open && this.socket) this.socket.send(JSON.stringify(frame))
  }

  /** @internal */
  forget(id: string): void {
    this.queries.delete(id)
  }

  /** @internal */
  get timers() { return this.clock }
  /** @internal */
  get renewEarlyMs() { return this.options.renewEarlyMs }
  /** @internal */
  get isOpen() { return this.open }

  private async connect(): Promise<void> {
    if (this.closed) return
    try {
      const socket = await this.options.connect()
      if (this.closed) { socket.close(); return }
      this.socket = socket
      socket.addEventListener('open', this.listeners.open)
      socket.addEventListener('message', this.listeners.message)
      socket.addEventListener('close', this.listeners.close)
      socket.addEventListener('error', this.listeners.error)
      if (socket.readyState === 1) this.onOpen()
    } catch {
      this.scheduleReconnect()
    }
  }

  private detach(): LiveSocket | undefined {
    const socket = this.socket
    if (!socket) return undefined
    socket.removeEventListener('open', this.listeners.open)
    socket.removeEventListener('message', this.listeners.message)
    socket.removeEventListener('close', this.listeners.close)
    socket.removeEventListener('error', this.listeners.error)
    this.socket = undefined
    this.open = false
    return socket
  }

  private onOpen(): void {
    if (this.open) return
    this.open = true
    this.attempt = 0
    for (const q of this.queries.values()) q.attach()
  }

  private onClose(): void {
    this.detach()
    for (const q of this.queries.values()) q.disconnected()
    this.scheduleReconnect()
  }

  private scheduleReconnect(): void {
    if (this.closed) return
    this.attempt++
    const delay = this.options.reconnectDelayMs?.(this.attempt)
      ?? Math.min(30_000, 250 * 2 ** Math.min(this.attempt - 1, 7)) * (0.5 + Math.random() / 2)
    this.reconnectTimer = this.clock.setTimeout(() => { void this.connect() }, delay)
  }

  private onMessage(event: unknown): void {
    const data = (event as { data?: unknown })?.data
    if (typeof data !== 'string') return
    let frame: LiveServerFrame
    try {
      frame = parseLiveServerFrame(data)
    } catch {
      return // not a live frame: other realtime traffic shares the socket
    }
    if (frame.type === LIVE_TYPES.progress) {
      for (const q of this.queries.values()) q.receive(frame)
      return
    }
    const id = frame.subscription
    if (id !== undefined) this.queries.get(id)?.receive(frame)
  }
}

/** A live query's state, for useSyncExternalStore and other stores. */
export interface LiveQuery<Row> {
  readonly id: string
  getSnapshot(): LiveQueryState<Row>
  subscribe(listener: () => void): () => void
  /**
   * Resolves once the result reflects a committed transaction, such as the
   * one that wrote the data (SELECT pg_current_xact_id()), whether or not
   * the write changed these rows.
   */
  awaitTxId(xid: bigint | number | string, options?: { signal?: AbortSignal }): Promise<void>
  close(): void
}

class LiveQueryHandle<Row> implements LiveQuery<Row> {
  readonly id: string
  private readonly conn: LiveConnection
  private readonly options: LiveQueryOptions<Row>
  private result = new LiveResult()
  private sealed: string
  private state: LiveQueryState<Row>
  private readonly listeners = new Set<() => void>()
  private waiters: { xid: bigint; resolve: () => void; reject: (e: unknown) => void }[] = []
  private renewTimer: unknown
  private attached = false
  private finished = false

  constructor(conn: LiveConnection, id: string, options: LiveQueryOptions<Row>) {
    this.conn = conn
    this.id = id
    this.options = options
    this.sealed = options.sealed
    this.state = Object.freeze({
      status: 'connecting', rows: options.initialRows ?? [], keys: [], columns: [], snapshot: null, error: undefined,
    })
  }

  getSnapshot = (): LiveQueryState<Row> => this.state

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  /** @internal Subscribes on the socket, resuming from the version held. */
  attach(): void {
    if (this.finished) return
    const resume = this.result.ready ? this.result.version : 0
    const again = this.attached
    if (again) this.result.resubscribe()
    this.attached = true
    // A held seal may have expired while the socket was down.
    void this.withFreshSeal(again).then((sealed) => {
      if (this.finished || !this.conn.isOpen) return
      this.conn.send({ type: LIVE_TYPES.subscribe, subscription: this.id, sealed, ...(resume > 0 ? { version: resume } : {}) })
    })
  }

  awaitTxId(xid: bigint | number | string, options: { signal?: AbortSignal } = {}): Promise<void> {
    const target = BigInt(xid)
    if (this.result.sees(target)) return Promise.resolve()
    if (this.finished) return Promise.reject(new LiveQueryClosedError(this.result.error))
    return new Promise((resolve, reject) => {
      const waiter = { xid: target, resolve, reject }
      this.waiters.push(waiter)
      options.signal?.addEventListener('abort', () => {
        this.waiters = this.waiters.filter((w) => w !== waiter)
        reject(options.signal?.reason)
      }, { once: true })
    })
  }

  close(): void {
    if (this.finished) return
    this.conn.send({ type: LIVE_TYPES.unsubscribe, subscription: this.id })
    this.finish(undefined)
    this.conn.forget(this.id)
  }

  /** @internal */
  receive(frame: LiveServerFrame): void {
    if (this.finished) return
    try {
      this.result.apply(frame)
    } catch (err) {
      if (!(err instanceof LiveProtocolError)) throw err
      // Start over: the server sends a full result to a fresh subscription.
      // The rows shown stay until it arrives.
      this.conn.send({ type: LIVE_TYPES.unsubscribe, subscription: this.id })
      this.result = new LiveResult()
      this.attach()
      return
    }
    if (frame.type === LIVE_TYPES.error) {
      if (frame.closed) {
        this.finish(frame)
        this.conn.forget(this.id)
        return
      }
      if (frame.code === 'seal_expired') void this.renew()
    }
    if (frame.type === LIVE_TYPES.rows || frame.type === LIVE_TYPES.renewed) this.scheduleRenewal()
    this.publish()
  }

  /** @internal */
  disconnected(): void {
    this.conn.timers.clearTimeout(this.renewTimer)
    if (this.state.status !== 'closed') this.setState({ ...this.state, status: 'connecting' })
  }

  /** @internal */
  finish(frame: LiveErrorFrame | undefined): void {
    if (this.finished) return
    this.finished = true
    this.conn.timers.clearTimeout(this.renewTimer)
    for (const w of this.waiters) w.reject(new LiveQueryClosedError(frame))
    this.waiters = []
    this.setState({ ...this.state, status: 'closed', error: frame })
  }

  private async withFreshSeal(fresh: boolean): Promise<string> {
    if (fresh && this.options.refresh) {
      try { this.sealed = await this.options.refresh() } catch { /* the held seal may still be valid */ }
    }
    return this.sealed
  }

  private scheduleRenewal(): void {
    this.conn.timers.clearTimeout(this.renewTimer)
    if (!this.options.refresh || this.result.expires <= 0) return
    const left = this.result.expires * 1000 - this.conn.timers.now()
    const early = this.conn.renewEarlyMs ?? Math.min(15_000, Math.max(1_000, left / 5))
    this.renewTimer = this.conn.timers.setTimeout(() => { void this.renew() }, Math.max(0, left - early))
  }

  private async renew(): Promise<void> {
    if (!this.options.refresh || this.finished) return
    let sealed: string
    try {
      sealed = await this.options.refresh()
    } catch {
      return // stays live until expiry, then stale; the next renewal retries
    }
    if (this.finished) return
    this.sealed = sealed
    this.conn.send({ type: LIVE_TYPES.renew, subscription: this.id, sealed })
  }

  private publish(): void {
    const r = this.result
    if (!r.ready) {
      if (r.stale && this.state.status !== 'stale') this.setState({ ...this.state, status: 'stale', error: r.error })
      return
    }
    const parse = this.options.parse ?? (parseLiveRow as unknown as NonNullable<LiveQueryOptions<Row>['parse']>)
    const entries = r.entries()
    const prev = this.state
    const sameRows = prev.keys.length === entries.length && prev.snapshot !== null
      && entries.every((e, i) => e.id === prev.keys[i] && this.rowCache.get(e.id)?.values === e.values)
    const rows = sameRows ? prev.rows : entries.map((e) => {
      const cached = this.rowCache.get(e.id)
      if (cached?.values === e.values) return cached.row
      const row = parse(e.values, r.columns)
      this.rowCache.set(e.id, { values: e.values, row })
      return row
    })
    if (!sameRows) for (const id of this.rowCache.keys()) if (!entries.some((e) => e.id === id)) this.rowCache.delete(id)
    this.setState({
      status: r.stale ? 'stale' : 'live',
      rows,
      keys: sameRows ? prev.keys : entries.map((e) => e.id),
      columns: r.columns,
      snapshot: r.snapshot.text,
      error: r.stale ? r.error : undefined,
    })
    this.waiters = this.waiters.filter((w) => {
      if (!r.sees(w.xid)) return true
      w.resolve()
      return false
    })
  }

  private readonly rowCache = new Map<string, { values: ReadonlyArray<string | null>; row: Row }>()

  private setState(next: Omit<LiveQueryState<Row>, never>): void {
    const s = this.state
    if (s.status === next.status && s.rows === next.rows && s.snapshot === next.snapshot && s.error === next.error && s.columns === next.columns) return
    this.state = Object.freeze(next)
    for (const l of [...this.listeners]) l()
  }
}
