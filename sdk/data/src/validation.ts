/** A synchronous parser, such as a Zod schema. Null fields are passed through it. */
export type JsonParser<Output = unknown> = { parse(value: unknown): Output }
type Parsers = Record<string, JsonParser>
type Row<Data> = NonNullable<Data> extends readonly (infer Item)[] ? Item : NonNullable<Data>
type CheckedParsers<Data, Fields extends Parsers> = {
  [Key in keyof Fields]: Key extends string
    ? Key extends keyof Row<Data>
      ? Extract<ReturnType<Fields[Key]['parse']>, PromiseLike<unknown>> extends never ? Fields[Key] : never
      : never
    : never
}
type Parsed<Row, Fields extends Parsers> = Omit<Row, keyof Fields> & { -readonly [Key in keyof Fields]: ReturnType<Fields[Key]['parse']> }
export type ValidatedJson<Data, Fields extends Parsers> = Data extends null ? null
  : Data extends readonly (infer Item)[] ? Parsed<Item, Fields>[] : Parsed<Data, Fields>

/** Deliberately omits the input and original parser error, which may contain secrets. */
export class JsonValidationError extends Error {
  readonly code = 'JSON_VALIDATION_FAILED'
  constructor(readonly rowIndex: number | null, readonly field: string) {
    super(`JSON validation failed at ${rowIndex === null ? 'row' : `row ${rowIndex}`}, field ${JSON.stringify(field)}`)
    this.name = 'JsonValidationError'
  }
}

/** Validate selected top-level fields without mutating the input.
 * Call after checking the query error. A null single-row result stays null.
 * Array row indexes in errors are zero-based; single-row errors use null.
 */
export function validateJson<Data extends object | readonly object[] | null, const Fields extends Parsers>(
  data: Data,
  fields: Fields & CheckedParsers<Data, Fields>
    & (string extends keyof Fields ? never : unknown),
): ValidatedJson<Data, Fields> {
  const entries = Object.entries(fields)
  const parseRow = (row: object, index: number | null) => {
    const result = { ...row }
    for (const [field, parser] of entries) {
      try {
        if (!Object.hasOwn(row, field)) throw new Error()
        const value: unknown = parser.parse((row as Record<string, unknown>)[field])
        if (value !== null && (typeof value === 'object' || typeof value === 'function') && typeof (value as { then?: unknown }).then === 'function') {
          // Avoid an unhandled rejection from an accidentally asynchronous parser.
          void Promise.resolve(value).catch(() => {})
          throw new Error()
        }
        Object.defineProperty(result, field, { value, enumerable: true, writable: true, configurable: true })
      } catch {
        throw new JsonValidationError(index, field)
      }
    }
    return result
  }
  return (data === null ? null : Array.isArray(data) ? data.map((row, index) => parseRow(row, index)) : parseRow(data, null)) as ValidatedJson<Data, Fields>
}
