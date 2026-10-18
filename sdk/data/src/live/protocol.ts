/**
 * The live-query frame contract (ADR-20261016151947), mirroring the Go reference client
 * in pkg/livequery/liveproto. Both run the shared session fixtures.
 */

export const LIVE_TYPES = {
  subscribe: 'live.subscribe',
  renew: 'live.renew',
  unsubscribe: 'live.unsubscribe',
  rows: 'live.rows',
  changes: 'live.changes',
  progress: 'live.progress',
  renewed: 'live.renewed',
  error: 'live.error',
} as const

export type LiveColumn = Readonly<{ name: string; type: string }>
/** PostgreSQL text values; null is SQL NULL. */
export type LiveValues = ReadonlyArray<string | null>
export type LiveRow = Readonly<{ id: string; values: LiveValues }>

export type LiveRowsFrame = {
  type: 'live.rows'; subscription: string; database: string; version: number; snapshot: string
  expires: number; columns: LiveColumn[]; ordered?: boolean; rows: LiveRow[]; complete: boolean
}
export type LiveChange = { op: 'upsert'; id: string; values: LiveValues } | { op: 'remove'; id: string }
export type LiveChangesFrame = {
  type: 'live.changes'; subscription: string; base: number; version: number; snapshot: string
  xids: string[]; changes: LiveChange[]; order?: string[]
}
export type LiveProgressFrame = { type: 'live.progress'; database: string; snapshot: string }
export type LiveRenewedFrame = { type: 'live.renewed'; subscription: string; expires: number }
export type LiveErrorFrame = {
  type: 'live.error'; subscription?: string; code: string; message: string; closed: boolean
  limit?: number; observed?: number; docs?: string
}
export type LiveServerFrame = LiveRowsFrame | LiveChangesFrame | LiveProgressFrame | LiveRenewedFrame | LiveErrorFrame

export class LiveProtocolError extends Error {
  constructor(message: string) { super(`live protocol violation: ${message}`); this.name = 'LiveProtocolError' }
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const isVersion = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0
const isValues = (v: unknown): v is LiveValues => Array.isArray(v) && v.every((x) => x === null || typeof x === 'string')

/** Decodes and shape-checks a server frame. */
export function parseLiveServerFrame(data: string): LiveServerFrame {
  let f: unknown
  try { f = JSON.parse(data) } catch { throw new LiveProtocolError('frame is not JSON') }
  if (!isObject(f) || typeof f.type !== 'string') throw new LiveProtocolError('frame has no type')
  const str = (k: string) => typeof f[k] === 'string'
  const ok = (() => {
    switch (f.type) {
      case LIVE_TYPES.rows:
        return str('subscription') && str('database') && isVersion(f.version) && str('snapshot') && typeof f.expires === 'number'
          && Array.isArray(f.columns) && f.columns.every((c) => isObject(c) && typeof c.name === 'string' && typeof c.type === 'string')
          && Array.isArray(f.rows) && f.rows.every((r) => isObject(r) && typeof r.id === 'string' && isValues(r.values))
          && typeof f.complete === 'boolean' && (f.ordered === undefined || typeof f.ordered === 'boolean')
      case LIVE_TYPES.changes:
        return str('subscription') && isVersion(f.base) && isVersion(f.version) && str('snapshot')
          && Array.isArray(f.xids) && f.xids.every((x) => typeof x === 'string')
          && Array.isArray(f.changes) && f.changes.every((c) => isObject(c) && typeof c.id === 'string'
            && (c.op === 'remove' || (c.op === 'upsert' && isValues(c.values))))
          && (f.order === undefined || (Array.isArray(f.order) && f.order.every((x) => typeof x === 'string')))
      case LIVE_TYPES.progress:
        return str('database') && str('snapshot')
      case LIVE_TYPES.renewed:
        return str('subscription') && typeof f.expires === 'number'
      case LIVE_TYPES.error:
        return str('code') && str('message') && typeof f.closed === 'boolean'
      default:
        return false
    }
  })()
  if (!ok) throw new LiveProtocolError(`malformed ${String(f.type)} frame`)
  return f as unknown as LiveServerFrame
}

/** A parsed pg_snapshot ("xmin:xmax:xip,..."): the transactions a result reflects. */
export class LiveSnapshot {
  readonly xmin: bigint
  readonly xmax: bigint
  readonly xip: ReadonlySet<bigint>
  readonly text: string

