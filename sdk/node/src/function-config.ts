// src/function-config.ts — typed config declared next to a function's code.
//
// ADR-20261107193000: `gregale deploy` reads `export const config = {...}`
// (or `defineConfig({...})`) from a function's handler file statically; it
// never runs the module. Keep the object a literal: strings, numbers,
// booleans, null, arrays and nested objects. Put values that change between
// deploys in env or secrets. Fields gregale.yaml also sets take its value.

/** An object-storage notification routed to the function. */
export interface FunctionBucketEventConfig {
  bucket: string;
  events: string[];
  id?: string;
  app?: string;
  prefix?: string;
  suffix?: string;
}

/** An inbound webhook endpoint delivering to the function. */
export interface FunctionWebhookConfig {
  name: string;
  provider: 'stripe' | 'generic';
  /** Environment variable holding the signing secret when you deploy. */
  signingSecretEnv: string;
  deliveryPath?: string;
}

/** A queue whose messages are pushed to the function. */
export interface FunctionQueueBindingConfig {
  name: string;
  queueName: string;
  maxConcurrency?: number;
  enabled?: boolean;
}

/** An event subscription delivering to the function. */
export interface FunctionEventTriggerConfig {
  source: string;
  type: string;
  filter?: string;
  ordered?: boolean;
}

/**
 * The settings a function may declare in code: the fields of a
 * `functions:` entry in gregale.yaml except name, root and handler, plus the
 * `cron` and `queue` shorthands.
 */
export interface FunctionConfig {
  /** Cron schedule(s) that call the function on path `/`. */
  cron?: string | string[];
  /** Queue name(s) whose messages are pushed to the function. */
  queue?: string | string[];
  runtime?: 'node22' | 'node24';
  ramMb?: number;
  requestTimeoutS?: number;
  idleTimeoutS?: number;
  minInstances?: number;
  /** Requests one worker serves at once (function_concurrency). */
  concurrency?: number;
  env?: Record<string, string>;
  pruneEnv?: boolean;
  /** Secret names whose values are read from your environment at deploy. */
  secretsFromEnv?: string[];
  /** Request path globs the entry function's address sends here. */
  routes?: string[];
  /** Serve every path no other function's routes claim. */
  entry?: boolean;
  bucketEvents?: FunctionBucketEventConfig[];
  webhooks?: FunctionWebhookConfig[];
  queueBindings?: FunctionQueueBindingConfig[];
  eventTriggers?: FunctionEventTriggerConfig[];
  /** Milliseconds beforeSnapshot may take (default 500, at most 2000). */
  beforeSnapshotTimeoutMs?: number;
  /** Milliseconds afterRestore may take (default 500, at most 2000). */
  afterRestoreTimeoutMs?: number;
  /**
   * JSON Schema of the request body, or a path to a .json file in the
   * function's root; typed clients are generated from it.
   */
  input?: Record<string, unknown> | string;
  /** JSON Schema of the response body, or a path to a .json file. */
  output?: Record<string, unknown> | string;
  /** Other apps of the account, by slug, the typed client may call. */
  calls?: string[];
}

/**
 * Returns its argument unchanged; it exists for type checking and editor
 * completion.
 *
 * ```js
 * import { defineConfig } from '@gregale/sdk-node';
 * export const config = defineConfig({ cron: '0 8 * * *', ramMb: 512 });
 * ```
 */
export function defineConfig<T extends FunctionConfig>(config: T): T {
  return config;
}

/**
 * What a function's snapshot hook receives (ADR-20261110190000).
 * `signal` fires if the call is cancelled.
 */
export interface FunctionHookContext {
  signal: AbortSignal;
  /** The hook being run: 'before_snapshot' or 'after_restore'; absent for init. */
  hook?: 'before_snapshot' | 'after_restore';
}

/**
 * Snapshot hooks a function declares on its default export or as named
 * exports beside `handler`. `gregale deploy` finds them without running the
 * module, so declare them in the exported object literal itself:
 *
 * ```js
 * // handler.js
 * /** @type {import('@gregale/sdk-node').FunctionSnapshotHooks} *\/
 * export default {
 *   async init() { db = await connect(); },
 *   async beforeSnapshot() { await db.end(); },
 *   async afterRestore() { db = await connect(); },
 *   async fetch(request) { return new Response('ok'); },
 * };
 * ```
 */
export interface FunctionSnapshotHooks {
  /** Runs in each worker before it is ready; the snapshot waits for it. */
  init?(ctx: FunctionHookContext): void | Promise<void>;
  /** Runs in every worker before a new snapshot is captured (500 ms). */
  beforeSnapshot?(ctx: FunctionHookContext): void | Promise<void>;
  /** Runs in every worker after a restore, before serving (500 ms). */
  afterRestore?(ctx: FunctionHookContext): void | Promise<void>;
}
