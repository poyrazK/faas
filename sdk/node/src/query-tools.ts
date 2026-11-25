/** Typed, bounded application reads. Handlers must use read-only, authorization-scoped data access. */
import { createHmac, timingSafeEqual } from "node:crypto";
import {
  JobContract,
  JobError,
  jobInputId,
  jobScope,
  jobSnapshot,
  jobValueHash,
} from "./job-runtime.js";
import { pipelineScope, type PipelineScope } from "./pipeline-state.js";

export type QueryCode =
  | "invalid_input"
  | "invalid_cursor"
  | "invalid_output"
  | "unauthorized"
  | "deadline"
  | "cancelled"
  | "limit"
  | "query_failed"
  | "not_found";
export class QueryToolError extends Error {
  constructor(readonly code: QueryCode) {
    super("SDK query stopped: " + code);
    this.name = "QueryToolError";
  }
}
export interface QueryActor {
  readonly subject_id: string;
  readonly owner_id: string;
}
export interface QueryOptions {
  readonly timeoutMs: number;
  readonly signal?: AbortSignal;
}
export interface QueryPageOptions extends QueryOptions {
  readonly after?: string;
  readonly pageSize?: number;
}
export interface QueryIterationOptions extends QueryPageOptions {
  readonly maxPages?: number;
  readonly maxItems?: number;
}
export interface QueryPage<O> {
  readonly items: readonly O[];
  readonly next_cursor: string | null;
}
export interface QueryLoadPage<O> {
  readonly items: readonly O[];
  readonly after: string | null;
}
export interface QueryLoadContext {
  readonly actor: QueryActor;
  readonly after: string | null;
  readonly limit: number;
  readonly signal: AbortSignal;
}
export interface QueryAuthorizedLoadContext extends QueryLoadContext {
  remainingMs(): number;
}
export interface QueryToolDefinition<I, O, A> {
  readonly name: string;
  readonly description: string;
  readonly revision: string;
  readonly scope: PipelineScope;
  /** Backend-only shared key, 32–128 printable ASCII characters. Rotation invalidates outstanding cursors. */
  readonly cursorKey: string;
  readonly maxPageSize?: number;
  readonly maxBytes?: number;
  readonly allowEmptyPages?: boolean;
  readonly authorize: (
    input: I,
    access: A,
    signal: AbortSignal,
  ) => Promise<QueryActor | null>;
  readonly load: (
    input: I,
    context: QueryLoadContext,
  ) => Promise<QueryLoadPage<O>>;
}
export interface AuthorizedQueryToolDefinition<I, O, A>
  extends Omit<QueryToolDefinition<I, O, A>, "load"> {
  readonly load: (
    input: I,
    context: QueryAuthorizedLoadContext,
    access: A,
  ) => Promise<QueryLoadPage<O>>;
}
export interface QueryTool<I, O, A> {
  readonly name: string;
  readonly description: string;
  readonly contract: JobContract<I, O>;
  readonly maxPageSize: number;
  page(input: I, access: A, options: QueryPageOptions): Promise<QueryPage<O>>;
  pages(
    input: I,
    access: A,
    options: QueryIterationOptions,
  ): AsyncIterable<QueryPage<O>>;
  items(input: I, access: A, options: QueryIterationOptions): AsyncIterable<O>;
}
const bytes = (v: unknown) => Buffer.byteLength(JSON.stringify(v));
const validName = (v: unknown): v is string =>
  typeof v === "string" && /^[a-z][a-z0-9_]{0,47}$/.test(v);
const bound = (n: unknown, max: number) =>
  Number.isSafeInteger(n) && Number(n) > 0 && Number(n) <= max;
const position = (v: unknown): v is string =>
  typeof v === "string" &&
  v.length > 0 &&
  Buffer.byteLength(v) <= 2048 &&
  Buffer.from(v).toString() === v;
