// Sandbox: an E2B-style handle on a Gregale sandbox session
// (ADR-20261027090013 §12). Written from E2B's public documentation; code
// moves by changing the import and the API key.

import { Api, toSandboxError, type ApiOptions } from './api.js';
import { Commands } from './commands.js';
import { Connection, type SandboxView } from './connection.js';
import { InvalidArgumentError, NotFoundError, UnsupportedError } from './errors.js';
import { Filesystem } from './filesystem.js';

/** The harness presets a template name may be. */
const PRESETS = new Set(['codex', 'opencode', 'claude-code']);
/** Template names E2B code commonly passes for the stock image. */
const DEFAULT_TEMPLATES = new Set(['base', 'default']);

export type ConnectionOpts = ApiOptions & { requestTimeoutMs?: number; signal?: AbortSignal };

export type SandboxOpts = ConnectionOpts & {
  /** A preset (codex, opencode, claude-code); "base" is the default preset. */
  template?: string;
  /** Labels on the session. */
  metadata?: Record<string, string>;
  /** Environment for every command in the session. */
  envs?: Record<string, string>;
  /** Idle time before the session freezes, in ms (Gregale: idle_timeout_s). */
  timeoutMs?: number;
  /** true asks for the web network mode; false for none. Default: registries. */
  allowInternetAccess?: boolean;
  // Gregale options.
  /** none, registries (the default) or web. Overrides allowInternetAccess. */
  network?: 'none' | 'registries' | 'web';
  /** VM memory, a multiple of 512 within the plan's limit. */
  ramMb?: number;
  /** The session's lifetime in ms, within the plan's limit. */
  lifetimeMs?: number;
  /** Ports to expose at create, so getHost(port) answers at once. */
  ports?: number[];
  /** Visibility of exposed ports; private (behind a preview token) by default. */
  portVisibility?: 'private' | 'public';
  /** Start from a workspace snapshot. */
  snapshotId?: string;
  /** How long create and connect wait for the VM; 120 s by default. */
  readyTimeoutMs?: number;
};

export type SandboxConnectOpts = ConnectionOpts & Pick<SandboxOpts, 'envs' | 'portVisibility' | 'readyTimeoutMs'>;

export type SandboxState = 'running' | 'paused';

export type SandboxInfo = {
  sandboxId: string;
  templateId: string;
  metadata: Record<string, string>;
  startedAt: Date;
  endAt: Date;
  state: SandboxState;
  cpuCount: number;
  memoryMB: number;
};

export type SandboxListOpts = ApiOptions & { query?: { metadata?: Record<string, string>; state?: SandboxState[] }; limit?: number };

type PortView = { port: number; url: string; preview_token?: string };

function info(view: SandboxView): SandboxInfo {
  return {
    sandboxId: view.id,
    templateId: view.preset,
    metadata: view.labels ?? {},
    startedAt: new Date(view.created_at),
    endAt: new Date(view.expires_at),
    state: view.state === 'frozen' ? 'paused' : 'running',
    cpuCount: Math.max(1, Math.round(view.ram_mb / 1024)),
    memoryMB: view.ram_mb,
  };
}

/** Pages of the account's live sessions, newest first. */
export class SandboxPaginator {
  readonly #api: Api;
  readonly #opts: SandboxListOpts;
  #before?: string;
  #done = false;

  constructor(opts: SandboxListOpts = {}) {
    this.#api = new Api(opts);
    this.#opts = opts;
  }

  get hasNext(): boolean {
    return !this.#done;
  }

