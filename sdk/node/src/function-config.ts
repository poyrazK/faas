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
