import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {GregaleOperationClient} from '@gregale/sdk-node/operations';
import {generateExport} from './export.mjs';

const publicRoot = new URL('./public/', import.meta.url);
const sdkRoot = new URL('./', import.meta.resolve('@gregale/sdk-node/operations'));
const files = new Map([
  ['/', [new URL('index.html', publicRoot), 'text/html']],
  ['/app.mjs', [new URL('app.mjs', publicRoot), 'text/javascript']],
  ['/customer-auth.mjs', [new URL('customer-auth.mjs', publicRoot), 'text/javascript']],
  ['/style.css', [new URL('style.css', publicRoot), 'text/css']],
  ['/sdk/operations.js', [new URL('operations-browser.js', sdkRoot), 'text/javascript']],
  ['/sdk/operation-feature.js', [new URL('operation-feature.js', sdkRoot), 'text/javascript']],
  ['/sdk/operation-auth.js', [new URL('operation-auth.js', sdkRoot), 'text/javascript']],
  ['/sdk/customer-operations.js', [new URL('customer-operations.js', sdkRoot), 'text/javascript']],
  ['/sdk/operation-session.js', [new URL('operation-session.js', sdkRoot), 'text/javascript']],
  ['/sdk/operation-submission.js', [new URL('operation-submission.js', sdkRoot), 'text/javascript']],
  ['/sdk/sse.js', [new URL('sse.js', sdkRoot), 'text/javascript']],
]);

export function validateExportConfig(config) {
  new GregaleOperationClient({apiURL: config.apiURL, credential: () => ''});
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  if (!uuid.test(config.appID ?? '') || (config.definitionID && !uuid.test(config.definitionID)) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(config.scope ?? '')) throw new Error('Set valid public app, environment and definition selectors');
}

export function createExportServer({config, runtime}) {
  validateExportConfig(config);
  // The config projection is an allowlist; never expose credentials or an
  // application's full configuration through this public bootstrap endpoint.
  const publicConfig = {apiURL: config.apiURL, appID: config.appID, scope: config.scope, definitionID: config.definitionID};
  return http.createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store'); res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader('Referrer-Policy', 'no-referrer');
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
      if (req.method === 'POST' && path === '/exports') {
        if (!runtime) { res.statusCode = 503; res.end('Workload identity is not configured'); return; }
        const chunks = []; let size = 0;
        for await (const chunk of req) { size += chunk.length; if (size > 1024) { res.statusCode = 413; res.end('Export input is too large'); return; } chunks.push(chunk); }
        const output = await generateExport(runtime, req.headers, JSON.parse(Buffer.concat(chunks).toString('utf8')));
        res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(output)); return;
      }
      res.statusCode = 404; res.end('Not found');
    } catch { res.statusCode = 500; res.end('Export request failed'); }
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = {apiURL: process.env.GREGALE_API_URL, appID: process.env.GREGALE_EXPORT_APP_ID ?? process.env.FAAS_APP_ID, scope: process.env.GREGALE_EXPORT_SCOPE, definitionID: process.env.GREGALE_EXPORT_DEFINITION_ID};
  validateExportConfig(config);
  let runtime;
  if (process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT) {
    const {GregaleOperations} = await import('@gregale/sdk-node/operations/runtime');
    runtime = new GregaleOperations({apiURL: config.apiURL});
  }
  createExportServer({config, runtime}).listen(Number(process.env.PORT ?? 8080), process.env.HOST ?? '127.0.0.1');
}
