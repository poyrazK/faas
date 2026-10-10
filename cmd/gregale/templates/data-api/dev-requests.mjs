import { open, lstat, writeFile, rename, unlink } from 'node:fs/promises'
import { constants } from 'node:fs'
import { randomBytes, createHash } from 'node:crypto'

export const requestLimit = 65536
const fields = (value, allowed) => value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).every(key => allowed.includes(key))
export function validateRequests(value) {
  let groups
  if (value?.version === 1 && fields(value, ['version', 'requests']) && Array.isArray(value.requests)) groups = [{ name: 'default', requests: value.requests }]
  else if (value?.version === 2 && fields(value, ['version', 'scenarios']) && Array.isArray(value.scenarios) && value.scenarios.length > 0 && value.scenarios.length <= 100) groups = value.scenarios
  else throw new Error('Use a version 1 request collection or version 2 scenario collection.')
  const scenarioNames = new Set()
  let count = 0
  for (const group of groups) {
    if (!fields(group, ['name', 'requests']) || typeof group.name !== 'string' || !/^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(group.name) || scenarioNames.has(group.name) || !Array.isArray(group.requests)) throw new Error('Use unique scenario identifiers with ordered requests.')
    scenarioNames.add(group.name)
    count += group.requests.length
    if (count > 100) throw new Error('Use at most 100 requests across all scenarios.')
    const names = new Set()
    for (const request of group.requests) {
      if (!fields(request, ['name', 'method', 'path', 'identity', 'body', 'expect', 'capture']) || typeof request.name !== 'string' || !request.name.trim() || request.name.length > 100 || names.has(request.name)) throw new Error('Requests need unique names of 1–100 characters.')
      names.add(request.name)
      if (!['GET', 'POST', 'PATCH', 'DELETE'].includes(request.method) || !['alice', 'bob'].includes(request.identity)) throw new Error('Use a supported method and Alice or Bob identity.')
      if (typeof request.path !== 'string' || request.path.length > 4096 || !request.path.startsWith('/rest/v1/') || /[\\\s#]/.test(request.path)) throw new Error('Use a relative /rest/v1/ path without fragments or whitespace.')
      const target = new URL(request.path, 'http://127.0.0.1')
      if (target.origin !== 'http://127.0.0.1' || !target.pathname.startsWith('/rest/v1/')) throw new Error('Use a local API path.')
      if ('capture' in request) {
        if (!fields(request.capture, Object.keys(request.capture ?? {})) || Object.keys(request.capture).length > 32) throw new Error('Use at most 32 named captures per request.')
        for (const [name, pointer] of Object.entries(request.capture)) {
          if (!/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(name) || ['constructor', 'prototype', '__proto__'].includes(name) || typeof pointer !== 'string' || pointer.length > 1024 || (pointer !== '' && !pointer.startsWith('/')) || /~(?![01])/.test(pointer)) throw new Error('Capture names must be identifiers and selectors must be JSON Pointers.')
        }
      }
      if ('body' in request && !['POST', 'PATCH'].includes(request.method)) throw new Error('Only POST and PATCH requests accept a body.')
      if (!fields(request.expect, ['status', 'json']) || !Number.isInteger(request.expect.status) || request.expect.status < 100 || request.expect.status > 599) throw new Error('Set an expected HTTP status from 100 to 599.')
    }
  }
  if (Buffer.byteLength(JSON.stringify(value)) > requestLimit) throw new Error('Request collection exceeds 64 KiB.')
  return value
}
export function requestStore(path) {
  let writes = Promise.resolve()
  const read = async (required = false) => {
    let value
    try {
      const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK)
      try {
        const stat = await file.stat()
        if (!stat.isFile() || stat.size > requestLimit) throw new Error('Request collection must be a regular file under 64 KiB.')
        const buffer = Buffer.alloc(requestLimit + 1)
        let length = 0
        while (length < buffer.length) { const { bytesRead } = await file.read(buffer, length, buffer.length - length, length); if (!bytesRead) break; length += bytesRead }
        if (length > requestLimit) throw new Error('Request collection exceeds 64 KiB.')
        value = validateRequests(JSON.parse(buffer.subarray(0, length).toString('utf8')))
      } finally { await file.close() }
    } catch (error) {
      if (error.code !== 'ENOENT' || required) throw new Error('Cannot read data-api.requests.json; check its format, size and file type.')
      value = { version: 1, requests: [] }
    }
    return { ...value, revision: createHash('sha256').update(JSON.stringify(value)).digest('hex') }
  }
  const save = (value, revision) => {
    const operation = writes.then(async () => {
      validateRequests(value)
      if ((await read()).revision !== revision) { const error = new Error('Saved requests changed; reload the collection before saving.'); error.status = 409; throw error }
      try { if (!(await lstat(path)).isFile()) throw new Error('Request collection must be a regular file.') } catch (error) { if (error.code !== 'ENOENT') throw error }
      const temporary = `${path}.${randomBytes(12).toString('hex')}.tmp`
      try { await writeFile(temporary, JSON.stringify(value, null, 2) + '\n', { mode: 0o600, flag: 'wx' }); await rename(temporary, path) } finally { await unlink(temporary).catch(() => {}) }
      return read()
    })
    writes = operation.catch(() => {})
    return operation
  }
  return { read, save }
}
