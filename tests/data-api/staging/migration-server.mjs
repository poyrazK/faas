import { createServer } from 'node:http'

// Migration credentials must be confined to the release task, even when the
// same revision is subsequently serving HTTP.
if (process.env.MIGRATION_DATABASE_URL) throw new Error('migration_credential_leaked_to_runtime')
createServer((req, res) => {
  res.writeHead(req.url === '/healthz' ? 200 : 404, { 'Content-Type': 'application/json' })
  res.end('{"migration_credential_present":false}')
}).listen(Number(process.env.PORT || 8080), '0.0.0.0')
