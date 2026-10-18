import { renderNoteFields } from '../src/schema-form.js'
import { notesClient, noteInputFeedback } from '../src/notes.js'
import { createNoteForm } from '../src/note-form.js'
import type { DataClientOptions } from '@gregale/data'

export function readBrowserNotes(options: { url: string; subject: string; accessToken: () => string | Promise<string>; signal: AbortSignal; onResponse?: DataClientOptions['onResponse']; expectedSchemaFingerprint?: DataClientOptions['expectedSchemaFingerprint'] }) {
  return notesClient(options).page({ size: 20 }).abortSignal(options.signal).retry(false)
}

const createFields=document.querySelector<HTMLElement>('#create-fields')
if(createFields) renderNoteFields(createFields,'insert')
const editFields=document.querySelector<HTMLElement>('#edit-fields')
if(editFields) renderNoteFields(editFields,'update')

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
  const retry = noteForm.querySelector<HTMLButtonElement>('#retry-note')!
  const output = document.querySelector<HTMLElement>('#note-result')!
  const protectedInputs = [...Array.from(form.querySelectorAll<HTMLInputElement>('[name=url],[name=subject]')), ...Array.from(noteForm.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>('[name=body],[name=tags]'))]
  const sync = () => {
    const pending = Boolean(controller?.pending)
    protectedInputs.forEach(input => { input.disabled = working || pending })
    save.disabled = working || pending
    recover.hidden = !pending
    recover.disabled = working
    retry.hidden = !pending
    retry.disabled = working
    form.querySelector<HTMLButtonElement>('button')!.disabled = working || pending
  }
  const run = async (action: 'submit' | 'check' | 'retry') => {
    if (working) return
    for (const field of ['body', 'tags']) {
      document.querySelector<HTMLElement>(`#${field === 'body' ? 'insert-body' : field}-error`)!.textContent = ''
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
      }), { replayContract: 'v2' })
      const input = { body: noteForm.querySelector<HTMLTextAreaElement>('[name=body]')!.value, tags: noteForm.querySelector<HTMLInputElement>('[name=tags]')!.value }
      const result = action === 'check' ? await controller.reconcile() : action === 'retry' ? await controller.retry() : await controller.submit(input)
      if (result.outcome === 'validation_failed') {
        for (const [field, message] of Object.entries(result.fields)) {
          document.querySelector<HTMLElement>(`#${field === 'body' ? 'insert-body' : field}-error`)!.textContent = message
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
  noteForm.addEventListener('submit', event => { event.preventDefault(); void run('submit') })
  recover.addEventListener('click', () => { void run('check') })
  retry.addEventListener('click', () => {
    if (window.confirm('Retry the original save with the same request? Check the pending save first. Retrying after its replay window closes will be refused.')) void run('retry')
  })
}

const editForm=document.querySelector<HTMLFormElement>('#edit-form')
if(editForm&&form){
 let editing=false
 editForm.addEventListener('submit',async event=>{
  event.preventDefault();if(editing)return
  const output=document.querySelector<HTMLElement>('#edit-result')!
  const fields=new FormData(editForm),id=Number(fields.get('id')),version=Number(fields.get('version'))
  const body=String(fields.get('body')??''),priority=String(fields.get('priority')??'').trim()
  const patch={...(body.trim()?{body:body.trim()}:{}),...(priority?{priority:Number(priority)}:{})}
  for(const field of ['body','priority']){
   const error=document.querySelector<HTMLElement>(`#update-${field}-error`);if(error)error.textContent=''
   editForm.querySelector(`[name=${field}]`)?.removeAttribute('aria-invalid')
  }
  if(!Number.isSafeInteger(id)||id<1||id>2147483647||!Number.isSafeInteger(version)||version<1||version>2147483647||!Object.keys(patch).length){output.textContent='Enter a note ID, current version and at least one change.';return}
  if(priority&&(!Number.isSafeInteger(Number(priority))||Number(priority)<-2147483648||Number(priority)>2147483647)){output.textContent='Enter a whole-number priority.';return}
  let editToken=''
  let client:ReturnType<typeof notesClient>
  try{client=notesClient({url:form.querySelector<HTMLInputElement>('[name=url]')!.value,subject:form.querySelector<HTMLInputElement>('[name=subject]')!.value,accessToken:()=>editToken})}
  catch{output.textContent='Enter a valid Data API URL and application subject.';return}
  try{client.validateEdit(patch)}catch(error){
   const feedback=noteInputFeedback(error)
   const field=feedback?.body?'body':'priority',message=feedback?.body??'Enter a valid priority.'
   const target=document.querySelector<HTMLElement>(`#update-${field}-error`);if(target)target.textContent=message
   editForm.querySelector(`[name=${field}]`)?.setAttribute('aria-invalid','true');output.textContent='Check the highlighted fields.';return
  }
  editToken=form.querySelector<HTMLInputElement>('[name=token]')!.value
  form.querySelector<HTMLInputElement>('[name=token]')!.value=''
  editing=true;editForm.querySelector<HTMLButtonElement>('button')!.disabled=true
  try{
   const result=await client.update(id,version,patch,AbortSignal.timeout(5000))
   output.textContent=result.error?result.status===409?'Note changed or is unavailable. Reload before saving.':'Save failed. Check your session and try a fresh read.':`Saved note ${result.data!.id}. Read the new version before editing again.`
  }catch{output.textContent='The save could not be confirmed. Read the note before trying again.'}
  finally{editToken='';editing=false;editForm.querySelector<HTMLButtonElement>('button')!.disabled=false}
 })
}
