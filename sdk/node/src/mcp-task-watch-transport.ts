import { randomUUID } from 'node:crypto';
import { parseFrame } from './sse.js';

const listenMethod = 'subscriptions/listen';
const watchIdKey = 'io.gregale/local-task-watch';
const capabilitiesKey = 'io.modelcontextprotocol/clientCapabilities';
const subscriptionIdKey = 'io.modelcontextprotocol/subscriptionId';
const tasksExtension = 'io.modelcontextprotocol/tasks';

type RecordValue = Record<string, unknown>;
type Registration = { requested: ReadonlySet<string>; honored?: ReadonlySet<string>; notify(taskId: string): void };
type Registrations = Map<string, Registration>;
const transports = new WeakMap<object, Registrations>();

function record(value: unknown): RecordValue | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as RecordValue : undefined;
}

function observeEvent(data: string, requestId: unknown, registration: Registration): void {
  let message: RecordValue | undefined;
  try { message = record(JSON.parse(data)); } catch { return; }
  const params = record(message?.params);
  if (message?.jsonrpc !== '2.0' || !params || record(params._meta)?.[subscriptionIdKey] !== requestId) return;
  if (message.method === 'notifications/subscriptions/acknowledged' && !registration.honored) {
    const ids = record(params.notifications)?.taskIds;
    registration.honored = new Set(Array.isArray(ids) ? ids.filter(
      (id): id is string => typeof id === 'string' && registration.requested.has(id),
    ) : []);
  }
  if (message.method === 'notifications/tasks' && typeof params.taskId === 'string' && registration.honored?.has(params.taskId)) {
    registration.notify(params.taskId);
  }
}

function observeStream(response: Response, id: unknown, registration: Registration): Response {
  if (!response.ok || !response.body ||
      response.headers.get('content-type')?.split(';', 1)[0]?.trim().toLowerCase() !== 'text/event-stream') return response;
  const decoder = new TextDecoder();
  let pending = '';
  const body = response.body.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
    transform(bytes, output) {
      pending += decoder.decode(bytes, { stream: true });
      if (pending.length > (8 << 20)) throw new Error('MCP Task subscription frame exceeds the size limit');
      let frame: ReturnType<typeof parseFrame>;
      while ((frame = parseFrame(pending)) !== null) {
        pending = pending.slice(frame.consumed);
        if (frame.event.event === 'message' && frame.event.data) observeEvent(frame.event.data, id, registration);
      }
      output.enqueue(bytes);
    },
  }));
  Object.defineProperty(response, 'body', { value: body });
  return response;
}

export function taskWatchTransport(endpoint: string, fetcher: typeof globalThis.fetch): {
  fetch: typeof globalThis.fetch;
  bind(transport: object): void;
} {
  const watches: Registrations = new Map();
  return {
    bind(transport) { transports.set(transport, watches); },
    async fetch(input, init) {
      const request = input instanceof Request ? input : undefined;
      const headers = new Headers(init?.headers ?? request?.headers);
      if (headers.get('mcp-method') !== listenMethod || (init?.method ?? request?.method)?.toUpperCase() !== 'POST') {
        return fetcher(input, init);
      }
      const body = init?.body ?? await request?.clone().text();
      let message: RecordValue | undefined;
      try { message = typeof body === 'string' ? record(JSON.parse(body)) : undefined; }
      catch { return fetcher(input, init); }
      const params = record(message?.params);
      const filter = record(params?.notifications);
      if (!filter || !Object.hasOwn(filter, watchIdKey)) return fetcher(input, init);
      const watchId = filter[watchIdKey];
      const registration = typeof watchId === 'string' ? watches.get(watchId) : undefined;
      const meta = record(params?._meta);
      if (!registration || !message || message.method !== listenMethod || !meta ||
          (typeof message.id !== 'number' && typeof message.id !== 'string')) {
        throw new TypeError('Invalid MCP Task watch request');
      }
      if (new URL(input instanceof Request ? input.url : input).href !== endpoint) {
        throw new TypeError('MCP Task watch endpoint changed');
      }
      const capabilities = record(meta[capabilitiesKey]);
      meta[capabilitiesKey] = {
        ...capabilities, extensions: { ...record(capabilities?.extensions), [tasksExtension]: {} },
      };
      delete filter[watchIdKey];
      filter.taskIds = [...registration.requested];
      headers.delete('content-length');
      const response = await fetcher(input, { ...init, headers, redirect: 'manual', body: JSON.stringify(message) });
      if ((response.status >= 300 && response.status < 400) || (response.url && new URL(response.url).href !== endpoint)) {
        void response.body?.cancel().catch(() => undefined);
        throw new TypeError('MCP Task watch endpoint changed');
      }
      return observeStream(response, message.id, registration);
    },
  };
}

export type TaskListenFilter = {
  resourceSubscriptions?: string[];
  [watchIdKey]: string;
};

export function registerTaskWatch(
  transport: object | undefined, taskIds: readonly string[], notify: (taskId: string) => void,
) {
  const watches = transport && transports.get(transport);
  if (!watches) return undefined;
  const id = randomUUID();
  const registration: Registration = { requested: new Set(taskIds), notify };
  watches.set(id, registration);
  return {
    filter: { [watchIdKey]: id } satisfies TaskListenFilter,
    get honored() { return registration.honored; },
    close() { watches.delete(id); },
  };
}
