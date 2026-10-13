import { classifyDataError, type ClassifiedDataError, type DataErrorCategory, type DataErrorRecovery } from '../src/index.js'
const error: { code: string; details: string; applicationId: number } | null = Math.random() ? { code: '23505', details: '', applicationId: 42 } : null
const failure = classifyDataError(error, { status: 409 })
if (failure) {
  const category: DataErrorCategory = failure.category
  const recovery: DataErrorRecovery = failure.recovery
  const originalId: number = failure.original.applicationId
  const typed: ClassifiedDataError<NonNullable<typeof error>> = failure
  void [category, recovery, originalId, typed]
  // @ts-expect-error classification metadata is readonly
  failure.category = 'unknown'
  // @ts-expect-error status must be numeric
  classifyDataError(error, { status: '409' })
}
const caught: unknown = new Error('failure')
const classified = classifyDataError(caught)
if (classified) {
  // @ts-expect-error callers must narrow unknown original errors
  classified.original.code
}
