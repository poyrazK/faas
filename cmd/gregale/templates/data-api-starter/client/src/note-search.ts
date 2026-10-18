export const NOTE_SEARCH_LIMITS = Object.freeze({ maxQueryBytes: 1024, maxTerms: 16, maxPageSize: 100, maxCursorBytes: 128 })

declare const searchCursorBrand: unique symbol
export type NoteSearchCursor = string & { readonly [searchCursorBrand]: true }
export type NoteSearchResult = Readonly<{ id: number; body: string; priority: number; created_at: string; version: number; rank: number }>
export type NoteSearchPage = Readonly<{ next_cursor: NoteSearchCursor | null; has_more: boolean; results: readonly NoteSearchResult[] }>

const integer = (value: unknown, min = 1, max = 2147483647): value is number => typeof value === 'number' && Number.isInteger(value) && value >= min && value <= max
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)
const fields = (value: Record<string, unknown>, keys: readonly string[]) => Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key))

export function noteSearchText(value: string): string {
  if (typeof value !== 'string' || !value.trim() || /\u0000|[\uD800-\uDFFF]/u.test(value) || new TextEncoder().encode(value).length > NOTE_SEARCH_LIMITS.maxQueryBytes) {
    throw new TypeError('Search requires nonempty text within the query byte limit')
  }
  return value
}

export function noteSearchPageSize(value: number): number {
  if (!integer(value, 1, NOTE_SEARCH_LIMITS.maxPageSize)) throw new RangeError('Search page size is outside the allowed range')
  return value
}

export function noteSearchCursor(value: unknown): NoteSearchCursor {
  if (typeof value !== 'string' || value.length > NOTE_SEARCH_LIMITS.maxCursorBytes || value.trim() !== value) throw new TypeError('Invalid search cursor')
  const match = /^v1\.[0-9a-f]{64}\.(0|[1-9][0-9]{0,6})\.([1-9][0-9]{0,9})$/.exec(value)
  if (!match || !integer(Number(match[1]), 0, 1000000) || !integer(Number(match[2]))) throw new TypeError('Invalid search cursor')
  return value as NoteSearchCursor
}

export function parseNoteSearchPage(value: unknown, after?: string): NoteSearchPage {
  if (!object(value) || !fields(value, ['next_cursor', 'has_more', 'results']) || typeof value.has_more !== 'boolean' || !Array.isArray(value.results) || value.results.length > NOTE_SEARCH_LIMITS.maxPageSize) {
    throw new TypeError('Invalid search page')
  }
  const previous = after === undefined ? undefined : noteSearchCursor(after)
  const next = value.next_cursor === null ? null : noteSearchCursor(value.next_cursor)
  if (value.has_more !== (next !== null) || (value.has_more && value.results.length === 0) || (previous && next && previous.slice(0, 68) !== next.slice(0, 68))) throw new TypeError('Invalid search continuation')
  let lastRank = previous ? Number(previous.split('.')[2]) : Infinity
  let lastID = previous ? Number(previous.split('.')[3]) : Infinity
  const results: NoteSearchResult[] = []
  for (const row of value.results) {
    if (!object(row) || !fields(row, ['id', 'body', 'priority', 'created_at', 'version', 'rank']) || !integer(row.id) || typeof row.body !== 'string' || !integer(row.priority, -2147483648) || !integer(row.version) || !integer(row.rank, 0, 1000000) || typeof row.created_at !== 'string' || !Number.isFinite(Date.parse(row.created_at))) throw new TypeError('Invalid search result')
    if (row.rank > lastRank || (row.rank === lastRank && row.id >= lastID)) throw new TypeError('Unordered search results')
    lastRank = row.rank; lastID = row.id
    results.push(Object.freeze({ id: row.id, body: row.body, priority: row.priority, created_at: row.created_at, version: row.version, rank: row.rank }))
  }
  if (next && (Number(next.split('.')[2]) !== lastRank || Number(next.split('.')[3]) !== lastID)) throw new TypeError('Search cursor does not match the last result')
  return Object.freeze({ next_cursor: next, has_more: value.has_more, results: Object.freeze(results) })
}
