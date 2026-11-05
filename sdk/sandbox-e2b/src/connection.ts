// One session's connection: its token, re-minted from the API key as it
// nears expiry, and its data-plane calls.

import { Api, ApiError, toSandboxError, type RequestArgs } from './api.js';
import { SandboxNotFoundError, TimeoutError } from './errors.js';

/** A session as the API shows it. */
export type SandboxView = {
  id: string;
  state: 'queued' | 'starting' | 'ready' | 'frozen' | 'closed' | 'expired' | 'failed';
  preset: string;
  ram_mb: number;
  network: { mode: string };
  idle_timeout_s: number;
  expires_at: string;
  labels: Record<string, string>;
  outcome?: { code: string; message?: string };
  close_requested_at?: string;
  created_at: string;
  token?: string;
  token_expires_at?: string;
};

const LIVE = new Set(['queued', 'starting', 'ready', 'frozen']);
const PLACED = new Set(['ready', 'frozen']);
const MARGIN_MS = 60_000;

export const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export class Connection {
  readonly api: Api;
  readonly sandboxId: string;
  /** The session's environment, sent with every command. */
  readonly envs: Record<string, string>;
  readonly readyTimeoutMs: number;
  #token?: { value: string; expires: number };

  constructor(args: { api: Api; sandboxId: string; envs?: Record<string, string>; readyTimeoutMs?: number }) {
    this.api = args.api;
    this.sandboxId = args.sandboxId;
    this.envs = args.envs ?? {};
    this.readyTimeoutMs = args.readyTimeoutMs ?? 120_000;
  }

  get path(): string {
    return `/v1/sandboxes/${this.sandboxId}`;
  }

  /** Reads the session, keeping the token the read minted. */
  async read(args: RequestArgs = {}): Promise<SandboxView> {
    let view: SandboxView;
    try {
      view = await this.api.control<SandboxView>('GET', this.path, args);
    } catch (err) {
      throw toSandboxError(err, `sandbox ${this.sandboxId}`);
    }
    this.#token = view.token && view.token_expires_at ? { value: view.token, expires: Date.parse(view.token_expires_at) } : undefined;
    return view;
  }

  /** Waits until the session runs on a node; fails if it has ended. */
  async waitReady(args: RequestArgs = {}): Promise<SandboxView> {
    const deadline = Date.now() + this.readyTimeoutMs;
    let delay = 250;
    for (;;) {
      const view = await this.read(args);
      if (!LIVE.has(view.state) || view.close_requested_at) {
        throw new SandboxNotFoundError(
          `sandbox ${this.sandboxId} is ${view.close_requested_at ? 'closing' : view.state}${view.outcome ? ` (${view.outcome.code})` : ''}`,
        );
      }
      if (PLACED.has(view.state) && this.#token) return view;
      if (Date.now() >= deadline) throw new TimeoutError(`sandbox ${this.sandboxId} did not start within ${this.readyTimeoutMs} ms`);
      await sleep(delay);
      delay = Math.min(delay * 2, 2_000);
    }
  }

  async #token_(): Promise<string> {
    if (!this.#token || this.#token.expires - MARGIN_MS <= Date.now()) await this.waitReady();
    return this.#token!.value;
  }

  /** A data-plane call; a token the data plane no longer takes is re-minted once. */
  async call(method: string, sub: string, args: RequestArgs = {}): Promise<Response> {
    const path = this.path + sub;
    try {
      try {
        return await this.api.data(method, path, await this.#token_(), args);
      } catch (err) {
        const stale = err instanceof ApiError && ((err.status === 404 && err.code === '') || err.code === 'sandbox_not_ready');
        if (!stale) throw err;
        this.#token = undefined;
        return await this.api.data(method, path, await this.#token_(), args);
      }
    } catch (err) {
      throw toSandboxError(err, `${method} ${sub}`);
    }
  }

  async json<T>(method: string, sub: string, args: RequestArgs = {}): Promise<T> {
    return (await (await this.call(method, sub, args)).json()) as T;
  }

  async control<T>(method: string, sub: string, args: RequestArgs = {}): Promise<T> {
    try {
      return await this.api.control<T>(method, this.path + sub, args);
    } catch (err) {
      throw toSandboxError(err, `${method} ${this.path + sub}`);
    }
  }
}
