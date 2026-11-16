const http = require('node:http');
const html = '<!doctype html><html><head><title>Gregale zone probe</title></head><body>zone-byte-probe-v1</body></html>\n';
http.createServer((req, res) => {
  const url = new URL(req.url, 'http://probe.invalid');
  res.setHeader('cache-control', 'no-store');
  res.setHeader('x-zone-probe', 'v1');
  if (url.pathname === '/html') {
    res.writeHead(200, {'content-type': 'text/html; charset=utf-8', 'content-security-policy': "default-src 'none'"});
    return res.end(html);
  }
  const code = url.pathname === '/status' ? Number(url.searchParams.get('code')) : 200;
  if (![200, 500, 502, 503, 504].includes(code)) { res.writeHead(400); return res.end('invalid code'); }
  res.writeHead(code, {'content-type': 'application/json'});
  res.end(JSON.stringify({probe: 'zone-byte-probe-v1', status: code}) + '\n');
}).listen(Number(process.env.PORT || 3000), '0.0.0.0');
