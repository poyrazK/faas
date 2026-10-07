import { performance } from 'node:perf_hooks';
import { setTimeout as delay } from 'node:timers/promises';
import express from 'express';
import { toNodeHandler } from '@modelcontextprotocol/node';
import { completable, createMcpHandler, McpServer, ResourceTemplate } from '@modelcontextprotocol/server';
import * as z from 'zod/v4';
import { createAuth } from './auth.js';
import { clientSupportsMcpTasks, installMcpTaskHandlers, MCP_TASKS_EXTENSION_ID, mcpTaskHandlers } from './tasks.js';

const MCP_PROTOCOL_VERSION_META_KEY = 'io.modelcontextprotocol/protocolVersion';
const MCP_CLIENT_CAPABILITIES_META_KEY = 'io.modelcontextprotocol/clientCapabilities';
const MCP_SUBSCRIPTION_ID_META_KEY = 'io.modelcontextprotocol/subscriptionId';
const MCP_SERVER_INFO_META_KEY = 'io.modelcontextprotocol/serverInfo';
const MCP_SERVER_INFO = { name: 'gregale-mcp-server', version: '1.0.0' };
const SUMMARY_STYLES = ['brief', 'technical', 'executive'];
const SAMPLE_RECORD_IDS = ['example-1', 'example-2', 'example-3'];
const MCP_TASK_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const MAX_TASK_SUBSCRIPTION_STREAMS = 128;
const MAX_TASK_RESOURCE_SUBSCRIPTIONS = 64;
const MAX_RESOURCE_URI_BYTES = 16 * 1024;
const TASK_SUBSCRIPTION_KEEPALIVE_MS = 15_000;

function isManagedSubscriptionRequest(request) {
  const notifications = request?.body?.params?.notifications;
  return request?.method === 'POST'
    && request.body?.method === 'subscriptions/listen'
    && request.body?.params?._meta?.[MCP_PROTOCOL_VERSION_META_KEY] === '2026-07-28'
    && notifications && typeof notifications === 'object' && !Array.isArray(notifications)
    && (Object.hasOwn(notifications, 'taskIds') || Object.hasOwn(notifications, 'resourceSubscriptions'));
}

