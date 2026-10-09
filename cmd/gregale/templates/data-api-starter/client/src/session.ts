import type { NoteCursor, notesClient } from './notes.js'

// Reads can retry once after renewal. Never automatically replay a mutation.
export async function readCursorPageWithSession(options: {
  client: ReturnType<typeof notesClient>
  renewSession: (signal: AbortSignal) => Promise<void>
  signal: AbortSignal
  after?: NoteCursor
  size?: number
}) {
  const read = () => options.client.cursorPage({ after: options.after, size: options.size })
    .abortSignal(options.signal).retry(false)
  const result = await read()
  if (result.status !== 401 || options.signal.aborted) return result
  await options.renewSession(options.signal)
  return read()
}
