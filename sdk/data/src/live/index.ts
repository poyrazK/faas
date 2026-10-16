/**
 * Live queries (ADR-1384): SQL results that stay current. A backend seals a
 * query; the browser subscribes over the app's realtime endpoint and holds
 * the result as of a committed snapshot, updated by changes.
 *
 * Backend:
 *   const live = await createLiveSealer({ secret: process.env.GREGALE_LIVE_SECRET!, database: process.env.GREGALE_LIVE_DATABASE! })
 *   const sealed = await live.seal(sql`SELECT id, body FROM messages WHERE channel_id = ${id} ORDER BY id DESC LIMIT 50`)
 *
 * Browser:
 *   const connection = new LiveConnection({ connect: () => openRealtimeSocket() })
 *   const feed = connection.query({ sealed, refresh: () => fetchSeal(id) })
 *   feed.subscribe(() => render(feed.getSnapshot().rows))
 *   await feed.awaitTxId(xidFromWrite)
 */

export { sql, toPostgresText } from './sql.js'
export { createLiveSealer, LiveSealError, LIVE_SEAL_DEFAULT_TTL_SECONDS, LIVE_SEAL_MAX_TTL_SECONDS } from './seal.js'
export type { LiveQueryText, LiveSealer, LiveSealOptions } from './seal.js'
export { LiveConnection, LiveQueryClosedError, parseLiveRow, parseLiveValue } from './client.js'
export type { LiveConnectionOptions, LiveQuery, LiveQueryOptions, LiveQueryState, LiveSocket, LiveStatus } from './client.js'
export { LiveProtocolError, LiveResult, LiveSnapshot, parseLiveServerFrame, LIVE_TYPES } from './protocol.js'
export type {
  LiveChange, LiveChangesFrame, LiveColumn, LiveErrorFrame, LiveProgressFrame, LiveRenewedFrame, LiveRow, LiveRowsFrame,
  LiveServerFrame, LiveValues,
} from './protocol.js'