function taskIDFromResourceURI(uri) {
  if (typeof uri !== 'string' || Buffer.byteLength(uri) > MAX_RESOURCE_URI_BYTES) return undefined;
  const match = /^task:\/\/tasks\/([0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$/i.exec(uri);
  return match?.[1].toLowerCase();
}

function writeSseMessage(response, message) {
  if (!response.writableEnded && !response.destroyed) response.write(`event: message\ndata: ${JSON.stringify(message)}\n\n`);
}

function jsonRpcError(response, requestID, code, message, status = 200, data) {
  return response.status(status).json({
    jsonrpc: '2.0', id: requestID,
    error: { code, message, ...(data === undefined ? {} : { data }) },
  });
}

export function createApp(config, { keyResolver, log = console.log, taskRuntime } = {}) {
  if (config.version !== 1 || config.transport !== 'streamable-http' || config.mode !== 'stateless') throw new Error('Unsupported MCP hosting profile');
  if (typeof config.endpoint !== 'string' || !/^\/(?:[A-Za-z0-9._~-]+\/?)*$/.test(config.endpoint) || config.endpoint.split('/').some(s => s === '.' || s === '..')) throw new Error('Invalid MCP endpoint');
  if (!Array.isArray(config.allowed_origins)) throw new Error('Set allowed_origins explicitly');
  const tasksEnabled = config.tasks?.enabled === true;
  if (config.tasks !== undefined && (typeof config.tasks !== 'object' || config.tasks === null || typeof config.tasks.enabled !== 'boolean')) throw new Error('tasks.enabled must be explicitly true or false when task settings are present');
  if (tasksEnabled !== !!taskRuntime) throw new Error('MCP Tasks require an enabled durable task runtime');
  for (const origin of config.allowed_origins) {
    const url = new URL(origin);
    if (!['http:', 'https:'].includes(url.protocol) || url.origin !== origin) throw new Error('Use exact HTTP allowed origins');
  }
  const auth = createAuth(config, keyResolver);
  const readOnly = { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false };

  function observe(name, callback) {
    return async (args, ctx) => {
      const start = performance.now();
      let outcome = 'error';
      try {
        const result = await callback(args, ctx);
        outcome = result.isError ? 'tool_error' : result.resultType === 'task' ? 'task_created' : 'success';
        return result;
      } finally {
        if (ctx.mcpReq.signal.aborted) outcome = 'cancelled';
        // Keep tool arguments, results, access tokens and customer identity out of logs.
        log(JSON.stringify({ event: 'mcp_tool_call', tool: name, outcome, duration_ms: Math.round(performance.now() - start) }));
      }
    };
  }

  function createServer({ authInfo, era }) {
    const server = new McpServer({ name: 'gregale-mcp-server', version: '1.0.0' }, {
      capabilities: {
        tools: { listChanged: true }, resources: { listChanged: true, ...(taskRuntime && era === 'modern' ? { subscribe: true } : {}) }, prompts: { listChanged: true },
        ...(taskRuntime ? { extensions: { [MCP_TASKS_EXTENSION_ID]: {} } } : {}),
      },
    });
    if (taskRuntime) installMcpTaskHandlers(server, taskRuntime, authInfo, config.auth.mode, name => auth.toolPolicy.canAccess(name, authInfo));
    function registerTool(name, definition, callback) {
      const tool = server.registerTool(name, definition, auth.toolPolicy.guard(name, observe(name, callback)));
      if (!auth.toolPolicy.canAccess(name, authInfo)) tool.disable();
    }
    function registerResource(name, uriOrTemplate, definition, callback) {
      const policyKey = typeof uriOrTemplate === 'string' ? uriOrTemplate : uriOrTemplate.uriTemplate.toString();
      if (!auth.resourcePolicy.canAccess(policyKey, authInfo)) return;
      server.registerResource(name, uriOrTemplate, definition, auth.resourcePolicy.guard(policyKey, callback, 'Resource access denied'));
    }
    function registerPrompt(name, definition, callback) {
      if (!auth.promptPolicy.canAccess(name, authInfo)) return;
      server.registerPrompt(name, definition, auth.promptPolicy.guard(name, callback, 'Prompt access denied'));
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
    registerTool('build_report', {
      description: 'Build a harmless sample report; supports durable MCP Tasks when a task store is configured.',
      inputSchema: z.object({ report: z.string().min(1).max(80), steps: z.number().int().min(1).max(20) }),
      annotations: readOnly,
    }, async (args, ctx) => {
      if (taskRuntime && clientSupportsMcpTasks(ctx)) {
        const clientCapabilities = ctx.mcpReq.envelope?.['io.modelcontextprotocol/clientCapabilities'] || {};
        return taskRuntime.create('build_report', args, authInfo, config.auth.mode, clientCapabilities);
      }
      return mcpTaskHandlers.build_report.execute(args, { signal: ctx.mcpReq.signal });
    });
    registerResource('welcome', 'greeting://welcome', {
      title: 'Welcome', description: 'A public example resource.', mimeType: 'text/plain',
    }, async uri => ({ contents: [{ uri: uri.href, mimeType: 'text/plain', text: 'Welcome to the Gregale MCP starter.' }] }));
    registerResource('customer_record', new ResourceTemplate('customer://records/{recordId}', {
      list: undefined,
      complete: {
        recordId: value => SAMPLE_RECORD_IDS.filter(recordID => recordID.startsWith(value.toLowerCase())),
      },
    }), {
      title: 'Customer record', description: 'Example customer-record resource; enforce ownership in your own data lookup.', mimeType: 'application/json',
    }, async (uri, { recordId }) => ({
      contents: [{ uri: uri.href, mimeType: 'application/json', text: JSON.stringify({ recordId, status: 'example' }) }],
    }));
    if (taskRuntime) {
      registerResource('task', new ResourceTemplate('task://tasks/{taskId}', { list: undefined }), {
        title: 'MCP task', description: 'Read the current state and result of a task owned by this caller.', mimeType: 'application/json',
      }, async (uri, { taskId }) => {
        if (!MCP_TASK_ID_PATTERN.test(taskId)) throw new Error('Task not found');
        const task = await taskRuntime.get(taskId, authInfo, config.auth.mode, name => auth.toolPolicy.canAccess(name, authInfo));
        return { contents: [{ uri: uri.href, mimeType: 'application/json', text: JSON.stringify(task) }] };
      });
    }
    registerPrompt('summarize', {
      title: 'Summarize text', description: 'Create a concise summary request.',
      argsSchema: z.object({
        text: z.string().min(1).max(4000),
        style: completable(z.enum(SUMMARY_STYLES), value => SUMMARY_STYLES.filter(style => style.startsWith(value.toLowerCase()))).optional(),
      }),
    }, ({ text, style }) => ({
      description: style ? `Ask for a ${style} summary.` : 'Ask for a concise summary.',
      messages: [{ role: 'user', content: { type: 'text', text: `${style ? `Summarize this text in a ${style} style:` : 'Summarize this text concisely:'}\n\n${text}` } }],
    }));
    return server;
  }

  const handler = createMcpHandler(createServer, { legacy: config.legacy ? 'stateless' : 'reject', responseMode: 'sse', onerror: () => console.error(JSON.stringify({ event: 'mcp_protocol_error' })) });
  const nodeHandler = toNodeHandler(handler);
  const app = express();
  const taskSubscriptionStreams = new Set();

  async function serveTaskSubscription(req, res) {
    const body = req.body;
    const notifications = body.params.notifications;
    const requestID = body.id;
    if (!((typeof requestID === 'string' && requestID.length > 0) || Number.isSafeInteger(requestID))) {
      return jsonRpcError(res, null, -32600, 'Invalid Request', 400);
    }
    if (req.headers['mcp-method'] !== 'subscriptions/listen'
      || req.headers['mcp-protocol-version'] !== body.params._meta[MCP_PROTOCOL_VERSION_META_KEY]
      || !String(req.headers.accept || '').split(',').some(value => value.trim().split(';', 1)[0] === 'text/event-stream')) {
      return nodeHandler(req, res, body);
    }
    const requestedTaskIDs = notifications.taskIds ?? [];
    const requestedResourceURIs = notifications.resourceSubscriptions ?? [];
    if (['toolsListChanged', 'promptsListChanged', 'resourcesListChanged'].some(key => Object.hasOwn(notifications, key) && typeof notifications[key] !== 'boolean')
      || (Object.hasOwn(notifications, 'resourceSubscriptions') && (!Array.isArray(requestedResourceURIs)
        || requestedResourceURIs.length > MAX_TASK_RESOURCE_SUBSCRIPTIONS
        || requestedResourceURIs.some(uri => typeof uri !== 'string' || uri.length === 0 || Buffer.byteLength(uri) > MAX_RESOURCE_URI_BYTES)))
      || (Object.hasOwn(notifications, 'taskIds') && (!Array.isArray(requestedTaskIDs)
        || requestedTaskIDs.length > 64
        || requestedTaskIDs.some(taskID => typeof taskID !== 'string' || !MCP_TASK_ID_PATTERN.test(taskID))))) {
      return jsonRpcError(res, requestID, -32602, 'Invalid params: notifications contains an invalid task or resource subscription filter', 400);
    }

    const uniqueTaskIDs = [...new Set(requestedTaskIDs.map(taskID => taskID.toLowerCase()))];
    const uniqueResourceURIs = [...new Set(requestedResourceURIs)];
    const clientCapabilities = body.params._meta[MCP_CLIENT_CAPABILITIES_META_KEY];
    if (uniqueTaskIDs.length > 0 && !clientSupportsMcpTasks({ mcpReq: { envelope: { [MCP_CLIENT_CAPABILITIES_META_KEY]: clientCapabilities } } })) {
      return jsonRpcError(res, requestID, -32021, 'Missing required client capability', 400, {
        requiredCapabilities: { extensions: { [MCP_TASKS_EXTENSION_ID]: {} } },
      });
    }
    if (taskSubscriptionStreams.size >= MAX_TASK_SUBSCRIPTION_STREAMS) {
      return jsonRpcError(res, requestID, -32603, 'Subscription limit reached');
    }

    const honored = {};
    if (notifications.toolsListChanged === true) honored.toolsListChanged = true;
    if (notifications.promptsListChanged === true) honored.promptsListChanged = true;
    if (notifications.resourcesListChanged === true) honored.resourcesListChanged = true;
    const resourceTaskIDs = new Map();
    for (const uri of uniqueResourceURIs) {
      const taskID = taskIDFromResourceURI(uri);
      if (!taskRuntime || typeof taskRuntime.subscribe !== 'function' || !taskID || !auth.resourcePolicy.canAccess(uri, req.auth)) continue;
      try {
        await taskRuntime.get(taskID, req.auth, config.auth.mode, name => auth.toolPolicy.canAccess(name, req.auth));
        resourceTaskIDs.set(uri, taskID);
      } catch {
        // Acknowledgements must not reveal task IDs outside the caller's access.
      }
    }
    const requestedTaskIDSet = new Set(uniqueTaskIDs);
    const resourceURIsByTaskID = new Map();
    for (const [uri, taskID] of resourceTaskIDs) {
      const uris = resourceURIsByTaskID.get(taskID) || [];
      uris.push(uri);
      resourceURIsByTaskID.set(taskID, uris);
    }
    const allTaskIDs = [...new Set([...uniqueTaskIDs, ...resourceTaskIDs.values()])];
    if (allTaskIDs.length > 64) {
      return jsonRpcError(res, requestID, -32602, 'Invalid params: task and resource subscription filters exceed 64 distinct task IDs', 400);
    }
    let subscription;
    let unsubscribeBus;
    let keepAlive;
    let ended = false;
    let ready = false;
    let closeAfterSetup = false;
    const queued = [];
    const cleanup = endResponse => {
      if (ended) return;
      ended = true;
      clearInterval(keepAlive);
      unsubscribeBus?.();
      subscription?.close();
      taskSubscriptionStreams.delete(closeGracefully);
      if (endResponse && !res.writableEnded && !res.destroyed) res.end();
    };
    const closeNow = () => cleanup(true);
    const closeGracefully = () => {
      if (ended) return;
      const result = { jsonrpc: '2.0', id: requestID, result: {
        resultType: 'complete',
        _meta: {
          [MCP_SUBSCRIPTION_ID_META_KEY]: requestID,
          [MCP_SERVER_INFO_META_KEY]: MCP_SERVER_INFO,
        },
      } };
      if (!ready) {
        queued.push(result);
        closeAfterSetup = true;
        return;
      }
      writeSseMessage(res, result);
      closeNow();
    };
    function deliver(message) {
      if (ended) return;
      if (!ready) { queued.push(message); return; }
      writeSseMessage(res, message);
    }
    function sendTask(task) {
      const params = {
        ...task,
        _meta: { ...task._meta, [MCP_SUBSCRIPTION_ID_META_KEY]: requestID },
      };
      deliver({ jsonrpc: '2.0', method: 'notifications/tasks', params });
    }
    function sendChange(method, params = {}) {
      deliver({ jsonrpc: '2.0', method, params: {
        ...params,
        _meta: { ...params._meta, [MCP_SUBSCRIPTION_ID_META_KEY]: requestID },
      } });
    }
    taskSubscriptionStreams.add(closeGracefully);
    res.once('close', closeNow);
    req.once('aborted', closeNow);

    try {
      unsubscribeBus = handler.bus.subscribe(event => {
        if (event.kind === 'tools_list_changed' && honored.toolsListChanged) sendChange('notifications/tools/list_changed');
        else if (event.kind === 'prompts_list_changed' && honored.promptsListChanged) sendChange('notifications/prompts/list_changed');
        else if (event.kind === 'resources_list_changed' && honored.resourcesListChanged) sendChange('notifications/resources/list_changed');
        else if (event.kind === 'resource_updated' && resourceTaskIDs.has(event.uri)) sendChange('notifications/resources/updated', { uri: event.uri });
      });
      if (allTaskIDs.length > 0 && taskRuntime?.subscribe) {
        subscription = await taskRuntime.subscribe(
          allTaskIDs, req.auth, config.auth.mode,
          name => auth.toolPolicy.canAccess(name, req.auth), task => {
            const taskID = task.taskId.toLowerCase();
            if (requestedTaskIDSet.has(taskID)) sendTask(task);
            for (const uri of resourceURIsByTaskID.get(taskID) || []) handler.notify.resourceUpdated(uri);
          }, closeGracefully,
        );
        const acceptedTaskIDs = new Set(subscription.taskIds.map(taskID => taskID.toLowerCase()));
        for (const [uri, taskID] of resourceTaskIDs) {
          if (!acceptedTaskIDs.has(taskID)) resourceTaskIDs.delete(uri);
        }
        resourceURIsByTaskID.clear();
        for (const [uri, taskID] of resourceTaskIDs) {
          const uris = resourceURIsByTaskID.get(taskID) || [];
          uris.push(uri);
          resourceURIsByTaskID.set(taskID, uris);
        }
        const acceptedResourceURIs = [...resourceTaskIDs.keys()];
        if (acceptedResourceURIs.length > 0) honored.resourceSubscriptions = acceptedResourceURIs;
        if (Object.hasOwn(notifications, 'taskIds')) {
          honored.taskIds = subscription.taskIds.filter(taskID => requestedTaskIDSet.has(taskID.toLowerCase()));
        }
      } else if (uniqueTaskIDs.length > 0) {
        honored.taskIds = [];
      }
      if (ended) { subscription?.close(); return; }

      res.status(200).set({
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache, no-transform',
        Connection: 'keep-alive',
        'X-Accel-Buffering': 'no',
      });
      res.flushHeaders?.();
      writeSseMessage(res, { jsonrpc: '2.0', method: 'notifications/subscriptions/acknowledged', params: {
        notifications: honored,
        _meta: { [MCP_SUBSCRIPTION_ID_META_KEY]: requestID },
      } });
      for (const task of subscription?.tasks || []) {
        if (requestedTaskIDSet.has(task.taskId.toLowerCase())) sendTask(task);
      }
      ready = true;
      for (const message of queued.splice(0)) writeSseMessage(res, message);
      if (closeAfterSetup) closeNow();
      else if (Object.keys(honored).length === 0) closeGracefully();
      else {
        keepAlive = setInterval(() => { if (!ended && !res.destroyed) res.write(': keepalive\n\n'); }, TASK_SUBSCRIPTION_KEEPALIVE_MS);
        keepAlive.unref?.();
      }
    } catch {
      cleanup(res.headersSent);
      if (!res.headersSent) return jsonRpcError(res, requestID, -32603, 'Could not establish task subscription');
    }
  }

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
  app.use(config.endpoint, auth.middleware, express.json({ limit: '1mb' }), auth.authorizeCall);
  app.all(config.endpoint, (req, res, next) => {
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('X-Accel-Buffering', 'no');
    if (isManagedSubscriptionRequest(req)) {
      void serveTaskSubscription(req, res).catch(next);
      return;
    }
    void nodeHandler(req, res, req.body).catch(next);
  });
  app.use((_error, _req, res, _next) => {
    // Error messages can contain submitted values; use a stable, redacted event.
    console.error(JSON.stringify({ event: 'mcp_http_error' }));
    if (!res.headersSent) res.status(500).json({ error: 'internal_error' });
  });
  return {
    app,
    handler,
    closeTaskSubscriptions() {
      for (const close of [...taskSubscriptionStreams]) close();
    },
  };
}
