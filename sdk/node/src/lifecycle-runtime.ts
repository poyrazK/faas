// Gregale lifecycle hooks (ADR-20261106105015 §2): serve the gregale.yaml
// lifecycle.before_checkpoint and lifecycle.after_restore callbacks
// (docs/cold-wake.md) without hand-rolled header, loopback and timeout checks.
//
//   import {LifecycleHooks} from '@gregale/sdk-node/lifecycle/runtime';
//   const hooks = new LifecycleHooks();
//   hooks.onAfterRestore(() => dropIdleClients());
//   app.use(hooks.middleware());               // Express, Connect
//   // or: http.createServer((req, res) => hooks.handle(req, res) || app(req, res));
//
// Secrets keep their own SIGHUP path through SecretReloadCoordinator
// (`@gregale/sdk-node/secrets/runtime`); do not reload them here.

import type {IncomingMessage, ServerResponse} from 'node:http';

export const DEFAULT_BEFORE_CHECKPOINT_PATH = '/internal/before-checkpoint';
export const DEFAULT_AFTER_RESTORE_PATH = '/internal/after-restore';
/** Headroom under the platform's 2 s hook ceiling. */
export const DEFAULT_LIFECYCLE_HOOK_BUDGET_MS = 1500;

export type LifecycleCallback = (signal: AbortSignal) => void | Promise<void>;

export interface LifecycleHooksOptions {
  beforeCheckpointPath?: string;
  afterRestorePath?: string;
  budgetMs?: number;
}

type Kind = 'before_checkpoint' | 'after_restore';

const MARKER: Record<Kind, string> = {
  before_checkpoint: 'x-faas-before-checkpoint',
  after_restore: 'x-faas-after-restore',
};

export class LifecycleHookError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LifecycleHookError';
  }
}

/**
 * Answers only loopback POSTs that carry the platform's marker header, runs
 * the registered callbacks in order under one budget, and shares one run
 * between overlapping deliveries.
 */
export class LifecycleHooks {
  readonly beforeCheckpointPath: string;
  readonly afterRestorePath: string;
  readonly budgetMs: number;
  readonly #callbacks: Record<Kind, LifecycleCallback[]> = {before_checkpoint: [], after_restore: []};
  readonly #inflight: Partial<Record<Kind, Promise<void>>> = {};

  constructor(options: LifecycleHooksOptions = {}) {
    this.beforeCheckpointPath = options.beforeCheckpointPath ?? DEFAULT_BEFORE_CHECKPOINT_PATH;
    this.afterRestorePath = options.afterRestorePath ?? DEFAULT_AFTER_RESTORE_PATH;
    const budget = options.budgetMs ?? DEFAULT_LIFECYCLE_HOOK_BUDGET_MS;
    if (!Number.isFinite(budget) || budget <= 0) throw new LifecycleHookError('budgetMs must be a positive number');
    this.budgetMs = budget;
  }

  /** Work before Gregale captures a new init snapshot; a throw refuses the capture. */
  onBeforeCheckpoint(callback: LifecycleCallback): this {
    this.#callbacks.before_checkpoint.push(callback);
    return this;
  }

  /**
   * Work when an instance restores or resumes frozen: drop stale connections,
   * refresh per-instance values. A throw discards the instance and the request
   * falls back to a fresh boot. Keep it short: it adds to wake latency.
   */
  onAfterRestore(callback: LifecycleCallback): this {
    this.#callbacks.after_restore.push(callback);
    return this;
  }

  /** Connect/Express middleware: answers the hook paths, passes the rest on. */
  middleware(): (req: IncomingMessage, res: ServerResponse, next: (err?: unknown) => void) => void {
    return (req, res, next) => {
      if (!this.handle(req, res)) next();
    };
  }

  /** Answers a hook request and returns true, or returns false for any other request. */
  handle(req: IncomingMessage, res: ServerResponse): boolean {
    const path = (req.url ?? '').split('?', 1)[0];
    const kind: Kind | undefined =
      path === this.beforeCheckpointPath ? 'before_checkpoint' : path === this.afterRestorePath ? 'after_restore' : undefined;
    if (!kind) return false;
    if (req.method !== 'POST') {
      res.writeHead(405, {allow: 'POST', 'content-type': 'text/plain'}).end('method not allowed');
      return true;
    }
    // Gregale strips inbound x-faas-* headers at the public proxy; the loopback
    // check also refuses a caller inside the app's network.
    if (req.headers[MARKER[kind]] !== '1' || !isLoopback(req.socket.remoteAddress)) {
      res.writeHead(403, {'content-type': 'text/plain'}).end('forbidden');
      return true;
    }
    req.resume();
    this.run(kind).then(
      () => { res.writeHead(204).end(); },
      () => { res.writeHead(500, {'content-type': 'text/plain'}).end('lifecycle hook failed'); },
    );
    return true;
  }

  /** Runs one kind's callbacks, sharing an in-flight run. Exposed for tests and custom servers. */
  run(kind: Kind): Promise<void> {
    const current = this.#inflight[kind];
    if (current) return current;
    const callbacks = [...this.#callbacks[kind]];
    const run = runCallbacks(callbacks, this.budgetMs).finally(() => { delete this.#inflight[kind]; });
    this.#inflight[kind] = run;
    return run;
  }
}

async function runCallbacks(callbacks: LifecycleCallback[], budgetMs: number): Promise<void> {
  const controller = new AbortController();
  let timer: NodeJS.Timeout | undefined;
  const expired = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      controller.abort();
      reject(new LifecycleHookError('lifecycle callbacks exceeded their budget'));
    }, budgetMs);
    timer.unref?.();
  });
  expired.catch(() => {});
  try {
    for (const callback of callbacks) {
      await Promise.race([Promise.resolve().then(() => callback(controller.signal)), expired]);
    }
  } finally {
    clearTimeout(timer);
  }
}

function isLoopback(address: string | undefined): boolean {
  if (!address) return false;
  const value = address.startsWith('::ffff:') ? address.slice(7) : address;
  return value === '::1' || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(value);
}
