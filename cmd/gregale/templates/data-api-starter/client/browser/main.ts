import { notesClient } from '../src/notes.js'

export function readBrowserNotes(options: { url: string; subject: string; accessToken: () => string | Promise<string>; signal: AbortSignal }) {
  return notesClient(options).page({ size: 20 }).abortSignal(options.signal).retry(false)
}

const form = document.querySelector<HTMLFormElement>('#session')
form?.addEventListener('submit', async event => {
  event.preventDefault()
  const output = document.querySelector<HTMLElement>('#result')!
  const fields = new FormData(form)
  let token = String(fields.get('token') ?? '')
  const tokenInput = form.querySelector<HTMLInputElement>('[name=token]')!
  tokenInput.value = ''
  try {
    const result = await readBrowserNotes({ url: String(fields.get('url')), subject: String(fields.get('subject')), accessToken: () => token, signal: AbortSignal.timeout(5000) })
    output.textContent = result.error ? result.status === 401 ? 'Session expired or invalid; sign in again.' : 'Read failed or canceled. Check the API and allowed origin.' : JSON.stringify({ rows: result.data, total: result.count }, null, 2)
  } catch { output.textContent = 'Read failed. Check the application session and API configuration.' }
  finally { token = '' }
})