  private constructor(xmin: bigint, xmax: bigint, xip: Set<bigint>, text: string) {
    this.xmin = xmin; this.xmax = xmax; this.xip = xip; this.text = text
  }

  static parse(text: string): LiveSnapshot {
    const parts = text.split(':')
    const digits = /^(0|[1-9][0-9]*)$/
    if (parts.length !== 3 || !digits.test(parts[0]) || !digits.test(parts[1])) throw new LiveProtocolError(`bad snapshot ${text}`)
    const xmin = BigInt(parts[0]); const xmax = BigInt(parts[1])
    if (xmin > xmax) throw new LiveProtocolError(`bad snapshot ${text}`)
    const xip = new Set<bigint>()
    if (parts[2] !== '') {
      for (const raw of parts[2].split(',')) {
        if (!digits.test(raw)) throw new LiveProtocolError(`bad snapshot ${text}`)
        const x = BigInt(raw)
        if (x < xmin || x >= xmax) throw new LiveProtocolError(`bad snapshot ${text}`)
        xip.add(x)
      }
    }
    return new LiveSnapshot(xmin, xmax, xip, text)
  }

  static readonly zero = LiveSnapshot.parse('0:0:')

  /** Whether a committed transaction is visible, as pg_visible_in_snapshot decides. */
  visible(xid: bigint): boolean {
    if (xid < this.xmin) return true
    if (xid >= this.xmax) return false
    return !this.xip.has(xid)
  }

  /** Whether every transaction visible in o is visible here: this is not older than o. */
  covers(o: LiveSnapshot): boolean {
    for (const x of this.xip) if (o.visible(x)) return false
    if (this.xmax >= o.xmax) return true
    if (o.xmax - this.xmax > BigInt(o.xip.size)) return false
    for (let x = this.xmax; x < o.xmax; x++) if (o.visible(x)) return false
    return true
  }
}

/**
 * One subscription's result as the client holds it. apply() refuses frames
 * that break the contract; the caller then resubscribes.
 */
export class LiveResult {
  database = ''
  columns: LiveColumn[] = []
  ordered = false
  version = 0
  snapshot: LiveSnapshot = LiveSnapshot.zero
  expires = 0
  stale = false
  closed = false
  error: LiveErrorFrame | undefined
  private isReady = false
  private rows = new Map<string, LiveValues>()
  private order: string[] = []
  private pending: LiveRowsFrame | undefined
  private resyncing = false

  get ready(): boolean { return this.isReady }

  /** After a (re)subscribe, the next live.rows may start a new version sequence. */
  resubscribe(): void { this.resyncing = true; this.pending = undefined }

  apply(frame: LiveServerFrame): void {
    if (this.closed) throw new LiveProtocolError('frame after the subscription closed')
    switch (frame.type) {
      case 'live.rows': return this.applyRows(frame)
      case 'live.changes': return this.applyChanges(frame)
      case 'live.progress':
        if (frame.database !== this.database || !this.isReady || this.stale) return
        this.snapshot = this.advance(frame.snapshot)
        return
      case 'live.renewed':
        this.expires = frame.expires; this.stale = false
        return
      case 'live.error':
        this.error = frame
        if (frame.closed) this.closed = true
        else this.stale = true
    }
  }

