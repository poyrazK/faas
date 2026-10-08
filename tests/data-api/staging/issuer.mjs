import { createServer } from 'node:http'
import { readFileSync } from 'node:fs'

// Only the public key is deployed. The canary controller retains the signing
// key in memory and never uploads it or writes it to its evidence.
const jwks = readFileSync(new URL('./jwks.json', import.meta.url))
createServer((req, res) => {
  if (req.url !== '/jwks.json' && req.url !== '/healthz') {
    res.writeHead(404).end(); return
  }
  res.setHeader('Content-Type', 'application/json')
  res.end(req.url === '/jwks.json' ? jwks : '{"ready":true}')
}).listen(Number(process.env.PORT || 8080), '0.0.0.0')
