import { readFileSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
import { setTimeout as delay } from 'node:timers/promises';
import express from 'express';
import { toNodeHandler } from '@modelcontextprotocol/node';
import { createMcpHandler, McpServer } from '@modelcontextprotocol/server';
import * as z from 'zod/v4';
import { createAuth } from './auth.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
if (config.version !== 1 || config.transport !== 'streamable-http' || config.mode !== 'stateless') throw new Error('Unsupported MCP hosting profile');
if (typeof config.endpoint !== 'string' || !/^\/(?:[A-Za-z0-9._~-]+\/?)*$/.test(config.endpoint) || config.endpoint.split('/').some(s => s === '.' || s === '..')) throw new Error('Invalid MCP endpoint');
if (!Array.isArray(config.allowed_origins)) throw new Error('Set allowed_origins explicitly');
for (const origin of config.allowed_origins) {
  const url = new URL(origin);
  if (!['http:', 'https:'].includes(url.protocol) || url.origin !== origin) throw new Error('Use exact HTTP allowed origins');
}
const auth = createAuth(config);
const readOnly = { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false };

function observe(name, callback) {
  return async (args, ctx) => {
    const start = performance.now();
    let outcome = 'error';
    try {
      const result = await callback(args, ctx);
      outcome = result.isError ? 'tool_error' : 'success';
      return result;
    } finally {
      if (ctx.mcpReq.signal.aborted) outcome = 'cancelled';
      // Keep tool arguments, results, access tokens and customer identity out of logs.
      console.log(JSON.stringify({ event: 'mcp_tool_call', tool: name, outcome, duration_ms: Math.round(performance.now() - start) }));
    }
  };
}

function createServer() {
  const server = new McpServer({ name: 'gregale-mcp-server', version: '1.0.0' });
  server.registerTool('greet', {
    description: 'Return a greeting from Gregale.', inputSchema: z.object({ name: z.string().min(1).max(100) }), annotations: readOnly,
  }, observe('greet', async ({ name }) => ({ content: [{ type: 'text', text: `Hello, ${name}!` }] })));
  server.registerTool('add', {
    description: 'Add two finite numbers.', inputSchema: z.object({ a: z.number().finite(), b: z.number().finite() }),
    outputSchema: z.object({ sum: z.number().finite() }), annotations: readOnly,
  }, observe('add', async ({ a, b }) => {
    const sum = a + b;
    if (!Number.isFinite(sum)) return { isError: true, content: [{ type: 'text', text: 'Sum exceeds the finite number range.' }] };
    return { content: [{ type: 'text', text: String(sum) }], structuredContent: { sum } };
  }));
  server.registerTool('stream_demo', {
    description: 'Send three progress events over one second.', inputSchema: z.object({}), annotations: readOnly,
  }, observe('stream_demo', async (_args, ctx) => {
    const progressToken = ctx.mcpReq._meta?.progressToken;
    for (let step = 1; step <= 3; step++) {
      if (progressToken !== undefined) await ctx.mcpReq.notify({ method: 'notifications/progress', params: { progressToken, progress: step, total: 3 } });
      await delay(400, undefined, { signal: ctx.mcpReq.signal });
    }
    return { content: [{ type: 'text', text: 'Streaming complete.' }] };
  }));
  return server;
}

const handler = createMcpHandler(createServer, { legacy: config.legacy ? 'stateless' : 'reject', responseMode: 'sse', onerror: () => console.error(JSON.stringify({ event: 'mcp_protocol_error' })) });
const nodeHandler = toNodeHandler(handler);
const app = express();
app.disable('x-powered-by');
app.use((req, res, next) => {
  if (req.headers.origin !== undefined && !config.allowed_origins.includes(req.headers.origin)) return res.status(403).json({ error: 'origin_not_allowed' });
  if (req.headers.origin) {
    res.setHeader('Access-Control-Allow-Origin', req.headers.origin);
    res.setHeader('Vary', 'Origin');
    res.setHeader('Access-Control-Expose-Headers', 'WWW-Authenticate, MCP-Protocol-Version, Streaming-Status');
    if (req.method === 'OPTIONS') {
      const headers = (req.headers['access-control-request-headers'] || '').split(',').map(h => h.trim().toLowerCase()).filter(Boolean);
      const standard = ['authorization', 'content-type', 'accept', 'mcp-protocol-version', 'mcp-method', 'mcp-name'];
      if (headers.some(h => !standard.includes(h) && !/^mcp-param-[!#$%&'*+.^_`|~0-9a-z-]+$/.test(h))) return res.status(400).json({ error: 'header_not_allowed' });
      res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
      res.setHeader('Access-Control-Allow-Headers', headers.join(', '));
      return res.status(204).end();
    }
  }
  next();
});
app.get('/healthz', (_req, res) => res.json({ status: 'ok' }));
if (auth.metadata) app.get(`/.well-known/oauth-protected-resource${config.endpoint}`, (_req, res) => res.json(auth.metadata));
app.use(config.endpoint, auth.middleware, express.json({ limit: '1mb' }));
app.all(config.endpoint, (req, res, next) => {
  res.setHeader('Cache-Control', 'no-store');
  res.setHeader('X-Accel-Buffering', 'no');
  void nodeHandler(req, res, req.body).catch(next);
});
app.use((_error, _req, res, _next) => {
  // Error messages can contain submitted values; use a stable, redacted event.
  console.error(JSON.stringify({ event: 'mcp_http_error' }));
  if (!res.headersSent) res.status(500).json({ error: 'internal_error' });
});
const appID = process.env.FAAS_APP_ID || '';
const host = process.env.MCP_BIND_ADDRESS || (appID ? '0.0.0.0' : '127.0.0.1');
const listener = app.listen(Number(process.env.PORT || 8080), host, () => console.log(JSON.stringify({ event: 'mcp_listening', port: listener.address().port })));
process.on('SIGTERM', () => { listener.close(); void handler.close(); });
