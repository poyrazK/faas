// Pull-lease queue consumer for worker and job apps (ADR-1140).
//
// A worker leases messages from a queue with an enabled pull binding, runs a
// handler for each one, and settles it: ack on success, nack (retry under the
// binding's policy) on a throw. While a handler runs, its lease is extended
// at half the visibility timeout so slow work is not redelivered. Inside a
// Gregale VM the consumer authenticates with a workload-identity token for
// audience gregale:queues; elsewhere it takes an API key with queues:consume.
// A qualification candidate has neither: Gregale sets GREGALE_QUEUE_LEASE_URL
// to its node-local proxy, which serves only the reviewed smoke message and
// needs no credential (ADR-20261022155812). That base takes precedence.

export const DEFAULT_GREGALE_API_URL = 'https://api.gregale.dev';
const QUEUES_AUDIENCE = 'gregale:queues';
const MAX_BATCH = 100;
const MAX_WAIT_SECONDS = 20;
const ERROR_MAX_BYTES = 1024;
const TOKEN_REFRESH_MARGIN_MS = 60_000;
const MAX_BACKOFF_MS = 30_000;
// A call may not outlive its server-side wait by more than this.
const CALL_SLACK_MS = 10_000;

export interface QueueMessage<T = unknown> {
  readonly id: string;
  readonly payload: T;
  readonly headers: Readonly<Record<string, string>>;
  /** 1 on first delivery; redeliveries count up. */
  readonly attempt: number;
  readonly enqueuedAt: Date;
}

export interface QueueHandlerContext {
  /** Aborted when the lease is lost and the message may be redelivered elsewhere. */
  readonly signal: AbortSignal;
}

export type QueueHandler<T = unknown> = (message: QueueMessage<T>, context: QueueHandlerContext) => unknown;

export interface QueueConsumerOptions {
  /** App slug that owns the pull binding. */
  app: string;
  /** Queue name of the pull binding. */
  queue: string;
  /** apid base URL. Defaults to GREGALE_API_URL, then https://api.gregale.dev. HTTPS required. GREGALE_QUEUE_LEASE_URL, set by Gregale inside a qualification candidate, overrides it and the credentials. */
  apiURL?: string;
  /** API key with queues:consume. Without one the consumer uses workload identity. */
  apiKey?: string;
  /** Loopback token URL supplied by guest-init. Defaults to FAAS_WORKLOAD_IDENTITY_ENDPOINT. */
  identityEndpoint?: string;
  /** Messages handled at once (1-100, default 1). The binding's max_concurrency still caps the app. */
  concurrency?: number;
  /** Seconds a leased message stays hidden (default 30, at least 2); extended while the handler runs. */
  visibilityTimeoutSeconds?: number;
  /** Seconds a lease call waits for a message (0-20, default 20). */
  waitSeconds?: number;
  /** Extra delay before a failed message is retried, on top of the binding's back-off. */
  retryDelaySeconds?: number;
  /** Receives lease, settle and handler errors; the consumer keeps running. */
  onError?: (error: unknown) => void;
  fetch?: typeof globalThis.fetch;
}

interface LeasedMessage {
  id: string;
  receipt: string;
  payload: unknown;
  headers?: Record<string, string>;
  attempt: number;
  enqueued_at: string;
}

interface SettleResponse {
  succeeded: string[];
  failed: { receipt: string; reason: string }[];
}

/** Leases and handles queue messages until stopped. */
export class GregaleQueueConsumer<T = unknown> {
  private readonly leasesURL: URL;
  private readonly identityURL?: URL;
  private readonly platformLease: boolean;
  private readonly fetchImpl: typeof globalThis.fetch;
  private readonly concurrency: number;
  private readonly visibility: number;
  private readonly wait: number;
  private readonly retryDelay: number;
  private readonly stopping = new AbortController();
  private readonly inFlight = new Set<Promise<void>>();
  private running?: Promise<void>;
  private token?: { value: string; refreshAt: number };

