import http from 'node:http'
import { readFile } from 'node:fs/promises'
const files = new Map([['/', 'index.html'], ['/client.js', 'client.js']])
http.createServer(async (req, res) => {
  const file = files.get(req.url)
  if (!file) { res.writeHead(404); res.end(); return }
  try {
    const body = await readFile(new URL('../browser/' + file, import.meta.url))
    res.writeHead(200, { 'Content-Type': file.endsWith('.js') ? 'text/javascript' : 'text/html', 'Cache-Control': 'no-store' })
    res.end(body)
  } catch { res.writeHead(503); res.end('Run npm run build:browser first.') }
}).listen(3030, '127.0.0.1', () => console.log('Browser example: http://127.0.0.1:3030'))
