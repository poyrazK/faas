// End-to-end driver for the live-query SDK (ADR-20261016151947). The Go test
// TestSDKEndToEnd in pkg/livequery/liveserver starts a hub over real
// PostgreSQL and runs this script against it: LIVE_URL is the WebSocket and
// SEAL_URL returns fresh seals. The script reports the query's state as JSON
// lines on stdout and takes commands as JSON lines on stdin.
import { createInterface } from 'node:readline'
import { LiveConnection } from '../dist/live/index.js'

const emit = (event) => process.stdout.write(JSON.stringify(event) + '\n')
const fetchSeal = async () => {
  const response = await fetch(process.env.SEAL_URL)
  if (!response.ok) throw new Error(`seal endpoint: ${response.status}`)
  return response.text()
}

const connection = new LiveConnection({
  connect: () => new WebSocket(process.env.LIVE_URL),
  reconnectDelayMs: () => 100,
})
const feed = connection.query({ sealed: await fetchSeal(), refresh: fetchSeal })
let last = ''
feed.subscribe(() => {
  const s = feed.getSnapshot()
  const state = { event: 'state', status: s.status, rows: s.rows.map((r) => `${r.id}:${r.body}`), error: s.error?.code ?? null }
  const text = JSON.stringify(state)
  if (text !== last) { last = text; emit(state) }
})

for await (const line of createInterface({ input: process.stdin })) {
  const command = JSON.parse(line)
  if (command.await) {
    try {
      await feed.awaitTxId(command.await, { signal: AbortSignal.timeout(20_000) })
      emit({ event: 'seen', xid: command.await, rows: feed.getSnapshot().rows.map((r) => `${r.id}:${r.body}`) })
    } catch (err) {
      emit({ event: 'await-failed', xid: command.await, error: String(err) })
    }
  }
  if (command.exit) break
}
connection.close()