function actor(value: QueryActor | null): QueryActor {
  if (
    !value ||
    Object.keys(value).sort().join() !== "owner_id,subject_id" ||
    !jobInputId(value.subject_id) ||
    !jobInputId(value.owner_id)
  )
    throw new QueryToolError("unauthorized");
  return jobSnapshot(value);
}
/** @internal Shared deadline/error boundary, including callbacks that ignore cancellation. */
export async function queryScope<T>(
  settings: QueryOptions,
  work: (signal: AbortSignal) => Promise<T>,
): Promise<T> {
  if (!settings || !bound(settings.timeoutMs, 2147483647))
    throw new QueryToolError("invalid_input");
  try {
    return await jobScope(settings.timeoutMs, settings.signal, work);
  } catch (e) {
    if (settings.signal?.aborted) throw new QueryToolError("cancelled");
    if (e instanceof QueryToolError) throw e;
    if (
      e instanceof JobError &&
      ["invalid_input", "invalid_output", "deadline", "limit"].includes(e.code)
    )
      throw new QueryToolError(e.code as QueryCode);
    throw new QueryToolError("query_failed");
  }
}
function identity(
  name: string,
  description: string,
  revision: string,
  scope: PipelineScope,
) {
  if (
    !validName(name) ||
    typeof description !== "string" ||
    !description ||
    Buffer.byteLength(description) > 2048 ||
    !jobInputId(revision)
  )
    throw new QueryToolError("invalid_input");
  try {
    return pipelineScope(scope);
  } catch {
    throw new QueryToolError("invalid_input");
  }
}
export function defineQueryTool<I, O, A>(
  contract: JobContract<I, O>,
  definition: QueryToolDefinition<I, O, A>,
): QueryTool<I, O, A> {
  const d = { ...definition };
  if (typeof d.load !== "function") throw new QueryToolError("invalid_input");
  return defineAuthorizedQueryTool(contract, {
    ...d,
    load(input, { actor, after, limit, signal }) {
      return d.load(input, { actor, after, limit, signal });
    },
  });
}

