import { notesClient } from './notes.js'

// Compile-time assertions: weakened or mismatched generated types fail CI.
function editorContract(client: ReturnType<typeof notesClient>) {
  // @ts-expect-error unknown relations must fail in the editor
  client.db.from('missing_relation')
  // @ts-expect-error generated identity values cannot be inserted
  client.db.from('notes').insert({ id: 1, subject: 'user', body: 'hello' })
  // @ts-expect-error body is required for an insert
  client.db.from('notes').insert({ subject: 'user' })
  // @ts-expect-error priority comes from an integer database column
  client.create('hello', 'high')
}
void editorContract
