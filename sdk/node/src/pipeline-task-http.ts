/** Streamable HTTP transport for a pipeline Task client. */
import {
  PipelineTaskClientError,
  type PipelineTaskClientMethod,
  type PipelineTaskClientOptions,
  type PipelineTaskTransport,
} from "./pipeline-task-client.js";
import { streamSse } from "./sse.js";

const protocol = "2026-07-28";
const subscription = "subscriptions/listen";
const id = 1;
const maxFrameBytes = 1024 * 1024 + 4096;
type Method = PipelineTaskClientMethod | typeof subscription;

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new PipelineTaskClientError("invalid_response");
  return value as Record<string, unknown>;
}

function parse(value: string): Record<string, unknown> {
  let message: unknown;
  try {
    message = JSON.parse(value);
  } catch {
    throw new PipelineTaskClientError("invalid_response");
  }
  const frame = object(message);
  if (frame.jsonrpc !== "2.0")
    throw new PipelineTaskClientError("invalid_response");
  return frame;
}

function result(frame: Record<string, unknown>, method: Method): unknown {
  if (
    frame.id !== id ||
    Object.hasOwn(frame, "result") === Object.hasOwn(frame, "error")
  )
    throw new PipelineTaskClientError("invalid_response");
  if (!Object.hasOwn(frame, "error")) return frame.result;
  const { code } = object(frame.error);
  if (!Number.isSafeInteger(code))
    throw new PipelineTaskClientError("invalid_response");
  if (code === -32601 || code === -32021)
    throw new PipelineTaskClientError("unsupported");
  if (
    code === -32002 ||
    (code === -32602 && (method === "tasks/get" || method === "resources/read"))
  )
    throw new PipelineTaskClientError("not_found");
  if (code === -32602) throw new PipelineTaskClientError("invalid_input");
  throw new PipelineTaskClientError("request_failed");
}

async function jsonBody(
  response: Response,
  signal?: AbortSignal,
): Promise<Record<string, unknown>> {
  const reader = response.body?.getReader();
  if (!reader) throw new PipelineTaskClientError("invalid_response");
  const stop = () => {
    void reader.cancel().catch(() => {});
  };
  signal?.addEventListener("abort", stop, { once: true });
  try {
    const chunks: Uint8Array[] = [];
    let size = 0;
    for (;;) {
      signal?.throwIfAborted();
      const { value, done } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxFrameBytes)
        throw new PipelineTaskClientError("invalid_response");
      chunks.push(value);
    }
    let text: string;
    try {
      text = new TextDecoder("utf-8", { fatal: true }).decode(
        Buffer.concat(chunks),
      );
    } catch {
      throw new PipelineTaskClientError("invalid_response");
    }
    return parse(text);
  } finally {
    signal?.removeEventListener("abort", stop);
    reader.releaseLock();
  }
}

export function createPipelineTaskHttpTransport(options: {
  readonly endpoint: string;
  readonly fetch?: typeof fetch;
  readonly supportsForms?: boolean;
}): PipelineTaskTransport {
  let endpoint: URL;
  try {
    endpoint = new URL(options.endpoint);
  } catch {
    throw new PipelineTaskClientError("invalid_input");
  }
  if (
    !["http:", "https:"].includes(endpoint.protocol) ||
    endpoint.username ||
    endpoint.password ||
    endpoint.hash
  )
    throw new PipelineTaskClientError("invalid_input");
  const fetchRequest = options.fetch ?? globalThis.fetch;
  const capabilities = {
    extensions: { "io.modelcontextprotocol/tasks": {} },
    ...(options.supportsForms !== false && { elicitation: { form: {} } }),
  };

  async function* frames(
    method: Method,
    params: Readonly<Record<string, unknown>>,
    call: PipelineTaskClientOptions,
  ) {
    let response: Response | undefined;
    try {
      const name = params.name ?? params.taskId ?? params.uri;
      response = await fetchRequest(endpoint.href, {
        method: "POST",
        redirect: "error",
        signal: call.signal,
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json, text/event-stream",
          "MCP-Protocol-Version": protocol,
          "MCP-Method": method,
          ...(typeof name === "string" && { "MCP-Name": name }),
        },
        body: JSON.stringify({
          jsonrpc: "2.0",
          id,
          method,
          params: {
            ...params,
            _meta: {
              "io.modelcontextprotocol/protocolVersion": protocol,
              "io.modelcontextprotocol/clientInfo": {
                name: "gregale-sdk",
                version: "1",
              },
              "io.modelcontextprotocol/clientCapabilities": capabilities,
            },
          },
        }),
      });
      if (response.status === 401 || response.status === 403)
        throw new PipelineTaskClientError("unauthorized");
      if (response.status === 404 && method !== subscription)
        throw new PipelineTaskClientError("not_found");
      const type = response.headers
        .get("content-type")
        ?.split(";", 1)[0]
        ?.trim()
        .toLowerCase();
      if (!response.ok && type !== "application/json")
        throw new PipelineTaskClientError("request_failed");
      if (type === "application/json") {
        const frame = await jsonBody(response, call.signal);
        if (!response.ok && !Object.hasOwn(frame, "error"))
          throw new PipelineTaskClientError("request_failed");
        yield frame;
      } else if (type === "text/event-stream") {
        for await (const frame of streamSse(
          response,
          call.signal,
          maxFrameBytes,
        )) {
          if (frame.data) yield parse(frame.data);
        }
      } else {
        throw new PipelineTaskClientError("invalid_response");
      }
    } catch (error) {
      if (error instanceof PipelineTaskClientError) throw error;
      throw new PipelineTaskClientError(
        error instanceof RangeError ? "invalid_response" : "request_failed",
      );
    } finally {
      // The SDK owns this response, including a stream stopped after one snapshot.
      void response?.body?.cancel().catch(() => {});
    }
  }

  return Object.freeze<PipelineTaskTransport>({
    async request(method, params, call) {
      for await (const frame of frames(method, params, call))
        return result(frame, method);
      throw new PipelineTaskClientError("request_failed");
    },
    async *subscribe(taskId, call) {
      let accepted = false;
      for await (const frame of frames(
        subscription,
        { notifications: { taskIds: [taskId] } },
        call,
      )) {
        if (Object.hasOwn(frame, "id")) {
          const terminal = object(result(frame, subscription));
          if (terminal.resultType !== "complete")
            throw new PipelineTaskClientError("invalid_response");
          return;
        }
        const params = object(frame.params);
        if (
          object(params._meta)["io.modelcontextprotocol/subscriptionId"] !== id
        )
          throw new PipelineTaskClientError("invalid_response");
        if (frame.method === "notifications/subscriptions/acknowledged") {
          const ids = object(params.notifications).taskIds;
          if (!Array.isArray(ids) || ids.length === 0)
            throw new PipelineTaskClientError("not_found");
          if (accepted || ids.length !== 1 || ids[0] !== taskId)
            throw new PipelineTaskClientError("invalid_response");
          accepted = true;
        } else if (frame.method === "notifications/tasks") {
          if (!accepted || params.taskId !== taskId)
            throw new PipelineTaskClientError("invalid_response");
          yield params;
        }
      }
    },
  });
}