  private applyRows(f: LiveRowsFrame): void {
    const p = this.pending
    if (p && (p.version !== f.version || p.snapshot !== f.snapshot)) {
      throw new LiveProtocolError(`live.rows for version ${f.version} interleaved with version ${p.version}`)
    }
    const acc = p ?? { ...f, rows: [] }
    acc.rows = [...acc.rows, ...f.rows]
    if (!f.complete) { this.pending = acc; return }
    this.pending = undefined
    const snap = LiveSnapshot.parse(acc.snapshot)
    if (this.isReady && !this.resyncing && acc.version <= this.version) {
      throw new LiveProtocolError(`live.rows version ${acc.version} does not advance ${this.version}`)
    }
    const rows = new Map<string, LiveValues>()
    const order: string[] = []
    for (const r of acc.rows) {
      if (rows.has(r.id)) throw new LiveProtocolError(`row ${r.id} twice`)
      if (r.values.length !== acc.columns.length) throw new LiveProtocolError(`row ${r.id} has ${r.values.length} values for ${acc.columns.length} columns`)
      rows.set(r.id, r.values)
      if (acc.ordered) order.push(r.id)
    }
    this.database = acc.database; this.columns = acc.columns; this.ordered = acc.ordered === true
    this.version = acc.version; this.snapshot = snap; this.expires = acc.expires
    this.rows = rows; this.order = order; this.isReady = true; this.stale = false; this.resyncing = false
  }

  private applyChanges(f: LiveChangesFrame): void {
    if (!this.isReady || this.pending) throw new LiveProtocolError('live.changes before a complete live.rows')
    if (f.base !== this.version) throw new LiveProtocolError(`live.changes from version ${f.base}, client holds ${this.version}`)
    if (f.version <= f.base) throw new LiveProtocolError(`live.changes version ${f.version} does not advance ${f.base}`)
    if (this.stale) throw new LiveProtocolError('live.changes for a stale subscription')
    const snap = this.advance(f.snapshot)
    for (const x of f.xids) {
      if (!/^(0|[1-9][0-9]*)$/.test(x) || !snap.visible(BigInt(x))) throw new LiveProtocolError(`transaction ${x} is not visible in ${f.snapshot}`)
    }
    const rows = new Map(this.rows)
    let added = false
    for (const c of f.changes) {
      if (c.op === 'upsert') {
        if (c.values.length !== this.columns.length) throw new LiveProtocolError(`row ${c.id} has ${c.values.length} values for ${this.columns.length} columns`)
        if (!rows.has(c.id)) added = true
        rows.set(c.id, c.values)
      } else {
        if (!rows.delete(c.id)) throw new LiveProtocolError(`remove of unknown row ${c.id}`)
      }
    }
    let order = this.order
    if (f.order !== undefined) {
      if (!this.ordered) throw new LiveProtocolError('order for an unordered result')
      if (f.order.length !== rows.size) throw new LiveProtocolError(`order lists ${f.order.length} rows, result has ${rows.size}`)
      const seen = new Set<string>()
      for (const id of f.order) {
        if (!rows.has(id) || seen.has(id)) throw new LiveProtocolError('order is not a permutation of the result')
        seen.add(id)
      }
      order = [...f.order]
    } else if (this.ordered && added) {
      throw new LiveProtocolError('rows added to an ordered result without an order')
    } else if (this.ordered) {
      order = order.filter((id) => rows.has(id))
    }
    this.rows = rows; this.order = order; this.version = f.version; this.snapshot = snap
    this.resyncing = false // the server resumed the sequence the client held
  }

  private advance(text: string): LiveSnapshot {
    const snap = LiveSnapshot.parse(text)
    if (!snap.covers(this.snapshot)) throw new LiveProtocolError(`snapshot ${text} is older than ${this.snapshot.text}`)
    return snap
  }

  /** The rows: in the query's order when it has one, by row ID otherwise. */
  entries(): LiveRow[] {
    if (this.ordered) return this.order.map((id) => ({ id, values: this.rows.get(id)! }))
    return [...this.rows.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([id, values]) => ({ id, values }))
  }

  /** Whether the result reflects a committed transaction: the check behind awaitTxId. */
  sees(xid: bigint): boolean { return this.isReady && this.snapshot.visible(xid) }
}