  async nextItems(): Promise<SandboxInfo[]> {
    if (this.#done) return [];
    const query: Record<string, string | number | undefined> = { limit: this.#opts.limit ?? 100, before: this.#before };
    const states = this.#opts.query?.state;
    if (states?.length === 1) query.state = states[0] === 'paused' ? 'frozen' : 'ready';
    for (const [k, v] of Object.entries(this.#opts.query?.metadata ?? {})) query.selector = `${query.selector ? `${query.selector},` : ''}${k}=${v}`;
    let page: { items: SandboxView[]; next_before?: string };
    try {
      page = await this.#api.control('GET', '/v1/sandboxes', { query });
    } catch (err) {
      throw toSandboxError(err, 'Sandbox.list');
    }
    this.#before = page.next_before;
    this.#done = !page.next_before;
    return page.items
      .filter((v) => (v.state === 'ready' || v.state === 'frozen' || v.state === 'starting' || v.state === 'queued') && !v.close_requested_at)
      .map(info)
      .filter((i) => !states || states.length !== 2 || states.includes(i.state));
  }
}

export class Sandbox {
  readonly sandboxId: string;
  readonly files: Filesystem;
  readonly commands: Commands;
  readonly pty = {
    create: async (): Promise<never> => {
      throw new UnsupportedError('pty.create', 'interactive terminals are not available on Gregale yet; run commands with commands.run');
    },
    sendInput: async (): Promise<never> => {
      throw new UnsupportedError('pty.sendInput', 'interactive terminals are not available on Gregale yet');
    },
    resize: async (): Promise<never> => {
      throw new UnsupportedError('pty.resize', 'interactive terminals are not available on Gregale yet');
    },
    kill: async (): Promise<never> => {
      throw new UnsupportedError('pty.kill', 'interactive terminals are not available on Gregale yet');
    },
  };
  readonly #conn: Connection;
  readonly #portVisibility: 'private' | 'public';
  readonly #ports = new Map<number, PortView>();

  /** Use Sandbox.create or Sandbox.connect. */
  constructor(conn: Connection, portVisibility: 'private' | 'public' = 'private') {
    this.#conn = conn;
    this.sandboxId = conn.sandboxId;
    this.files = new Filesystem(conn);
    this.commands = new Commands(conn);
    this.#portVisibility = portVisibility;
  }

  /** Creates a session and waits until it runs. */
  static async create<S extends typeof Sandbox>(this: S, opts?: SandboxOpts): Promise<InstanceType<S>>;
  static async create<S extends typeof Sandbox>(this: S, template: string, opts?: SandboxOpts): Promise<InstanceType<S>>;
  static async create<S extends typeof Sandbox>(this: S, a?: string | SandboxOpts, b?: SandboxOpts): Promise<InstanceType<S>> {
    const opts: SandboxOpts = typeof a === 'string' ? { ...b, template: a } : (a ?? {});
    const template = opts.template ?? 'base';
    if (!PRESETS.has(template) && !DEFAULT_TEMPLATES.has(template)) {
      throw new UnsupportedError(`template "${template}"`, 'custom templates are not available yet; use codex, opencode or claude-code');
    }
    const network = opts.network ?? (opts.allowInternetAccess === true ? 'web' : opts.allowInternetAccess === false ? 'none' : undefined);
    const api = new Api(opts);
    let view: SandboxView;
    try {
      view = await api.control<SandboxView>('POST', '/v1/sandboxes', {
        json: {
          preset: PRESETS.has(template) ? template : 'codex',
          ram_mb: opts.ramMb,
          network: network ? { mode: network } : undefined,
          idle_timeout_s: opts.timeoutMs ? Math.ceil(opts.timeoutMs / 1000) : undefined,
          lifetime_s: opts.lifetimeMs ? Math.ceil(opts.lifetimeMs / 1000) : undefined,
          labels: opts.metadata,
          snapshot_id: opts.snapshotId,
        },
        signal: opts.signal,
        timeoutMs: opts.requestTimeoutMs,
      });
    } catch (err) {
      throw toSandboxError(err, 'Sandbox.create');
    }
    const conn = new Connection({ api, sandboxId: view.id, envs: opts.envs, readyTimeoutMs: opts.readyTimeoutMs });
    const sandbox = new this(conn, opts.portVisibility) as InstanceType<S>;
    try {
      await conn.waitReady({ signal: opts.signal });
      for (const port of opts.ports ?? []) await sandbox.exposePort(port);
    } catch (err) {
      await sandbox.kill().catch(() => undefined);
      throw err;
    }
    return sandbox;
  }

  /** Connects to a live session; its token is re-minted with your API key. */
  static async connect<S extends typeof Sandbox>(this: S, sandboxId: string, opts: SandboxConnectOpts = {}): Promise<InstanceType<S>> {
    if (!/^[0-9a-f-]{36}$/.test(sandboxId)) throw new InvalidArgumentError(`"${sandboxId}" is not a Gregale sandbox ID`);
    const conn = new Connection({ api: new Api(opts), sandboxId, envs: opts.envs, readyTimeoutMs: opts.readyTimeoutMs });
    await conn.waitReady({ signal: opts.signal });
    const sandbox = new this(conn, opts.portVisibility) as InstanceType<S>;
    for (const p of await sandbox.#listPorts()) sandbox.#ports.set(p.port, p);
    return sandbox;
  }

  static list(opts?: SandboxListOpts): SandboxPaginator {
    return new SandboxPaginator(opts);
  }

  /** Closes a session by ID; false when there is none. */
  static async kill(sandboxId: string, opts: ConnectionOpts = {}): Promise<boolean> {
    const conn = new Connection({ api: new Api(opts), sandboxId });
    return await new Sandbox(conn).kill();
  }

  static async getInfo(sandboxId: string, opts: ConnectionOpts = {}): Promise<SandboxInfo> {
    return info(await new Connection({ api: new Api(opts), sandboxId }).read({ signal: opts.signal }));
  }

  static async setTimeout(sandboxId: string, timeoutMs: number, opts: ConnectionOpts = {}): Promise<void> {
    await new Sandbox(new Connection({ api: new Api(opts), sandboxId })).setTimeout(timeoutMs);
  }

  async connect(): Promise<this> {
    await this.#conn.waitReady();
    return this;
  }

  async isRunning(): Promise<boolean> {
    try {
      const view = await this.#conn.read();
      return (view.state === 'ready' || view.state === 'frozen' || view.state === 'starting' || view.state === 'queued') && !view.close_requested_at;
    } catch (err) {
      if (err instanceof NotFoundError) return false;
      throw err;
    }
  }

  async getInfo(): Promise<SandboxInfo> {
    return info(await this.#conn.read());
  }

  /** Changes the idle time before the session freezes. */
  async setTimeout(timeoutMs: number): Promise<void> {
    await this.#conn.control('PATCH', '', { json: { idle_timeout_s: Math.ceil(timeoutMs / 1000) } });
  }

  /** Closes the session; its VM and workspace go with it. False when it was gone. */
  async kill(): Promise<boolean> {
    try {
      await this.#conn.control('DELETE', '');
      return true;
    } catch (err) {
      if (err instanceof NotFoundError) return false;
      throw err;
    }
  }

  async pause(): Promise<never> {
    throw new UnsupportedError('pause', 'idle sessions freeze on their own and resume on the next call; long memory-keeping pauses need persistent sessions, which are not available yet');
  }

  async betaPause(): Promise<never> {
    return await this.pause();
  }

  /**
   * Exposes a port at its preview URL and answers its host. A private
   * port's preview token is what previewToken(port) returns.
   */
  async exposePort(port: number, opts: { visibility?: 'private' | 'public' } = {}): Promise<string> {
    const view = await this.#conn.control<PortView>('POST', '/ports', { json: { port, visibility: opts.visibility ?? this.#portVisibility } });
    this.#ports.set(port, view);
    return new URL(view.url).host;
  }

  /**
   * The preview host of an exposed port. Gregale exposes ports with an API
   * call, so a port must be exposed first: pass `ports` at create, or await
   * exposePort(port).
   */
  getHost(port: number): string {
    const view = this.#ports.get(port);
    if (!view) {
      throw new UnsupportedError(`getHost(${port}) before the port is exposed`, `await sandbox.exposePort(${port}), or pass ports: [${port}] to Sandbox.create`);
    }
    return new URL(view.url).host;
  }

  /** The preview token a private port's requests need (bearer or ?gregale_preview_token=). */
  previewToken(port: number): string | undefined {
    return this.#ports.get(port)?.preview_token;
  }

  async #listPorts(): Promise<PortView[]> {
    try {
      return (await this.#conn.control<{ items: PortView[] }>('GET', '/ports')).items;
    } catch {
      // Previews may be off on this installation.
      return [];
    }
  }

  async getMcpUrl(): Promise<never> {
    throw new UnsupportedError('getMcpUrl', 'sandbox MCP gateways are not available on Gregale');
  }

  async createSnapshot(): Promise<{ snapshotId: string }> {
    const res = await this.#conn.json<{ snapshot_id: string }>('POST', '/workspace:persist', { json: {} });
    return { snapshotId: res.snapshot_id };
  }
}
