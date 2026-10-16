import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import type { LiveConnection, LiveQuery, LiveQueryOptions, LiveQueryState } from './client.js'
import { LiveQueryClosedError } from './client.js'

export type UseLiveQueryOptions<Row> = Omit<LiveQueryOptions<Row>, 'sealed'>

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
  const [query, setQuery] = useState<LiveQuery<Row> | null>(null)
  useEffect(() => {
    if (!connection || !sealed) return
    const { refresh, parse, initialRows } = latest.current
    const q = connection.query<Row>({
      sealed,
      initialRows,
      parse,
      refresh: refresh && (() => latest.current.refresh!()),
    })
    setQuery(q)
    return () => {
      q.close()
      setQuery(null)
    }
  }, [connection, sealed])
  const initial = useMemo<LiveQueryState<Row>>(() => Object.freeze({
    status: 'connecting', rows: options.initialRows ?? [], keys: [], columns: [], snapshot: null, error: undefined,
  }), []) // the server-rendered rows are fixed for the component's life
  const getInitial = useCallback(() => initial, [initial])
  const state = useSyncExternalStore(query ? query.subscribe : noSubscription, query ? query.getSnapshot : getInitial, getInitial)
  const awaitTxId = useCallback<UseLiveQueryResult<Row>['awaitTxId']>(
    (xid, o) => (query ? query.awaitTxId(xid, o) : Promise.reject(new LiveQueryClosedError(undefined))),
    [query],
  )
  return useMemo(() => ({ ...state, awaitTxId }), [state, awaitTxId])
}
