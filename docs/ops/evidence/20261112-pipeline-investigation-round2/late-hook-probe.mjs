import assert from 'node:assert/strict'
import http from 'node:http'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { configurePipelineReadGate } from '../../../../cmd/gregale/templates/data-api/pipeline-read-gate.mjs'
import { runtimeConfig } from '../../../../cmd/gregale/templates/data-api/config.mjs'
import { createServer } from '../../../../cmd/gregale/templates/data-api/server.mjs'

const requireRuntime = createRequire(new URL('../../../../cmd/gregale/templates/data-api/package.json', import.meta.url))
const pg = requireRuntime('pg')
const { decodeJwt } = await import(new URL('../../../../cmd/gregale/templates/data-api/node_modules/jose/dist/webapi/index.js', import.meta.url))
const config = runtimeConfig({ DATABASE_URL: process.env.PROBE_READER_DSN, DATA_API_SCHEMAS: 'public', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'mirror', DATA_API_READ_ONLY: 'true' })
assert.equal(await configurePipelineReadGate(config), false)

const admin = new pg.Client({ connectionString: process.env.PROBE_ADMIN_DSN })
await admin.connect()
try {
  await admin.query('CREATE SCHEMA gregale_pipeline')
  const sql = (await readFile(new URL('../../../../pkg/pipelineread/read_gate.sql', import.meta.url), 'utf8')).replaceAll('__PIPELINE_RETRY_SECONDS__', '15')
  await admin.query(sql)
  for (const name of ['check_pipeline_read', 'check_pipeline_request']) {
    await admin.query(`COMMENT ON FUNCTION gregale_data_api.${name}() IS 'gregale.pipeline.read-gate.v1:investigation'`)
  }
  const { rows: [{ reader }] } = await admin.query('SELECT $1::text AS reader', [new URL(process.env.PROBE_READER_DSN).username])
  assert.match(reader, /^pipeline_runtime_[a-f0-9]+$/)
  await admin.query(`GRANT USAGE ON SCHEMA gregale_data_api TO "${reader}"; GRANT EXECUTE ON FUNCTION gregale_data_api.check_pipeline_read(),gregale_data_api.check_pipeline_request() TO "${reader}"`)
} finally { await admin.end() }

const claims = []
const engine = http.createServer((req, res) => {
  claims.push(decodeJwt(req.headers.authorization.slice('Bearer '.length)))
  res.writeHead(200, { 'Content-Type': 'application/json' })
  res.end('[]')
})
await new Promise(resolve => engine.listen(0, '127.0.0.1', resolve))
const gateway = createServer(config, async () => ({ sub: 'alice', exp: 9999999999 }), engine.address().port)
await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
try {
  const response = await fetch(`http://127.0.0.1:${gateway.address().port}/rest/v1/orders`, { headers: { Authorization: 'Bearer valid' } })
  assert.equal(response.status, 200)
  assert.equal(config.pipelineReadGate, undefined)
  assert.equal(config.postgrestEnv.PGRST_DB_PRE_REQUEST, undefined)
  assert.equal(claims[0].gregale_pipeline_request, undefined)
  // Explicit reconfiguration sees valid hooks, ruling out PIPE-001's missing grants.
  assert.equal(await configurePipelineReadGate(config), true)
  console.log('API configured before pipeline hooks exist remains without pre-request hook or trusted coordination claim after installation, despite valid grants; explicit reconfiguration enables them')
} finally {
  gateway.closeAllConnections(); gateway.close()
  engine.closeAllConnections(); engine.close()
}
