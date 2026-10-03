import { AsyncLocalStorage } from 'node:async_hooks';
import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';

const methods = new Set(['initialize', 'notifications/initialized', 'notifications/cancelled', 'ping', 'tools/list', 'tools/call']);
const protocols = new Set(['2026-07-28', '2025-11-25']);
export const requestIDHeader = 'X-MCP-Request-ID';

// Application diagnostics, not a durable audit ledger. Never retain submitted
// IDs, headers, JWT claims, arguments, results or validation/error messages.
export function createEvents(log) {
  const storage = new AsyncLocalStorage();
  const tools = new Set();
  function register(name) {
    if (!/^[A-Za-z0-9_.-]{1,64}$/.test(name)) throw new Error('Use bounded ASCII tool names for MCP events');
    tools.add(name);
  }
  function emit(state, event, fields) {
    if (!state) return;
    try {
      log(JSON.stringify({ event_version: 1, event, request_id: state.id, protocol: state.protocol,
        rpc_method: state.method, ...fields }));
    } catch { /* Logging must not change execution or authorization. */ }
  }
  function mark(outcome, reason, state = storage.getStore()) {
    if (state && state.outcome !== 'cancelled') Object.assign(state, { outcome, reason });
  }
  function middleware(req, res, next) {
    const start = performance.now();
    const state = { id: randomUUID(), protocol: protocols.has(req.headers['mcp-protocol-version']) ? req.headers['mcp-protocol-version'] : 'unknown', method: 'unknown' };
    res.setHeader(requestIDHeader, state.id);
    let emitted = false;
    function finish(cancelled) {
      if (emitted) return;
      emitted = true;
      const status = res.headersSent ? res.statusCode : 0;
      const outcome = cancelled ? 'cancelled' : state.outcome ?? (status >= 500 ? 'error' : status >= 400 ? 'protocol_error' : 'success');
      emit(state, 'mcp_request', { ...(state.tool ? { tool: state.tool } : {}), outcome,
        ...(cancelled ? {} : state.reason ? { reason: state.reason } : {}), http_status: status,
        duration_ms: Math.round(performance.now() - start) });
    }
    res.once('finish', () => finish(false));
    res.once('close', () => finish(!res.writableFinished));
    storage.run(state, next);
  }
  function describe(req, _res, next) {
    const state = storage.getStore();
    if (state) {
      state.method = methods.has(req.body?.method) ? req.body.method : 'unknown';
      if (req.method === 'POST' && state.method === 'unknown') mark('protocol_error', 'unsupported_call', state);
      if (state.method === 'tools/call') {
        state.tool = tools.has(req.body.params?.name) ? req.body.params.name : 'unknown';
        if (state.tool === 'unknown') mark('protocol_error', 'unsupported_call', state);
      }
    }
    next();
  }
  function schema(schema, phase, state = storage.getStore()) {
    if (!schema) return schema;
    const standard = schema['~standard'];
    // Use the public Standard Schema contract, preserving JSON Schema discovery.
    return { '~standard': { ...standard, validate: async value => {
      try {
        const result = await standard.validate(value);
        if (result.issues?.length) mark('validation_error', `${phase}_validation`, state);
        return result;
      } catch (error) {
        mark('validation_error', `${phase}_validation`, state);
        throw error;
      }
    } } };
  }
  function observe(name, callback, state = storage.getStore(), hasOutputSchema = false) {
    return async (args, ctx) => {
      const start = performance.now();
      let outcome = 'error';
      try {
        const result = await callback(args, ctx);
        outcome = result.isError ? 'tool_error' : 'success';
        if (hasOutputSchema && !result.isError && result.structuredContent === undefined) mark('validation_error', 'output_validation', state);
        return result;
      } finally {
        if (ctx.mcpReq.signal.aborted) outcome = 'cancelled';
        if (outcome !== 'success') mark(outcome, undefined, state);
        emit(state, 'mcp_tool_call', { tool: name, outcome, duration_ms: Math.round(performance.now() - start) });
      }
    };
  }
  return { middleware, describe, mark, schema, observe, register };
}
