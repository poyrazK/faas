import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {GregaleOperationClient} from '@gregale/sdk-node/operations';
import {UUID} from './contract.mjs';

const publicRoot = new URL('./public/', import.meta.url);
const sdkRoot = new URL('./', import.meta.resolve('@gregale/sdk-node/operations'));
const files = new Map([
  ['/', [new URL('index.html', publicRoot), 'text/html']],
  ['/app.mjs', [new URL('app.mjs', publicRoot), 'text/javascript']],
  ['/customer-auth.mjs', [new URL('customer-auth.mjs', publicRoot), 'text/javascript']],
  ['/style.css', [new URL('style.css', publicRoot), 'text/css']],
  ['/sdk/operations.js', [new URL('operations-browser.js', sdkRoot), 'text/javascript']],
  ...['customer-operations', 'operation-auth', 'operation-feature', 'operation-session', 'operation-submission', 'operation-contract', 'sse'].map(name => [`/sdk/${name}.js`, [new URL(`${name}.js`, sdkRoot), 'text/javascript']]),
]);

export function createExportServer({config}) {
  new GregaleOperationClient({apiURL: config.apiURL, credential: () => ''});
  if (!UUID.test(config.appID ?? '') || (config.definitionID && !UUID.test(config.definitionID)) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(config.scope ?? '')) throw new Error('Configure public export selectors');
  const publicConfig = {apiURL: config.apiURL, appID: config.appID, scope: config.scope, definitionID: config.definitionID};
  return http.createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store'); res.setHeader('X-Content-Type-Options', 'nosniff'); res.setHeader('Referrer-Policy', 'no-referrer');
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' " + new URL(config.apiURL).origin + "; object-src 'none'; base-uri 'none'; frame-ancestors 'none'");
    try {
      const path = new URL(req.url, 'http://localhost').pathname;
      if (req.method === 'GET' && path === '/healthz') { res.end('ok'); return; }
      if (req.method === 'GET' && path === '/config') {
        res.setHeader('Content-Type', 'application/json');
        if (!config.definitionID) { res.statusCode = 503; res.end(JSON.stringify({code: 'export_unavailable'})); return; }
        res.end(JSON.stringify(publicConfig)); return;
      }
      if (req.method === 'GET' && files.has(path)) {
        const [file, type] = files.get(path); res.setHeader('Content-Type', type); res.end(await readFile(file)); return;
      }
      res.statusCode = 404; res.end('Not found');
    } catch { res.statusCode = 503; res.end('Export service unavailable'); }
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = {apiURL: process.env.GREGALE_API_URL, appID: process.env.GREGALE_EXPORT_APP_ID ?? process.env.FAAS_APP_ID,
    scope: process.env.GREGALE_EXPORT_SCOPE, definitionID: process.env.GREGALE_EXPORT_DEFINITION_ID};
  createExportServer({config}).listen(Number(process.env.PORT ?? 8080), process.env.HOST ?? '127.0.0.1');
}
