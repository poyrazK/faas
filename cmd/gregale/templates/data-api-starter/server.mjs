import http from 'node:http'

// The serving process never connects to PostgreSQL or runs migrations.
http.createServer((req, res) => {
  res.writeHead(req.url === '/healthz' ? 200 : 404, { 'Content-Type': 'application/json' })
  res.end(JSON.stringify(req.url === '/healthz' ? { ready: true } : { error: 'not_found' }))
}).listen(Number(process.env.PORT || 8080), '0.0.0.0')
