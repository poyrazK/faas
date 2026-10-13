import { notesClient } from '../src/notes.js'
import { createNoteForm } from '../src/note-form.js'
import type { DataClientOptions } from '@gregale/data'

export function readBrowserNotes(options: { url: string; subject: string; accessToken: () => string | Promise<string>; signal: AbortSignal; onResponse?: DataClientOptions['onResponse']; expectedSchemaFingerprint?: DataClientOptions['expectedSchemaFingerprint'] }) {
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

const noteForm = document.querySelector<HTMLFormElement>('#note-form')
if (noteForm && form) {
  let controller: ReturnType<typeof createNoteForm> | undefined
  let noteToken = ''
  let working = false
  const save = noteForm.querySelector<HTMLButtonElement>('#save-note')!
  const recover = noteForm.querySelector<HTMLButtonElement>('#recover-note')!
  const output = document.querySelector<HTMLElement>('#note-result')!
  const protectedInputs = [...Array.from(form.querySelectorAll<HTMLInputElement>('[name=url],[name=subject]')), ...Array.from(noteForm.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>('[name=body],[name=tags]'))]
  const sync = () => {
    const pending = Boolean(controller?.pending)
    protectedInputs.forEach(input => { input.disabled = working || pending })
    save.disabled = working || pending
    recover.hidden = !pending
    recover.disabled = working
    form.querySelector<HTMLButtonElement>('button')!.disabled = working || pending
  }
  const run = async (recovering: boolean) => {
    if (working) return
    for (const field of ['body', 'tags']) {
      document.querySelector<HTMLElement>(`#${field}-error`)!.textContent = ''
      noteForm.querySelector(`[name=${field}]`)!.removeAttribute('aria-invalid')
    }
    noteToken = form.querySelector<HTMLInputElement>('[name=token]')!.value
    form.querySelector<HTMLInputElement>('[name=token]')!.value = ''
    working = true
    sync()
    try {
      controller ??= createNoteForm(notesClient({
        url: form.querySelector<HTMLInputElement>('[name=url]')!.value,
        subject: form.querySelector<HTMLInputElement>('[name=subject]')!.value,
        accessToken: () => noteToken,
      }))
      const input = { body: noteForm.querySelector<HTMLTextAreaElement>('[name=body]')!.value, tags: noteForm.querySelector<HTMLInputElement>('[name=tags]')!.value }
      const result = recovering ? await controller.reconcile() : await controller.submit(input)
      if (result.outcome === 'validation_failed') {
        for (const [field, message] of Object.entries(result.fields)) {
          document.querySelector<HTMLElement>(`#${field}-error`)!.textContent = message
          noteForm.querySelector(`[name=${field}]`)!.setAttribute('aria-invalid', 'true')
        }
        output.textContent = 'Check the highlighted fields.'
      } else if (result.outcome === 'saved') {
        output.textContent = `Saved note ${result.note.id}: ${result.note.body}`
        noteForm.reset()
      } else if (result.outcome === 'failed') {
        output.textContent = result.message + (result.uncertain ? ' Keep this page open. Restore the same user’s session and resolve the pending save promptly.' : '')
      }
    } catch { output.textContent = 'Check the Data API URL and application session.' }
    finally {
      noteToken = ''
      working = false
      if (!controller?.pending) controller = undefined
      sync()
    }
  }
  noteForm.addEventListener('submit', event => { event.preventDefault(); void run(false) })
  recover.addEventListener('click', () => { void run(true) })
}
