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

async function relationshipContract(client: ReturnType<typeof notesClient>) {
  const notes = await client.listWithReplies()
  if (notes.data) for (const note of notes.data) {
    const replies: { id: number; body: string }[] = note.comments
    const summary: { summary: string } | null = note.note_details
    // @ts-expect-error a one-to-many embed is an array, not one object
    const single: { body: string } = note.comments
    // @ts-expect-error a reverse one-to-one embed can be absent
    const present: { summary: string } = note.note_details
    // @ts-expect-error nested projections only contain selected columns
    const subject = note.comments[0].subject
    void [replies, summary, single, present, subject]
  }
  const comments = await client.listComments()
  if (comments.data) for (const comment of comments.data) {
    const parent: { id: number; body: string } | null = comment.notes
    // @ts-expect-error nullable composite FKs produce a nullable parent embed
    const required: { id: number; body: string } = comment.notes
    // @ts-expect-error nested column types come from the referenced table
    const wrong: number | undefined = comment.notes?.body
    void [parent, required, wrong]
  }
  const inner = await client.db.from('comments').select('id,notes!inner(id)').single()
  if (inner.data) {
    const id: number = inner.data.notes.id
    void id
  }
}
void relationshipContract

async function tagContract(client: ReturnType<typeof notesClient>) {
  const result = await client.listWithTags()
  if (result.data) for (const note of result.data) {
    const tags: { id: number; name: string }[] = note.tags
    // @ts-expect-error many-to-many embeds are arrays
    const single: { name: string } = note.tags
    // @ts-expect-error unselected columns are absent
    const subject = note.tags[0].subject
    // @ts-expect-error tag names are text
    const name: number = note.tags[0].name
    void [tags, single, subject, name]
  }
  const reverse = await client.listTaggedNotes()
  if (reverse.data) for (const tag of reverse.data) {
    const notes: { id: number; body: string }[] = tag.notes
    void notes
  }
}
void tagContract
