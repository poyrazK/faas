const reviewMethod = 'gregale/tasks/input-review';
const maxReviewResponseBytes = 8 << 20;
export const maxReviewJSONDepth = 64;
const numberLiteral = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?(?![\s\S])/u;
type JSONWithSource = {
  parse(source: string, reviver: (this: object, key: string, value: unknown, context?: { source?: string }) => unknown): unknown;
  rawJSON?(source: string): unknown;
};
const native = JSON as JSONWithSource;

export class McpTaskReviewNumber {
  constructor(readonly source: string) {
    if (typeof source !== 'string' || source.length > maxReviewResponseBytes || !numberLiteral.test(source)) {
      throw new TypeError('Expected an exact JSON number literal');
    }
    Object.freeze(this);
  }

  toString(): string { return this.source; }

  toJSON(): unknown {
    if (!native.rawJSON) throw new TypeError('Exact MCP Task answers require native JSON.rawJSON');
    return native.rawJSON(this.source);
  }
}

export function isTaskReviewObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) return false;
  const prototype: unknown = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

async function readReviewBody(response: Response): Promise<string> {
  const reader = response.body?.getReader();
  if (!reader) throw new TypeError('Missing MCP Task review response');
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxReviewResponseBytes) throw new TypeError('MCP Task review response exceeds the size limit');
      chunks.push(value);
    }
    return new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks, size));
  } finally {
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

// Capture the original tokens at the JSON boundary, before the MCP codec
// projects the result. Only form contents need exact numbering. In particular,
// a JSON-RPC ID must remain a native number for SDK response correlation.
function parseReviewBody(source: string): unknown {
  const numericTokens = new WeakMap<object, Map<string, string>>();
  const message = native.parse(source, function (key, value, context) {
    if (typeof value === 'number' && context?.source !== undefined) {
      const tokens = numericTokens.get(this) ?? new Map<string, string>();
      tokens.set(key, context.source);
      numericTokens.set(this, tokens);
    }
    return value;
  });

  function restore(value: unknown, depth: number): void {
    if (!isTaskReviewObject(value) && !Array.isArray(value)) return;
    if (depth > maxReviewJSONDepth) throw new TypeError('MCP Task review exceeds the JSON nesting limit');
    const object = value as Record<string, unknown>;
    const tokens = numericTokens.get(value);
    for (const key of Object.keys(object)) {
      const token = tokens?.get(key);
      if (token !== undefined) object[key] = new McpTaskReviewNumber(token);
      else restore(object[key], depth + 1);
    }
  }
  if (isTaskReviewObject(message) && isTaskReviewObject(message.result)) restore(message.result.inputRequests, 0);
  return message;
}

/** Pass to the official Streamable HTTP transport's fetch option for exact form numbers. */
export function createMcpTaskReviewFetch(fetcher: typeof globalThis.fetch = globalThis.fetch): typeof globalThis.fetch {
  return async (input, init) => {
    const request = input instanceof Request ? input : undefined;
    const method = (init?.method ?? request?.method)?.toUpperCase();
    const headers = new Headers(init?.headers ?? request?.headers);
    const response = await fetcher(input, init);
    if (method === 'POST' && headers.get('mcp-method') === reviewMethod && response.ok &&
        response.headers.get('content-type')?.split(';', 1)[0]?.trim().toLowerCase() === 'application/json') {
      Object.defineProperty(response, 'json', { value: async () => parseReviewBody(await readReviewBody(response)) });
    }
    return response;
  };
}
