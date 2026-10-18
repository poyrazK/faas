import { MutationJournalError, MUTATION_JOURNAL_LIMITS, journalByteLength, journalSlot, type MutationJournalStore } from './mutation-journal.js'

/** A dedicated, explicitly selected IndexedDB database. Read/write transactions
 * serialize CAS and aggregate limits across tabs. Strict durability is required;
 * storage errors never fall back to an in-memory or localStorage write.
 */
export function createIndexedDBMutationStore(options: { databaseName: string; maxRecords?: number; maxBytes?: number }): MutationJournalStore & { close(): void } {
  const databaseName = options.databaseName
  journalSlot(databaseName)
  const maxRecords = options.maxRecords ?? MUTATION_JOURNAL_LIMITS.maxRecords
  const maxBytes = options.maxBytes ?? MUTATION_JOURNAL_LIMITS.maxBytes
  if (!Number.isSafeInteger(maxRecords) || maxRecords < 1 || maxRecords > MUTATION_JOURNAL_LIMITS.maxRecords ||
      !Number.isSafeInteger(maxBytes) || maxBytes < 1 || maxBytes > MUTATION_JOURNAL_LIMITS.maxBytes) throw new MutationJournalError('invalid_entry')
  let connection: Promise<IDBDatabase> | undefined, closed = false
  const open = (): Promise<IDBDatabase> => {
    if (closed) return Promise.reject(new MutationJournalError('storage_unavailable'))
    return connection ??= new Promise((resolve, reject) => {
      let failed = false
      try {
        const factory = globalThis.indexedDB
        if (!factory) throw new Error()
        const request = factory.open(databaseName, 1)
        request.onupgradeneeded = () => { request.result.createObjectStore('entries') }
        request.onerror = request.onblocked = () => { failed = true; reject(new MutationJournalError('storage_unavailable')) }
        request.onsuccess = () => {
          const db = request.result
          if (closed || failed) { db.close(); reject(new MutationJournalError('storage_unavailable')); return }
          db.onversionchange = () => { closed = true; db.close() }
          resolve(db)
        }
      } catch { reject(new MutationJournalError('storage_unavailable')) }
    })
  }
  async function get(slot: string): Promise<unknown> {
    journalSlot(slot)
    const db = await open()
    return new Promise((resolve, reject) => {
      try {
        const transaction = db.transaction('entries', 'readonly')
        const request = transaction.objectStore('entries').openCursor(slot)
        let value: unknown = null, invalid = false
        request.onsuccess = () => {
          if (request.result) { value = request.result.value; if (typeof value !== 'string') { invalid = true; transaction.abort() } }
        }
        transaction.oncomplete = () => resolve(value)
        transaction.onabort = () => reject(new MutationJournalError(invalid ? 'invalid_record' : 'storage_unavailable'))
      } catch { reject(new MutationJournalError('storage_unavailable')) }
    })
  }
  async function compareExchange(slot: string, expected: string | null, next: string | null): Promise<boolean> {
    journalSlot(slot)
    for (const value of [expected, next]) if (value !== null && (typeof value !== 'string' || journalByteLength(value) > MUTATION_JOURNAL_LIMITS.maxEntryBytes)) throw new MutationJournalError('invalid_record')
    const db = await open()
    return new Promise((resolve, reject) => {
      try {
        const transaction = db.transaction('entries', 'readwrite', { durability: 'strict' })
        if (transaction.durability !== 'strict') { transaction.abort(); reject(new MutationJournalError('storage_unavailable')); return }
        const store = transaction.objectStore('entries')
        let matched = false, failure: MutationJournalError | undefined
        const abort = (code: 'invalid_record' | 'storage_full') => { failure = new MutationJournalError(code); transaction.abort() }
        transaction.oncomplete = () => resolve(matched)
        transaction.onabort = () => reject(failure ?? new MutationJournalError(transaction.error?.name === 'QuotaExceededError' ? 'storage_full' : 'storage_unavailable'))
        const current = store.openCursor(slot)
        current.onsuccess = () => {
          const saved: unknown = current.result ? current.result.value : null
          if (current.result && typeof saved !== 'string') { abort('invalid_record'); return }
          if (saved !== expected) return
          matched = true
          let count = 0, bytes = 0
          const scan = store.openCursor()
          scan.onsuccess = () => {
            const cursor = scan.result
            if (cursor) {
              if (typeof cursor.key !== 'string' || typeof cursor.value !== 'string' || cursor.value.length > MUTATION_JOURNAL_LIMITS.maxEntryBytes) { abort('invalid_record'); return }
              count++; bytes += journalByteLength(cursor.value)
              if (count > MUTATION_JOURNAL_LIMITS.maxRecords || bytes > MUTATION_JOURNAL_LIMITS.maxBytes) { abort('storage_full'); return }
              cursor.continue(); return
            }
            if (next === null) { store.delete(slot); return }
            const nextCount = count + (expected === null ? 1 : 0)
            const nextBytes = bytes - (expected === null ? 0 : journalByteLength(expected)) + journalByteLength(next)
            if (nextCount > maxRecords || nextBytes > maxBytes) { abort('storage_full'); return }
            store.put(next, slot)
          }
        }
      } catch { reject(new MutationJournalError('storage_unavailable')) }
    })
  }
  function close(): void {
    closed = true
    void connection?.then(db => db.close()).catch(() => {})
  }
  return { get, compareExchange, close }
}
