import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import type { LiveConnection, LiveQuery, LiveQueryState, LiveReadOptions, LiveSealedOptions } from './client.js'
import { LiveQueryClosedError } from './client.js'

export type UseLiveQueryOptions<Row> = Omit<LiveSealedOptions<Row>, 'sealed'>

export type UseLiveQueryResult<Row> = LiveQueryState<Row> & {
  /** Resolves once the rows reflect a committed transaction (pg_current_xact_id() of a write). */
  awaitTxId(xid: bigint | number | string, options?: { signal?: AbortSignal }): Promise<void>
}

const noSubscription = () => () => {}

/**
 * Subscribes a component to a sealed live query. Server rendering and the
 * first client render show initialRows; the live result replaces them.
 * A new seal string subscribes again; renewals go through refresh.
 */
export function useLiveQuery<Row = Record<string, unknown>>(
  connection: LiveConnection | null | undefined,
  sealed: string | null | undefined,
  options: UseLiveQueryOptions<Row> = {},
): UseLiveQueryResult<Row> {
  const latest = useRef(options)
  latest.current = options
  return useLive<Row>(connection, sealed ?? null, options.initialRows, (conn) => {
    const { refresh, parse, initialRows } = latest.current
    return conn.query<Row>({ sealed: sealed!, initialRows, parse, refresh: refresh && (() => latest.current.refresh!()) })
  })
}

/**
 * Subscribes a component to a Data API read as the signed-in user (RLS
 * mode). A different database, read or schema subscribes again; token is
 * called whenever a fresh one is needed.
 */
export function useLiveRead<Row = Record<string, unknown>>(
  connection: LiveConnection | null | undefined,
  options: LiveReadOptions<Row> | null | undefined,
): UseLiveQueryResult<Row> {
  const latest = useRef(options)
  latest.current = options
  const key = options ? JSON.stringify([options.database, options.schema ?? '', options.read]) : null
  return useLive<Row>(connection, key, options?.initialRows, (conn) => {
    const o = latest.current!
    const read = { database: o.database, read: o.read, schema: o.schema, initialRows: o.initialRows, parse: o.parse }
    if (o.anonymous === true) return conn.query<Row>({ ...read, anonymous: true })
    if (typeof o.serverKey === 'string') return conn.query<Row>({ ...read, serverKey: o.serverKey, subject: o.subject })
    return conn.query<Row>({ ...read, token: () => latest.current!.token!() })
  })
}

function useLive<Row>(
  connection: LiveConnection | null | undefined,
  key: string | null,
  initialRows: ReadonlyArray<Row> | undefined,
  open: (connection: LiveConnection) => LiveQuery<Row>,
): UseLiveQueryResult<Row> {
  const opener = useRef(open)
  opener.current = open
  const [query, setQuery] = useState<LiveQuery<Row> | null>(null)
  useEffect(() => {
    if (!connection || key === null) return
    const q = opener.current(connection)
    setQuery(q)
    return () => {
      q.close()
      setQuery(null)
    }
  }, [connection, key])
  const initial = useMemo<LiveQueryState<Row>>(() => Object.freeze({
    status: 'connecting', rows: initialRows ?? [], keys: [], columns: [], snapshot: null, error: undefined,
  }), []) // the server-rendered rows are fixed for the component's life
  const getInitial = useCallback(() => initial, [initial])
  const state = useSyncExternalStore(query ? query.subscribe : noSubscription, query ? query.getSnapshot : getInitial, getInitial)
  const awaitTxId = useCallback<UseLiveQueryResult<Row>['awaitTxId']>(
    (xid, o) => (query ? query.awaitTxId(xid, o) : Promise.reject(new LiveQueryClosedError(undefined))),
    [query],
  )
  return useMemo(() => ({ ...state, awaitTxId }), [state, awaitTxId])
}