export function defineAuthorizedQueryTool<I, O, A>(
  contract: JobContract<I, O>,
  definition: AuthorizedQueryToolDefinition<I, O, A>,
): QueryTool<I, O, A> {
  const d = { ...definition },
    scope = identity(d.name, d.description, d.revision, d.scope),
    captured = new JobContract<I, O>(contract.spec),
    maxPageSize = d.maxPageSize ?? 100,
    maxBytes = d.maxBytes ?? 262144;
  if (
    typeof d.cursorKey !== "string" ||
    !/^[\x21-\x7e]{32,128}$/.test(d.cursorKey) ||
    !bound(maxPageSize, 100) ||
    !bound(maxBytes, 1048576) ||
    typeof d.authorize !== "function" ||
    typeof d.load !== "function"
  )
    throw new QueryToolError("invalid_input");
  function inputValue(input: I): I {
    try {
      const value = captured.input(input);
      if (bytes(value) > 8192) throw Error();
      return value;
    } catch {
      throw new QueryToolError("invalid_input");
    }
  }
  const sign = (payload: string) =>
    createHmac("sha256", d.cursorKey).update(payload).digest("base64url");
  function decode(cursor: string | undefined, binding: string): string | null {
    if (cursor === undefined) return null;
    try {
      if (
        typeof cursor !== "string" ||
        cursor.length > 8192 ||
        !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}$/.test(cursor)
      )
        throw Error();
      const [payload, signature] = cursor.split(".") as [string, string];
      if (!timingSafeEqual(Buffer.from(signature), Buffer.from(sign(payload))))
        throw Error();
      const raw = Buffer.from(payload, "base64url");
      if (raw.toString("base64url") !== payload) throw Error();
      const p = JSON.parse(
        new TextDecoder("utf-8", { fatal: true }).decode(raw),
      );
      if (
        Object.keys(p).sort().join() !== "a,b,v" ||
        p.v !== 1 ||
        p.b !== binding ||
        !position(p.a)
      )
        throw Error();
      return p.a;
    } catch {
      throw new QueryToolError("invalid_cursor");
    }
  }
  const api: QueryTool<I, O, A> = {
    name: d.name,
    description: d.description,
    contract: captured,
    maxPageSize,
    async page(input, access, settings) {
      // Snapshot synchronously so a caller cannot change a queued read's input/options.
      const options = { ...settings },
        value = inputValue(input);
      const end = performance.now() + options.timeoutMs;
      return queryScope(options, async (signal) => {
        const size = options.pageSize ?? Math.min(20, maxPageSize);
        if (bytes(value) > 8192 || !bound(size, maxPageSize))
          throw new QueryToolError("invalid_input");
        const first = actor(
          await d.authorize(jobSnapshot(value), access, signal),
        );
        signal.throwIfAborted();
        const binding = jobValueHash([
          "gregale.query.v1",
          scope,
          d.name,
          d.revision,
          captured.sha256,
          value,
          first,
        ]);
        const after = decode(options.after, binding);
        const context: QueryAuthorizedLoadContext = {
          actor: first,
          after,
          limit: size,
          signal,
          remainingMs() {
            signal.throwIfAborted();
            const remaining = end - performance.now();
            if (remaining <= 0) throw new QueryToolError("deadline");
            return Math.max(1, Math.ceil(remaining));
          },
        };
        const raw = await d.load(jobSnapshot(value), context, access);
        signal.throwIfAborted();
        if (
          !raw ||
          Object.keys(raw).sort().join() !== "after,items" ||
          !Array.isArray(raw.items) ||
          raw.items.length > size ||
          (raw.after !== null &&
            (!position(raw.after) ||
              raw.after === after ||
              (!d.allowEmptyPages && !raw.items.length)))
        )
          throw new QueryToolError("invalid_output");
        const items = raw.items.map((item) => captured.output(item));
        const payload =
          raw.after === null
            ? null
            : Buffer.from(
                JSON.stringify({ v: 1, b: binding, a: raw.after }),
              ).toString("base64url");
        const result = jobSnapshot({
          items,
          next_cursor: payload === null ? null : payload + "." + sign(payload),
        });
        if (bytes(result) > maxBytes) throw new QueryToolError("limit");
        const current = actor(
          await d.authorize(jobSnapshot(value), access, signal),
        );
        signal.throwIfAborted();
        if (jobValueHash(first) !== jobValueHash(current))
          throw new QueryToolError("unauthorized");
        return result;
      });
    },
    async *pages(input, access, settings) {
      const options = { ...settings },
        value = inputValue(input),
        maxPages = options.maxPages ?? 10,
        maxItems = options.maxItems ?? 1000;
      if (
        !bound(maxPages, 1000) ||
        !bound(maxItems, 100000) ||
        !bound(options.timeoutMs, 2147483647) ||
        !bound(options.pageSize ?? Math.min(20, maxPageSize), maxPageSize)
      )
        throw new QueryToolError("invalid_input");
      const end = performance.now() + options.timeoutMs,
        seen = new Set<string>();
      let after = options.after,
        total = 0;
      for (let n = 0; n < maxPages && total < maxItems; n++) {
        if (options.signal?.aborted) throw new QueryToolError("cancelled");
        if (performance.now() >= end) throw new QueryToolError("deadline");
        const page = await api.page(value, access, {
          ...options,
          after,
          timeoutMs: Math.max(1, Math.ceil(end - performance.now())),
          pageSize: Math.min(
            options.pageSize ?? Math.min(20, maxPageSize),
            maxItems - total,
          ),
        });
        if (page.next_cursor && seen.has(page.next_cursor))
          throw new QueryToolError("invalid_output");
        if (page.next_cursor) seen.add(page.next_cursor);
        total += page.items.length;
        yield page;
        if (!page.next_cursor) return;
        after = page.next_cursor;
      }
    },
    async *items(input, access, settings) {
      const options = { ...settings },
        end = performance.now() + options.timeoutMs;
      for await (const page of api.pages(input, access, options))
        for (const item of page.items) {
          if (options.signal?.aborted) throw new QueryToolError("cancelled");
          if (performance.now() >= end) throw new QueryToolError("deadline");
          yield item;
        }
    },
  };
  return Object.freeze(api);
}

