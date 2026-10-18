import type { JsonParser } from './validation.js'
import { reconcileMutation, type MutationReconciliation } from './reconcile.js'

// Mirrored from pkg/api/limits.go, enforced by the contract parity test (ADR-1674).
export const MUTATION_JOURNAL_LIMITS = Object.freeze({ maxRecords: 100, maxBytes: 8388608, maxEntryBytes: 1048576, maxSlotBytes: 256, maxDepth: 64 })
export type MutationJournalErrorCode = 'storage_unavailable' | 'storage_full' | 'conflict' | 'invalid_record' | 'invalid_operation' | 'context_mismatch' | 'invalid_entry' | 'not_prepared' | 'not_confirmed'
export class MutationJournalError extends Error {
  constructor(readonly code: MutationJournalErrorCode) {
    super('Mutation journal: ' + code)
    this.name = 'MutationJournalError'
  }
}
export type MutationJournalContext = Readonly<{ apiURL: string; schemaFingerprint: string | null; subject: string; tenant: string | null; contract: string }>
export type JournalOperation = MutationJournalContext & Readonly<{ key: string }>
export type MutationJournalState = 'prepared' | 'uncertain' | 'confirmed'
export type MutationJournalEntry<Operation extends JournalOperation> = Readonly<{ slot: string; revision: string; state: MutationJournalState; operation: Operation }>
/** compareExchange must atomically compare exact bytes and commit across all
 * users of this store before resolving true. A localStorage read/write pair
 * does not satisfy this contract. get returns untrusted serialized data.
 */
export type MutationJournalStore = {
  get(slot: string): Promise<unknown>
  compareExchange(slot: string, expected: string | null, next: string | null): Promise<boolean>
}
export type JournalReconciliation<Operation extends JournalOperation, Row, Error> = {
  result: MutationReconciliation<Row, Error>
  entry: MutationJournalEntry<Operation>
  persistence: 'unchanged' | 'saved' | 'conflict' | 'storage_failed'
}
type Response<Row, Error> = { data: Row[] | null; error: Error | null; status: number }
const keyPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/
const revisionPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)
export const journalByteLength = (value: string): number => new TextEncoder().encode(value).byteLength
export function journalSlot(slot: string): void {
  if (typeof slot !== 'string' || !slot || slot.includes('\0') || journalByteLength(slot) > MUTATION_JOURNAL_LIMITS.maxSlotBytes) throw new MutationJournalError('invalid_entry')
}

// Strict, bounded JSON serialization never invokes getters or toJSON. Sorting
// object keys makes restore validation independent of parser property order.
function canonicalJSON(value: unknown): string {
  const parts: string[] = [], ancestors = new Set<object>()
  let bytes = 0
  const append = (text: string) => {
    bytes += journalByteLength(text)
    if (bytes > MUTATION_JOURNAL_LIMITS.maxEntryBytes) throw new Error()
    parts.push(text)
  }
  const visit = (item: unknown, depth: number): void => {
    if (depth > MUTATION_JOURNAL_LIMITS.maxDepth) throw new Error()
    if (item === null || typeof item === 'boolean' || typeof item === 'string' || (typeof item === 'number' && Number.isFinite(item))) { append(JSON.stringify(item)); return }
    if (typeof item !== 'object' || ancestors.has(item)) throw new Error()
    const array = Array.isArray(item), proto = Object.getPrototypeOf(item)
    if (array ? proto !== Array.prototype : proto !== Object.prototype && proto !== null) throw new Error()
    const keys = Reflect.ownKeys(item)
    if (keys.some(key => typeof key !== 'string')) throw new Error()
    if (array && (item.length > MUTATION_JOURNAL_LIMITS.maxEntryBytes || keys.length !== item.length + 1)) throw new Error()
    ancestors.add(item)
    append(array ? '[' : '{')
    const names = array ? Array.from({ length: item.length }, (_, index) => String(index)) : (keys as string[]).sort()
    names.forEach((name, index) => {
      const field = Object.getOwnPropertyDescriptor(item, name)
      if (!field || !('value' in field) || !field.enumerable) throw new Error()
      if (index) append(',')
      if (!array) append(JSON.stringify(name) + ':')
      visit(field.value, depth + 1)
    })
    append(array ? ']' : '}')
    ancestors.delete(item)
  }
  visit(value, 0)
  return parts.join('')
}
function freeze<Value>(value: Value): Value {
  if (value !== null && typeof value === 'object') { Object.values(value).forEach(freeze); Object.freeze(value) }
  return value
}
function storageError(error: unknown): MutationJournalError {
  const allowed = ['storage_unavailable', 'storage_full', 'conflict', 'invalid_record']
  return new MutationJournalError(error instanceof MutationJournalError && allowed.includes(error.code) ? error.code : 'storage_unavailable')
}

