import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { inspectReceipts, runDiagnostics } from './receipt-diagnostics-core.mjs'

export function options(args) {
  if (args.length) throw new Error('Invalid diagnostics options')
  return {}
}

export function diagnostics(connectionString) {
  return inspectReceipts(connectionString, [{ contract: 'gregale.transaction.__OP__.v2', table: '__OP___v2_receipts' }])
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) await runDiagnostics(diagnostics, options)
