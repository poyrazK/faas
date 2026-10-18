import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('receipt diagnostic timeouts match central platform limits', async () => {
  const core = await readFile(new URL('../../cmd/gregale/templates/data-api-starter/migrations/receipt-diagnostics-core.mjs', import.meta.url), 'utf8')
  const limits = await readFile(new URL('../../pkg/api/limits.go', import.meta.url), 'utf8')
  for (const [field, name] of Object.entries({ connectMs: 'Connect', statementMs: 'Statement', lockMs: 'Lock', queryMs: 'Query' })) {
    const central = Number(limits.match(new RegExp(`DataAPIReceiptDiagnostics${name}MS\\s*=\\s*(\\d+)`))?.[1])
    assert.ok(central > 0); assert.equal(Number(core.match(new RegExp(`${field}: (\\d+)`))?.[1]), central)
  }
})
