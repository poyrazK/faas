// Plain Node app with no tracing code (ADR-958 preload acceptance). Each
// /checkout request calls the app's own /healthz over HTTP, so the platform
// preload must produce an HTTP client span, and logs one pino error line,
// which the runtime adapter must export with the request's trace (ADR-213).
'use strict';
const http = require('node:http');
const logger = require('pino')();

const port = Number(process.env.PORT || 8080);

http.createServer((req, res) => {
  if (req.url === '/healthz') {
    res.writeHead(200);
    res.end();
    return;
  }
  http.get({ host: '127.0.0.1', port, path: '/healthz' }, (dep) => {
    dep.resume();
    dep.on('end', () => {
      logger.error('charge failed for buyer@example.com: card declined');
      res.writeHead(200, { 'content-type': 'text/plain; charset=utf-8' });
      // FAAS_TRACING_ENABLED proves guest-init stamped the preload env.
      res.end(`preloaded ${process.env.FAAS_TRACING_ENABLED || ''}\n`);
    });
  }).on('error', (err) => {
    res.writeHead(502);
    res.end(String(err));
  });
}).listen(port, '0.0.0.0');
