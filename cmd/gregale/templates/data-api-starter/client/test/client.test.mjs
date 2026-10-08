import test from 'node:test'
import assert from 'node:assert/strict'
import { notesClient } from '../dist/notes.js'

test('typed notes use renewed application tokens and schema projections', async () => {
  let token = 'application-token-1'
  const requests = []
  const client = notesClient({
    url: 'https://notes.example', subject: 'user-subject', accessToken: () => token,
    fetch: async (url, init) => {
      requests.push({ url: String(url), init })
      return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })
    },
  })
  await client.list()
  token = 'application-token-2'
  await client.list()
  assert.equal(requests[0].init.headers.get('Authorization'), 'Bearer application-token-1')
  assert.equal(requests[1].init.headers.get('Authorization'), 'Bearer application-token-2')
  assert.equal(requests[0].init.headers.get('Accept-Profile'), 'api')
  assert.equal(new URL(requests[0].url).pathname, '/rest/v1/notes')
  assert.equal(new URL(requests[0].url).searchParams.get('select'), 'id,body,priority,created_at')
  assert.equal(requests[0].init.redirect, 'error')
  assert.equal(requests[0].init.credentials, 'omit')
})
