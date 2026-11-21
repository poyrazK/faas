import { AsyncLocalStorage } from 'node:async_hooks';
import { performance } from 'node:perf_hooks';

export const protocolContext = new AsyncLocalStorage();
const methods = new Set(['initialize', 'server/discover', 'ping', 'tools/list', 'tools/call', 'resources/list', 'resources/templates/list', 'resources/read', 'prompts/list', 'prompts/get', 'completion/complete', 'tasks/get', 'tasks/cancel', 'tasks/update', 'subscriptions/listen']);
const tools = new Set(['greet', 'add', 'stream_demo', 'build_report']);
export const toolLabel = name => tools.has(name) ? name : 'other';

export function observeProtocolRequest(req, res, log) {
  const message = req.body;
  if (!message || typeof message !== 'object' || Array.isArray(message) || typeof message.method !== 'string' || !Object.hasOwn(message, 'id')) return null;
  const start = performance.now();
  let executionEnd, streamStart, outcome, termination, finished = false;
  const requestID = req.headers['x-faas-request-id'];
  const trace = typeof req.headers.traceparent === 'string' ? /^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$/.exec(req.headers.traceparent) : null;
  const correlation = {};
  if (typeof requestID === 'string' && /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(requestID)) correlation.request_id = requestID;
  if (trace && trace[1] !== '0'.repeat(32) && trace[2] !== '0'.repeat(16)) correlation.trace_id = trace[1];
  const method = methods.has(message.method) ? message.method : 'other';
  const tool = message.method === 'tools/call' ? toolLabel(message.params?.name) : 'none';
  const observation = {
    response(value) {
      if (value?.method === 'notifications/subscriptions/acknowledged') { executionEnd ??= performance.now(); return; }
      if (!value || value.id !== message.id || (!Object.hasOwn(value, 'error') && !Object.hasOwn(value, 'result')) || outcome) return;
      executionEnd ??= performance.now();
      outcome = Object.hasOwn(value, 'error') ? 'rpc_error' : value.result?.isError === true ? 'tool_error' : value.result?.resultType === 'task' ? 'task_created' : 'success';
    },
    failed(reason) { termination = reason; },
    finish(streamOutcome = termination || 'complete') {
      if (finished) return;
      finished = true;
      const event = { event: 'mcp_protocol_request', method, tool, outcome: outcome || (streamOutcome !== 'complete' ? streamOutcome : 'unobserved'), stream_outcome: streamOutcome, ...correlation };
      if (executionEnd !== undefined) event.execution_duration_ms = Math.max(0, executionEnd - start);
      if (streamStart !== undefined) event.stream_duration_ms = Math.max(0, performance.now() - streamStart);
      try { log(JSON.stringify(event)); } catch { /* Telemetry cannot fail a request. */ }
    },
  };
  const writeHead = res.writeHead;
  res.writeHead = function (...args) {
    const supplied = args[typeof args[1] === 'string' ? 2 : 1];
    let contentType = res.getHeader('Content-Type');
    if (Array.isArray(supplied)) {
      for (let i=0; i<supplied.length; i+=2) if (String(supplied[i]).toLowerCase() === 'content-type') contentType = supplied[i+1];
    } else if (supplied && typeof supplied === 'object') {
      for (const [name,value] of Object.entries(supplied)) if (name.toLowerCase() === 'content-type') contentType = value;
    }
    if (String(contentType || '').startsWith('text/event-stream')) streamStart ??= performance.now();
    return writeHead.apply(this, args);
  };
  res.once('finish', () => observation.finish());
  res.once('close', () => { if (!res.writableFinished) observation.finish('cancelled'); });
  res.once('error', error => observation.finish(error?.code === 'ETIMEDOUT' || error?.name === 'TimeoutError' ? 'timeout' : 'cancelled'));
  return observation;
}

// Observe SDK protocol objects at its public transport boundary, never response bytes.
export function observeServerTransport(server, observation) {
  if (!observation) return;
  const connect = server.connect.bind(server);
  server.connect = async transport => {
    const send = transport.send.bind(transport);
    transport.send = async (message, ...options) => {
      observation.response(message);
      try { return await send(message, ...options); }
      catch (error) {
        observation.failed(error?.name === 'TimeoutError' || error?.code === 'ETIMEDOUT' ? 'timeout' : ['ECONNRESET','EPIPE'].includes(error?.code) ? 'cancelled' : 'internal_error');
        throw error;
      }
    };
    return connect(transport);
  };
}


export function startProtocolHeartbeat(log = console.log) {
  const emit = () => { try { log(JSON.stringify({ event: 'mcp_telemetry_heartbeat', version: 1, loss_alert: process.env.MCP_TELEMETRY_LOSS_ALERT === 'true' })); } catch { /* Best effort. */ } };
  emit();
  const timer = setInterval(emit, 30_000);
  timer.unref();
  return () => clearInterval(timer);
}