  constructor(private readonly options: QueueConsumerOptions, private readonly handler: QueueHandler<T>) {
    if (!options.app || !options.queue) throw new Error('Queue consumer requires app and queue');
    const leasesPath = `/v1/apps/${encodeURIComponent(options.app)}/queues/${encodeURIComponent(options.queue)}/leases`;
    const platform = process.env.GREGALE_QUEUE_LEASE_URL;
    this.platformLease = Boolean(platform);
    if (platform) {
      const base = platformLeaseBase(platform);
      this.leasesURL = new URL(base.pathname.replace(/\/+$/, '') + leasesPath, base);
    } else {
      const base = new URL(options.apiURL ?? process.env.GREGALE_API_URL ?? DEFAULT_GREGALE_API_URL);
      if (base.protocol !== 'https:') throw new Error('Queue consumer requires an HTTPS API URL');
      this.leasesURL = new URL(leasesPath, base);
    }
    this.concurrency = options.concurrency ?? 1;
    this.visibility = options.visibilityTimeoutSeconds ?? 30;
    this.wait = options.waitSeconds ?? MAX_WAIT_SECONDS;
    this.retryDelay = options.retryDelaySeconds ?? 0;
    if (!(Number.isInteger(this.concurrency) && this.concurrency >= 1 && this.concurrency <= MAX_BATCH)) throw new Error('concurrency must be 1-100');
    if (!(Number.isInteger(this.visibility) && this.visibility >= 2)) throw new Error('visibilityTimeoutSeconds must be at least 2');
    if (!(Number.isInteger(this.wait) && this.wait >= 0 && this.wait <= MAX_WAIT_SECONDS)) throw new Error('waitSeconds must be 0-20');
    if (!(Number.isInteger(this.retryDelay) && this.retryDelay >= 0)) throw new Error('retryDelaySeconds must be a non-negative integer');
    if (!options.apiKey && !this.platformLease) {
      this.identityURL = new URL(options.identityEndpoint ?? process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT ?? '');
      if (this.identityURL.protocol !== 'http:' || !['127.0.0.1', '[::1]', 'localhost'].includes(this.identityURL.hostname)) {
        throw new Error('Workload identity endpoint must be loopback HTTP; pass apiKey outside a Gregale VM');
      }
      this.identityURL.searchParams.set('audience', QUEUES_AUDIENCE);
    }
    this.fetchImpl = options.fetch ?? globalThis.fetch;
  }

  /** Runs until stop(); resolves once in-flight messages are settled. */
  run(): Promise<void> {
    this.running ??= this.loop();
    return this.running;
  }

  /** Stops leasing, lets in-flight handlers finish and settles their messages. */
  async stop(): Promise<void> {
    this.stopping.abort();
    await this.running;
  }

  private async loop(): Promise<void> {
    const stopped = new Promise<void>(resolve => this.stopping.signal.addEventListener('abort', () => resolve(), { once: true }));
    let backoff = 0;
    while (!this.stopping.signal.aborted) {
      const free = this.concurrency - this.inFlight.size;
      if (free <= 0) {
        await Promise.race([...this.inFlight, stopped]);
        continue;
      }
      let messages: LeasedMessage[];
      try {
        messages = await this.lease(free);
        backoff = 0;
      } catch (error) {
        if (this.stopping.signal.aborted) break;
        this.report(error);
        backoff = Math.min(Math.max(backoff * 2, 1000), MAX_BACKOFF_MS);
        await sleep(backoff, this.stopping.signal);
        continue;
      }
      for (const message of messages) {
        const done: Promise<void> = this.process(message).finally(() => this.inFlight.delete(done));
        this.inFlight.add(done);
      }
    }
    await Promise.allSettled([...this.inFlight]);
  }

  private async lease(max: number): Promise<LeasedMessage[]> {
    // Stopping aborts the long-poll; a message leased in that instant is
    // redelivered after its visibility timeout.
    const body = await this.call<{ messages: LeasedMessage[] }>('', {
      max_messages: max, visibility_timeout_seconds: this.visibility, wait_seconds: this.wait,
    }, this.wait * 1000, this.stopping.signal);
    if (!Array.isArray(body.messages)) throw new Error('Invalid queue lease response');
    return body.messages;
  }

  private async process(leased: LeasedMessage): Promise<void> {
    const lost = new AbortController();
    const heartbeat = setInterval(() => {
      this.settle(':extend', leased.receipt, { visibility_timeout_seconds: this.visibility })
        .then(held => { if (!held) lost.abort(new Error('Queue lease lost')); })
        .catch(error => this.report(error));
    }, (this.visibility * 1000) / 2);
    heartbeat.unref?.();
    const message: QueueMessage<T> = {
      id: leased.id, payload: leased.payload as T, headers: leased.headers ?? {},
      attempt: leased.attempt, enqueuedAt: new Date(leased.enqueued_at),
    };
    let failure: unknown;
    let failed = false;
    try {
      await this.handler(message, { signal: lost.signal });
    } catch (error) {
      failed = true;
      failure = error;
    } finally {
      clearInterval(heartbeat);
    }
    if (failed) this.report(failure);
    if (lost.signal.aborted) return; // the message belongs to its next delivery now
    try {
      const held = failed
        ? await this.settle(':nack', leased.receipt, { error: errorText(failure), delay_seconds: this.retryDelay })
        : await this.settle(':ack', leased.receipt, {});
      if (!held) this.report(new Error(`Queue lease for message ${leased.id} expired before it was settled`));
    } catch (error) {
      this.report(error);
    }
  }

