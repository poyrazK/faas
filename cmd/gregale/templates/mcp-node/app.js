import { setTimeout as delay } from 'node:timers/promises';
import express from 'express';
import { toNodeHandler } from '@modelcontextprotocol/node';
import { createMcpHandler, McpServer } from '@modelcontextprotocol/server';
import * as z from 'zod/v4';
import { createAuth } from './auth.js';
import { createEvents, requestIDHeader } from './events.js';

export function createApp(config, { keyResolver, log = console.log } = {}) {
  if (config.version !== 1 || config.transport !== 'streamable-http' || config.mode !== 'stateless') throw new Error('Unsupported MCP hosting profile');
  if (typeof config.endpoint !== 'string' || !/^\/(?:[A-Za-z0-9._~-]+\/?)*$/.test(config.endpoint) || config.endpoint.split('/').some(s => s === '.' || s === '..')) throw new Error('Invalid MCP endpoint');
  if (!Array.isArray(config.allowed_origins)) throw new Error('Set allowed_origins explicitly');
  for (const origin of config.allowed_origins) {
    const url = new URL(origin);
    if (!['http:', 'https:'].includes(url.protocol) || url.origin !== origin) throw new Error('Use exact HTTP allowed origins');
  }
  const events = createEvents(log);
  const auth = createAuth(config, keyResolver, events.mark);
  const readOnly = { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false };

  const registrations = new Map();
  function registerTool(name, definition, callback) {
    events.register(name);
    registrations.set(name, { definition, callback });
  }
  registerTool('greet', {
    description: 'Return a greeting from Gregale.', inputSchema: z.object({ name: z.string().min(1).max(100) }), annotations: readOnly,
  }, async ({ name }) => ({ content: [{ type: 'text', text: `Hello, ${name}!` }] }));
  registerTool('add', {
    description: 'Add two finite numbers.', inputSchema: z.object({ a: z.number().finite(), b: z.number().finite() }),
    outputSchema: z.object({ sum: z.number().finite() }), annotations: readOnly,
  }, async ({ a, b }) => {
    const sum = a + b;
    if (!Number.isFinite(sum)) return { isError: true, content: [{ type: 'text', text: 'Sum exceeds the finite number range.' }] };
    return { content: [{ type: 'text', text: String(sum) }], structuredContent: { sum } };
  });
  registerTool('stream_demo', {
    description: 'Send three progress events over one second.', inputSchema: z.object({}), annotations: readOnly,
  }, async (_args, ctx) => {
    const progressToken = ctx.mcpReq._meta?.progressToken;
    for (let step = 1; step <= 3; step++) {
      if (progressToken !== undefined) await ctx.mcpReq.notify({ method: 'notifications/progress', params: { progressToken, progress: step, total: 3 } });
      await delay(400, undefined, { signal: ctx.mcpReq.signal });
    }
    return { content: [{ type: 'text', text: 'Streaming complete.' }] };
  });
  function createServer({ authInfo }) {
    const server = new McpServer({ name: 'gregale-mcp-server', version: '1.0.0' });
    for (const [name, { definition, callback }] of registrations) {
      const observed = { ...definition, inputSchema: events.schema(definition.inputSchema, 'input'),
        outputSchema: events.schema(definition.outputSchema, 'output') };
      const tool = server.registerTool(name, observed, auth.toolPolicy.guard(name, events.observe(name, callback, undefined, !!definition.outputSchema), () => events.mark('denied', 'tool_access_denied')));
      if (!auth.toolPolicy.canAccess(name, authInfo)) tool.disable();
    }
    return server;
  }

  const handler = createMcpHandler(createServer, { legacy: config.legacy ? 'stateless' : 'reject', responseMode: 'sse', onerror: () => events.mark('protocol_error', 'transport_error') });
  const nodeHandler = toNodeHandler(handler);
  const app = express();
  app.disable('x-powered-by');
  app.use(config.endpoint, events.middleware);
  app.use((req, res, next) => {
    if (req.headers.origin !== undefined && !config.allowed_origins.includes(req.headers.origin)) {
      events.mark('denied', 'origin_not_allowed');
      return res.status(403).json({ error: 'origin_not_allowed' });
    }
    if (req.headers.origin) {
      res.setHeader('Access-Control-Allow-Origin', req.headers.origin);
      res.setHeader('Vary', 'Origin');
      res.setHeader('Access-Control-Expose-Headers', `WWW-Authenticate, MCP-Protocol-Version, Streaming-Status, ${requestIDHeader}`);
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
  app.use(config.endpoint, auth.middleware, express.json({ limit: '1mb' }), events.describe, auth.authorizeCall);
  app.all(config.endpoint, (req, res, next) => {
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('X-Accel-Buffering', 'no');
    void nodeHandler(req, res, req.body).catch(next);
  });
  app.use((error, _req, res, _next) => {
    const invalid = error.type === 'entity.parse.failed' || error.type === 'entity.too.large';
    events.mark(invalid ? 'validation_error' : 'error', invalid ? 'invalid_body' : 'internal_error');
    if (!res.headersSent) res.status(invalid ? error.status : 500).json({ error: invalid ? 'invalid_body' : 'internal_error' });
    else res.destroy();
  });
  return { app, handler };
}