export interface QueryResourceLink {
  readonly type: "resource_link";
  readonly uri: string;
  readonly name: string;
  readonly description: string;
  readonly mimeType: string;
}
export interface QueryResourceDefinition<A> {
  readonly name: string;
  readonly description: string;
  readonly revision: string;
  readonly scope: PipelineScope;
  readonly mimeType: string;
  readonly maxBytes?: number;
  readonly authorize: (
    id: string,
    access: A,
    signal: AbortSignal,
  ) => Promise<QueryActor | null>;
  readonly read: (
    id: string,
    actor: QueryActor,
    signal: AbortSignal,
  ) => Promise<string>;
}
export interface QueryResource<A> {
  readonly name: string;
  readonly template: Readonly<{
    uriTemplate: string;
    name: string;
    description: string;
    mimeType: string;
  }>;
  link(
    id: string,
    access: A,
    options: QueryOptions,
  ): Promise<QueryResourceLink>;
  read(
    uri: string,
    access: A,
    options: QueryOptions,
  ): Promise<Readonly<{ uri: string; mimeType: string; text: string }>>;
}
export function defineQueryResource<A>(
  definition: QueryResourceDefinition<A>,
): QueryResource<A> {
  const d = { ...definition },
    scope = identity(d.name, d.description, d.revision, d.scope),
    maxBytes = d.maxBytes ?? 262144;
  if (
    typeof d.mimeType !== "string" ||
    !/^[a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+$/.test(d.mimeType) ||
    !bound(maxBytes, 1048576) ||
    typeof d.authorize !== "function" ||
    typeof d.read !== "function"
  )
    throw new QueryToolError("invalid_input");
  const prefix =
    "gregale-query://" +
    jobValueHash(["gregale.query-resource.v1", scope, d.name, d.revision]) +
    "/" +
    d.name +
    "/";
  const validId = (id: unknown): id is string =>
    typeof id === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(id);
  return Object.freeze({
    name: d.name,
    template: jobSnapshot({
      uriTemplate: prefix + "{id}",
      name: d.name,
      description: d.description,
      mimeType: d.mimeType,
    }),
    link(id, access, options) {
      return queryScope(options, async (signal) => {
        if (!validId(id)) throw new QueryToolError("invalid_input");
        actor(await d.authorize(id, access, signal));
        signal.throwIfAborted();
        return jobSnapshot({
          type: "resource_link" as const,
          uri: prefix + id,
          name: d.name + "/" + id,
          description: d.description,
          mimeType: d.mimeType,
        });
      });
    },
    read(uri, access, options) {
      return queryScope(options, async (signal) => {
        if (
          typeof uri !== "string" ||
          !uri.startsWith(prefix) ||
          !validId(uri.slice(prefix.length))
        )
          throw new QueryToolError("not_found");
        const id = uri.slice(prefix.length),
          first = actor(await d.authorize(id, access, signal));
        signal.throwIfAborted();
        const text = await d.read(id, first, signal);
        signal.throwIfAborted();
        if (typeof text !== "string" || Buffer.from(text).toString() !== text)
          throw new QueryToolError("invalid_output");
        if (Buffer.byteLength(text) > maxBytes)
          throw new QueryToolError("limit");
        const current = actor(await d.authorize(id, access, signal));
        signal.throwIfAborted();
        if (jobValueHash(first) !== jobValueHash(current))
          throw new QueryToolError("unauthorized");
        return jobSnapshot({ uri, mimeType: d.mimeType, text });
      });
    },
  } satisfies QueryResource<A>);
}
