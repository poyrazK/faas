import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {generateExport} from './export.mjs';

const publicRoot = new URL('./public/', import.meta.url);
const sdkRoot = new URL('../../sdk/node/dist/', import.meta.url);
const files = new Map([
  ['/', [new URL('index.html', publicRoot), 'text/html']],
  ['/app.mjs', [new URL('app.mjs', publicRoot), 'text/javascript']],
  ['/session.mjs', [new URL('session.mjs', publicRoot), 'text/javascript']],
  ['/style.css', [new URL('style.css', publicRoot), 'text/css']],
  ['/sdk/operations.js', [new URL('operations.js', sdkRoot), 'text/javascript']],
  ['/sdk/sse.js', [new URL('sse.js', sdkRoot), 'text/javascript']],
]);

export function createExportServer({config, runtime}) {
  return http.createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store'); res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader('Referrer-Policy', 'no-referrer');
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' " + new URL(config.apiURL).origin + "; object-src 'none'; base-uri 'none'; frame-ancestors 'none'");
    try {
      const path = new URL(req.url, 'http://localhost').pathname;
      if (req.method === 'GET' && path === '/healthz') { res.end('ok'); return; }
      if (req.method === 'GET' && path === '/config') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(config)); return; }
      if (req.method === 'GET' && files.has(path)) {
        const [file, type] = files.get(path); res.setHeader('Content-Type', type); res.end(await readFile(file)); return;
      }
      if (req.method === 'POST' && path === '/exports') {
        if (!runtime) { res.statusCode = 503; res.end('Workload identity is not configured'); return; }
        let body = ''; for await (const chunk of req) { body += chunk; if (Buffer.byteLength(body) > 1024) throw new Error('Input exceeds sample bound'); }
        const output = await generateExport(runtime, req.headers, JSON.parse(body));
        res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(output)); return;
      }
      res.statusCode = 404; res.end('Not found');
    } catch { res.statusCode = 500; res.end('Export request failed'); }
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = {apiURL: process.env.GREGALE_API_URL, appID: process.env.GREGALE_EXPORT_APP_ID, scope: process.env.GREGALE_EXPORT_SCOPE, definitionID: process.env.GREGALE_EXPORT_DEFINITION_ID};
  const {GregaleOperationClient} = await import(new URL('operations.js', sdkRoot));
  new GregaleOperationClient({apiURL: config.apiURL, credential: () => ''}); // validate API origin before serving it
  if (![config.appID, config.definitionID].every(value => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value ?? '')) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(config.scope ?? '')) throw new Error('Set public export app, environment and definition selectors');
  let runtime;
  if (process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT) {
    const {GregaleOperations} = await import(new URL('operations-runtime.js', sdkRoot));
    runtime = new GregaleOperations({apiURL: config.apiURL});
  }
  // Local UI development stays on loopback. Gregale guest hosting must opt into
  // HOST=0.0.0.0 and its trusted guest listener; never expose it directly.
  createExportServer({config, runtime}).listen(Number(process.env.PORT ?? 8080), process.env.HOST ?? '127.0.0.1');
}