  /** Applies one receipt; false when the receipt no longer owns its message. */
  private async settle(verb: string, receipt: string, extra: Record<string, unknown>): Promise<boolean> {
    const out = await this.call<SettleResponse>(verb, { receipts: [receipt], ...extra }, 0);
    return Array.isArray(out.succeeded) && out.succeeded.includes(receipt);
  }

  private async call<R>(verb: string, body: unknown, waitMs: number, stop?: AbortSignal): Promise<R> {
    const url = new URL(this.leasesURL.pathname + verb, this.leasesURL);
    const timeout = AbortSignal.timeout(waitMs + CALL_SLACK_MS);
    const response = await this.fetchImpl(url, {
      method: 'POST', signal: stop ? AbortSignal.any([stop, timeout]) : timeout, redirect: 'error', cache: 'no-store',
      headers: this.platformLease
        ? { 'Content-Type': 'application/json' }
        : { Authorization: `Bearer ${await this.credential()}`, 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (response.status === 401) this.token = undefined;
    if (!response.ok) throw new Error(`Queue ${verb || 'lease'} failed (${response.status})`);
    return await response.json() as R;
  }

  private async credential(): Promise<string> {
    if (this.options.apiKey) return this.options.apiKey;
    if (this.token && Date.now() < this.token.refreshAt) return this.token.value;
    const response = await this.fetchImpl(this.identityURL!, { redirect: 'error', cache: 'no-store', signal: AbortSignal.timeout(5000) });
    if (!response.ok) throw new Error('Queue workload identity unavailable');
    const token = await response.json() as { access_token?: unknown; expires_in?: unknown };
    if (typeof token.access_token !== 'string' || !token.access_token || token.access_token.length > 8192) throw new Error('Invalid workload identity');
    const lifetimeMs = typeof token.expires_in === 'number' && token.expires_in > 0 ? token.expires_in * 1000 : 300_000;
    this.token = { value: token.access_token, refreshAt: Date.now() + Math.max(lifetimeMs - TOKEN_REFRESH_MARGIN_MS, lifetimeMs / 2) };
    return this.token.value;
  }

  private report(error: unknown): void {
    try {
      this.options.onError?.(error);
    } catch {
      // A failing error hook must not stop the consumer.
    }
  }
}

export interface ConsumeQueueOptions extends QueueConsumerOptions {
  /** Stop gracefully on SIGTERM and SIGINT, then exit with that signal (default true). */
  handleSignals?: boolean;
}

/**
 * Starts a consumer. With handleSignals (the default) SIGTERM and SIGINT stop
 * leasing, let in-flight handlers finish within the app's stop grace period,
 * settle them, and then let the signal end the process.
 */
export function consumeQueue<T = unknown>(options: ConsumeQueueOptions, handler: QueueHandler<T>): GregaleQueueConsumer<T> {
  const consumer = new GregaleQueueConsumer<T>(options, handler);
  void consumer.run();
  if (options.handleSignals ?? true) {
    const onSignal = (signal: NodeJS.Signals): void => {
      process.removeListener('SIGTERM', onSignal);
      process.removeListener('SIGINT', onSignal);
      void consumer.stop().finally(() => process.kill(process.pid, signal));
    };
    process.once('SIGTERM', onSignal);
    process.once('SIGINT', onSignal);
  }
  return consumer;
}

// The platform lease base is HTTPS, or plain HTTP to the node's private bridge
// address; a candidate's egress reaches nothing else.
function platformLeaseBase(raw: string): URL {
  const base = new URL(raw);
  if (base.username || base.password || base.search || base.hash) throw new Error('Invalid GREGALE_QUEUE_LEASE_URL');
  if (base.protocol === 'https:' || (base.protocol === 'http:' && privateIPv4(base.hostname))) return base;
  throw new Error('GREGALE_QUEUE_LEASE_URL must be HTTPS or a private IPv4 HTTP address');
}

function privateIPv4(host: string): boolean {
  const parts = host.split('.');
  if (parts.length !== 4 || !parts.every(part => /^(0|[1-9][0-9]{0,2})$/.test(part) && Number(part) <= 255)) return false;
  const [a = -1, b = -1] = parts.map(Number);
  return a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168);
}

function errorText(error: unknown): string {
  const text = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
  const bytes = Buffer.from(text, 'utf8');
  return bytes.length <= ERROR_MAX_BYTES ? text : bytes.subarray(0, ERROR_MAX_BYTES).toString('utf8');
}

// Not unref'd: a back-off may be the only thing keeping a worker process
// alive. Stopping clears it.
function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise(resolve => {
    const timer = setTimeout(done, ms);
    function done(): void {
      clearTimeout(timer);
      signal.removeEventListener('abort', done);
      resolve();
    }
    signal.addEventListener('abort', done, { once: true });
  });
}