/** Opt-in durable bookkeeping. Loading never sends requests. Only dispatch
 * claims a prepared entry, and it commits uncertainty before calling send.
 * Recovery callbacks must perform receipt reads. Nothing retries or expires
 * an entry automatically; the database is authoritative about key expiry.
 */
export function createMutationJournal<Operation extends JournalOperation>(options: {
  store: MutationJournalStore; context: MutationJournalContext; parser: JsonParser<Operation>
}) {
  let context: MutationJournalContext
  try {
    const input = JSON.parse(canonicalJSON(options.context))
    if (!object(input) || Object.keys(input).sort().join(',') !== 'apiURL,contract,schemaFingerprint,subject,tenant' ||
        typeof input.apiURL !== 'string' || typeof input.subject !== 'string' || !input.subject ||
        !(input.tenant === null || typeof input.tenant === 'string' && input.tenant.length > 0) ||
        typeof input.contract !== 'string' || !input.contract ||
        !(input.schemaFingerprint === null || typeof input.schemaFingerprint === 'string' && /^[a-f0-9]{64}$/.test(input.schemaFingerprint))) throw new Error()
    const url = new URL(input.apiURL)
    if (url.username || url.password || url.search || url.hash || url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))) throw new Error()
    context = Object.freeze({ ...input, apiURL: url.toString().replace(/\/$/, '') }) as MutationJournalContext
    if (typeof options.store.get !== 'function' || typeof options.store.compareExchange !== 'function' || typeof options.parser.parse !== 'function') throw new Error()
  } catch { throw new MutationJournalError('invalid_operation') }
  const handles = new WeakMap<MutationJournalEntry<Operation>, string>()
  const validate = (value: unknown, code: 'invalid_operation' | 'invalid_record'): Operation => {
    try {
      const encoded = canonicalJSON(value), clean: unknown = JSON.parse(encoded)
      if (!object(clean) || typeof clean.key !== 'string' || !keyPattern.test(clean.key)) throw new Error()
      if (Object.entries(context).some(([field, expected]) => clean[field] !== expected)) throw new MutationJournalError('context_mismatch')
      const parsed = options.parser.parse(clean)
      if (parsed !== null && typeof parsed === 'object' && 'then' in parsed && typeof parsed.then === 'function') {
        void Promise.resolve(parsed).catch(() => {})
        throw new Error()
      }
      if (canonicalJSON(parsed) !== encoded) throw new Error() // Never normalize or replace saved inputs on restore.
      return freeze(JSON.parse(encoded)) as Operation
    } catch (error) {
      if (error instanceof MutationJournalError && error.code === 'context_mismatch') throw new MutationJournalError('context_mismatch')
      throw new MutationJournalError(code)
    }
  }
  const encode = (operation: Operation, state: MutationJournalState) => {
    try { return canonicalJSON({ version: 1, revision: crypto.randomUUID(), state, operation }) }
    catch { throw new MutationJournalError('invalid_operation') }
  }
  const decode = (slot: string, raw: unknown): MutationJournalEntry<Operation> => {
    try {
      if (typeof raw !== 'string' || raw.length > MUTATION_JOURNAL_LIMITS.maxEntryBytes || journalByteLength(raw) > MUTATION_JOURNAL_LIMITS.maxEntryBytes) throw new Error()
      const value: unknown = JSON.parse(raw)
      if (!object(value) || Object.keys(value).sort().join(',') !== 'operation,revision,state,version' || value.version !== 1 ||
          typeof value.revision !== 'string' || !revisionPattern.test(value.revision) ||
          !['prepared', 'uncertain', 'confirmed'].includes(value.state as string) || canonicalJSON(value) !== raw) throw new Error()
      const operation = validate(value.operation, 'invalid_record')
      const entry = Object.freeze({ slot, revision: value.revision, state: value.state as MutationJournalState, operation })
      handles.set(entry, raw)
      return entry
    } catch (error) {
      if (error instanceof MutationJournalError && error.code === 'context_mismatch') throw new MutationJournalError('context_mismatch')
      throw new MutationJournalError('invalid_record')
    }
  }
  const original = (entry: MutationJournalEntry<Operation>): string => {
    const raw = entry && handles.get(entry)
    if (!raw) throw new MutationJournalError('invalid_entry')
    return raw
  }
  const get = async (slot: string) => {
    try { return await options.store.get(slot) } catch (error) { throw storageError(error) }
  }
  const exchange = async (slot: string, expected: string | null, next: string | null) => {
    try { if (await options.store.compareExchange(slot, expected, next) !== true) throw new MutationJournalError('conflict') }
    catch (error) { throw storageError(error) }
  }
  async function prepare(slot: string, operation: Operation): Promise<MutationJournalEntry<Operation>> {
    journalSlot(slot)
    const raw = encode(validate(operation, 'invalid_operation'), 'prepared')
    await exchange(slot, null, raw)
    return decode(slot, raw)
  }
  async function load(slot: string): Promise<MutationJournalEntry<Operation> | null> {
    journalSlot(slot)
    const raw = await get(slot)
    return raw === null ? null : decode(slot, raw)
  }
  async function dispatch<Result>(entry: MutationJournalEntry<Operation>, send: (operation: Operation) => PromiseLike<Result>): Promise<{ entry: MutationJournalEntry<Operation>; response: Awaited<Result> }> {
    const before = original(entry)
    if (entry.state !== 'prepared') throw new MutationJournalError('not_prepared')
    const next = encode(entry.operation, 'uncertain')
    await exchange(entry.slot, before, next)
    const claimed = decode(entry.slot, next)
    return { entry: claimed, response: await send(claimed.operation) }
  }
  function reconcile<Row, Error, Matched extends Row>(entry: MutationJournalEntry<Operation>, response: Response<Row, Error>, matches: (row: Row) => row is Matched): Promise<JournalReconciliation<Operation, Matched, Error>>
  function reconcile<Row, Error>(entry: MutationJournalEntry<Operation>, response: Response<Row, Error>, matches: (row: Row) => boolean): Promise<JournalReconciliation<Operation, Row, Error>>
  async function reconcile<Row, Error>(entry: MutationJournalEntry<Operation>, response: Response<Row, Error>, matches: (row: Row) => boolean): Promise<JournalReconciliation<Operation, Row, Error>> {
    const before = original(entry), result = reconcileMutation(response, matches)
    if (result.outcome !== 'confirmed' || entry.state === 'confirmed') return { result, entry, persistence: 'unchanged' }
    try {
      const next = encode(entry.operation, 'confirmed')
      await exchange(entry.slot, before, next)
      return { result, entry: decode(entry.slot, next), persistence: 'saved' }
    } catch (error) {
      return { result, entry, persistence: error instanceof MutationJournalError && error.code === 'conflict' ? 'conflict' : 'storage_failed' }
    }
  }
  function recover<Row, Error, Matched extends Row>(entry: MutationJournalEntry<Operation>, lookup: (operation: Operation) => PromiseLike<Response<Row, Error>>, matches: (row: Row) => row is Matched): Promise<JournalReconciliation<Operation, Matched, Error>>
  function recover<Row, Error>(entry: MutationJournalEntry<Operation>, lookup: (operation: Operation) => PromiseLike<Response<Row, Error>>, matches: (row: Row) => boolean): Promise<JournalReconciliation<Operation, Row, Error>>
  async function recover<Row, Error>(entry: MutationJournalEntry<Operation>, lookup: (operation: Operation) => PromiseLike<Response<Row, Error>>, matches: (row: Row) => boolean): Promise<JournalReconciliation<Operation, Row, Error>> {
    const before = original(entry)
    if (await get(entry.slot) !== before) throw new MutationJournalError('conflict')
    return reconcile(entry, await lookup(entry.operation), matches)
  }
  async function removeConfirmed(entry: MutationJournalEntry<Operation>): Promise<void> {
    const raw = original(entry)
    if (entry.state !== 'confirmed') throw new MutationJournalError('not_confirmed')
    await exchange(entry.slot, raw, null)
  }
  return { prepare, load, dispatch, reconcile, recover, removeConfirmed }
}
