import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {GregaleOperationClient} from '@gregale/sdk-node/operations';
import {finishExport} from './artifact.mjs';

const publicRoot = new URL('./public/', import.meta.url);
const sdkRoot = new URL('./', import.meta.resolve('@gregale/sdk-node/operations'));
const files = new Map([
  ['/', [new URL('index.html', publicRoot), 'text/html']],
  ...['app.mjs', 'customer-auth.mjs', 'progress.mjs'].map(name => [`/${name}`, [new URL(name, publicRoot), 'text/javascript']]),
  ['/style.css', [new URL('style.css', publicRoot), 'text/css']],
  ['/sdk/operations.js', [new URL('operations-browser.js', sdkRoot), 'text/javascript']],
  ...['customer-operations', 'operation-auth', 'operation-feature', 'operation-session', 'operation-submission', 'sse'].map(name => [`/sdk/${name}.js`, [new URL(`${name}.js`, sdkRoot), 'text/javascript']]),
]);

export function validateExportConfig(config) {
  new GregaleOperationClient({apiURL: config.apiURL, credential: () => ''});
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  if (!uuid.test(config.appID ?? '') || (config.definitionID && !uuid.test(config.definitionID)) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(config.scope ?? '')) throw new Error('Set valid public app, environment and definition selectors');
}

// These bounded prefix steps have no external business effects. Real provider
// actions must deduplicate their stable action key and reconcile uncertain writes.
export function exportStep(path, input) {
  if (path === '/collect') {
    if (!Number.isInteger(input?.count) || input.count < 1 || input.count > 100) throw new Error('Invalid count');
    return {rows: Array.from({length: input.count}, (_, index) => ({id: index + 1, value: `item-${index + 1}`}))};
  }
  if (!Array.isArray(input?.rows) || input.rows.length < 1 || input.rows.length > 100 || input.rows.some(row => !Number.isInteger(row?.id) || row.id < 1 || row.id > 100 || typeof row?.value !== 'string' || !/^item-\d+$/i.test(row.value))) throw new Error('Invalid rows');
  if (path === '/transform') return {rows: input.rows.map(row => ({id: row.id, value: row.value.toUpperCase()}))};
  if (path === '/finish') return {rows: input.rows.length, csv: 'id,value\n' + input.rows.map(row => `${row.id},${row.value}\n`).join('')};
  throw new Error('Unknown step');
}

async function readInput(req, scope) {
  const stopRead = () => req.destroy(scope.signal.reason);
  scope.signal.addEventListener('abort', stopRead, {once: true});
  let size = 0; const parts = [];
  try {
    for await (const chunk of req) {
      scope.throwIfStopped(); size += chunk.length;
      if (size > 8192) throw new Error('Sample input too large');
      parts.push(chunk);
    }
  } finally { scope.signal.removeEventListener('abort', stopRead); }
  await scope.checkpoint();
  return JSON.parse(Buffer.concat(parts).toString('utf8'));
}

async function workflowAction(runtime, req, path) {
  return runtime.runCancellableRequest(req.headers, async scope => {
    if (runtime.context()?.step !== path.slice(1)) throw new Error('Workflow action mismatch');
    const result = exportStep(path, await readInput(req, scope));
    scope.throwIfStopped();
    return path === '/finish' ? finishExport(runtime, req.headers, result, scope) : result;
  });
}

export function createExportServer({config, runtime}) {
  validateExportConfig(config);
  // Public selectors are explicitly projected; credentials and workload context
  // must never enter this bootstrap or the browser module graph.
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
      if (req.method === 'POST' && ['/collect', '/transform', '/finish'].includes(path)) {
        if (!runtime) { res.statusCode = 503; res.end('Workload identity is not configured'); return; }
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify(await workflowAction(runtime, req, path))); return;
      }
      res.statusCode = 404; res.end('Not found');
    } catch (error) {
      res.statusCode = error?.name === 'OperationStoppedError' ? 409 : 400;
      res.end(JSON.stringify({error: res.statusCode === 409 ? 'operation stopped' : 'invalid export request'}));
    }
  });
}

// Retain the example's import name while sharing one complete feature server.
export const createWorkflowExportServer = createExportServer;

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = {apiURL: process.env.GREGALE_API_URL, appID: process.env.GREGALE_EXPORT_APP_ID ?? process.env.FAAS_APP_ID,
    scope: process.env.GREGALE_EXPORT_SCOPE, definitionID: process.env.GREGALE_EXPORT_DEFINITION_ID};
  validateExportConfig(config);
  let runtime;
  if (process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT) {
    const {GregaleWorkflowOperations} = await import('@gregale/sdk-node/operations/runtime');
    runtime = new GregaleWorkflowOperations({apiURL: config.apiURL});
  }
  createExportServer({config, runtime}).listen(Number(process.env.PORT ?? 8080), process.env.HOST ?? '127.0.0.1');
}
