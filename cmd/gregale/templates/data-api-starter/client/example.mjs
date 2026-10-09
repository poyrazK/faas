import { notesClient, noteCursor } from './dist/notes.js'

const url = process.env.DATA_API_URL
const subject = process.env.DATA_API_SUBJECT
const token = process.env.DATA_API_TOKEN
if (!url || !subject || !token) {
  console.error('Set DATA_API_URL, DATA_API_SUBJECT and an application DATA_API_TOKEN')
  process.exitCode = 1
} else {
  const notes = notesClient({ url, subject, accessToken: () => token })
  const signal = AbortSignal.timeout(5000)
  const result = await notes.cursorPage({ size: 20 }).abortSignal(signal).retry(false)
  if (result.error) {
    console.error(signal.aborted ? 'Read canceled or timed out' : result.status === 401 ? 'Renew the application session and try again' : 'Could not read notes; check the application session and API configuration')
    process.exitCode = 1
  } else {
    const last = result.data.at(-1)
    console.log({ rows: result.data, nextCursor: last ? noteCursor(last) : null })
  }
}
