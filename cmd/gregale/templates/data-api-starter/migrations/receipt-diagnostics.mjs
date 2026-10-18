import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { inspectReceipts, runDiagnostics } from './receipt-diagnostics-core.mjs'

const contracts = Object.freeze({
  create: { contract: 'gregale.note_create.v2', table: 'note_create_receipts_v2' },
  mutation: { contract: 'gregale.note_mutation.v2', table: 'note_mutation_receipts_v2' },
  batch: { contract: 'gregale.note_batch.v2', table: 'note_batch_receipts_v2' },
})

export function options(args) {
  if (args.length === 0) return { receiptKind: 'all' }
  if (args.length !== 2 || args[0] !== '--receipt-kind' || !['all', ...Object.keys(contracts)].includes(args[1])) throw new Error('Invalid diagnostics options')
  return { receiptKind: args[1] }
}

export function diagnostics(connectionString, { receiptKind = 'all' } = {}) {
  if (receiptKind !== 'all' && !Object.hasOwn(contracts, receiptKind)) throw new Error('Invalid receipt kind')
  return inspectReceipts(connectionString, receiptKind === 'all' ? Object.values(contracts) : [contracts[receiptKind]])
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) await runDiagnostics(diagnostics, options)
