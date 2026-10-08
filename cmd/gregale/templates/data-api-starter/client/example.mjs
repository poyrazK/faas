import { notesClient } from './dist/notes.js'

const url = process.env.DATA_API_URL
const subject = process.env.DATA_API_SUBJECT
const token = process.env.DATA_API_TOKEN
if (!url || !subject || !token) {
  console.error('Set DATA_API_URL, DATA_API_SUBJECT and an application DATA_API_TOKEN')
  process.exitCode = 1
} else {
  const notes = notesClient({ url, subject, accessToken: () => token })
  const result = await notes.page({ offset: 0, size: 20 })
  if (result.error) {
    console.error('Could not read notes; check the application session and API configuration')
    process.exitCode = 1
  } else {
    console.log({ rows: result.data, total: result.count, nextOffset: result.data.length < result.count ? result.data.length : null })
  }
}
