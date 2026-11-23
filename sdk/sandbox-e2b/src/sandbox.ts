// Sandbox: an E2B-style handle on a Gregale sandbox session
// (ADR-20261027090013 §12). Written from E2B's public documentation; code
// moves by changing the import and the API key.

import { Api, toSandboxError, type ApiOptions } from './api.js';
import { CodeInterpreter, type Context, type Execution, type RunCodeOpts } from './code.js';
import { Commands, Pty } from './commands.js';
import { Connection, PAUSED, sleep, type SandboxView } from './connection.js';
import { InvalidArgumentError, NotFoundError, SandboxNotFoundError, TimeoutError, UnsupportedError } from './errors.js';
import { Filesystem } from './filesystem.js';

/** The harness presets a template name may be. */
const PRESETS = new Set(['codex', 'opencode', 'claude-code', 'code-interpreter']);
/** Template names E2B code commonly passes for the stock image. */
const DEFAULT_TEMPLATES = new Set(['base', 'default']);

export type ConnectionOpts = ApiOptions & { requestTimeoutMs?: number; signal?: AbortSignal };

export type SandboxOpts = ConnectionOpts & {
  /**
   * A preset (codex, opencode, claude-code, code-interpreter for runCode),
   * "base" for the default preset,
   * or the name of a template built with `gregale sandbox template publish`.
   */
  template?: string;
  /** Labels on the session. */
  metadata?: Record<string, string>;
  /**
   * Environment for every command in the session. It is set in the session
   * (PUT …/env) once it runs, so a later Sandbox.connect sees it too.
   */
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
  /**
   * persistent keeps the session's memory and /workspace volume across
   * pauses (pause(), connect() resumes); ephemeral (the default) ends with
   * its lifetime. A persistent session has no lifetimeMs.
   */
  kind?: 'ephemeral' | 'persistent';
  /** A persistent session's /workspace volume in GB, within the plan. */
  volumeGb?: number;
  /** A persistent session's vCPUs, within the plan. */
  vcpu?: number;
};

export type ForkOpts = ConnectionOpts & Pick<SandboxOpts, 'portVisibility' | 'readyTimeoutMs'> & {
  /** The forks' kind; ephemeral by default. */
  kind?: 'ephemeral' | 'persistent';
  /** Labels on the forks; the source's by default. */
  metadata?: Record<string, string>;
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
    templateId: view.template ?? view.preset,
    metadata: view.labels ?? {},
    startedAt: new Date(view.created_at),
    endAt: new Date(view.expires_at),
    state: view.state === 'frozen' || PAUSED.has(view.state) ? 'paused' : 'running',
    cpuCount: view.vcpu || Math.max(1, Math.round(view.ram_mb / 1024)),
    memoryMB: view.ram_mb,
  };
}

/** Asks for a paused persistent session's resume; waitReady then waits for it. */
async function resumeIfPaused(conn: Connection, signal?: AbortSignal): Promise<void> {
  const view = await conn.read({ signal });
  if (view.state !== 'paused' && view.state !== 'pausing') return;
  await conn.control('POST', '/resume', { signal });
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
    // A paused session is frozen or paused (persistent): filtered here.
    if (states?.length === 1 && states[0] === 'running') query.state = 'ready';
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
      .filter((v) => (v.state === 'ready' || v.state === 'frozen' || v.state === 'starting' || v.state === 'queued' || PAUSED.has(v.state)) && !v.close_requested_at)
      .map(info)
      .filter((i) => !states || states.length === 0 || states.includes(i.state));
  }
}

export class Sandbox {
  readonly sandboxId: string;
  readonly files: Filesystem;
  readonly commands: Commands;
  readonly pty: Pty;
  readonly #code: CodeInterpreter;
  readonly #conn: Connection;
  readonly #portVisibility: 'private' | 'public';
  readonly #ports = new Map<number, PortView>();

  /** Use Sandbox.create or Sandbox.connect. */
  constructor(conn: Connection, portVisibility: 'private' | 'public' = 'private') {
    this.#conn = conn;
    this.sandboxId = conn.sandboxId;
    this.files = new Filesystem(conn);
    this.commands = new Commands(conn);
    this.pty = new Pty(this.commands);
    this.#code = new CodeInterpreter(conn);
    this.#portVisibility = portVisibility;
  }

  /** Creates a session and waits until it runs. */
  static async create<S extends typeof Sandbox>(this: S, opts?: SandboxOpts): Promise<InstanceType<S>>;
  static async create<S extends typeof Sandbox>(this: S, template: string, opts?: SandboxOpts): Promise<InstanceType<S>>;
  static async create<S extends typeof Sandbox>(this: S, a?: string | SandboxOpts, b?: SandboxOpts): Promise<InstanceType<S>> {
    const opts: SandboxOpts = typeof a === 'string' ? { ...b, template: a } : (a ?? {});
    const template = opts.template ?? 'base';
    // A name that is not a preset is one of the account's templates.
    const source = PRESETS.has(template) ? { preset: template } : DEFAULT_TEMPLATES.has(template) ? { preset: 'codex' } : { template };
    const network = opts.network ?? (opts.allowInternetAccess === true ? 'web' : opts.allowInternetAccess === false ? 'none' : undefined);
    const api = new Api(opts);
    let view: SandboxView;
    try {
      view = await api.control<SandboxView>('POST', '/v1/sandboxes', {
        json: {
          ...source,
          ram_mb: opts.ramMb,
          network: network ? { mode: network } : undefined,
          idle_timeout_s: opts.timeoutMs ? Math.ceil(opts.timeoutMs / 1000) : undefined,
          lifetime_s: opts.lifetimeMs ? Math.ceil(opts.lifetimeMs / 1000) : undefined,
          labels: opts.metadata,
          snapshot_id: opts.snapshotId,
          kind: opts.kind,
          volume_gb: opts.volumeGb,
          vcpu: opts.vcpu,
        },
        signal: opts.signal,
        timeoutMs: opts.requestTimeoutMs,
      });
    } catch (err) {
      throw toSandboxError(err, 'Sandbox.create');
    }
    const conn = new Connection({ api, sandboxId: view.id, readyTimeoutMs: opts.readyTimeoutMs });
    const sandbox = new this(conn, opts.portVisibility) as InstanceType<S>;
    try {
      await conn.waitReady({ signal: opts.signal });
      if (opts.envs && Object.keys(opts.envs).length > 0) await sandbox.setEnvs(opts.envs, { signal: opts.signal });
      for (const port of opts.ports ?? []) await sandbox.exposePort(port);
    } catch (err) {
      await sandbox.kill().catch(() => undefined);
      throw err;
    }
    return sandbox;
  }

  /**
   * Connects to a live session; its token is re-minted with your API key. A
   * paused persistent session is resumed first, as E2B's connect does.
   */
  static async connect<S extends typeof Sandbox>(this: S, sandboxId: string, opts: SandboxConnectOpts = {}): Promise<InstanceType<S>> {
    if (!/^[0-9a-f-]{36}$/.test(sandboxId)) throw new InvalidArgumentError(`"${sandboxId}" is not a Gregale sandbox ID`);
    const conn = new Connection({ api: new Api(opts), sandboxId, envs: opts.envs, readyTimeoutMs: opts.readyTimeoutMs });
    await resumeIfPaused(conn, opts.signal);
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

  /** Pauses a persistent session by ID; false when it was already paused. */
  static async betaPause(sandboxId: string, opts: ConnectionOpts = {}): Promise<boolean> {
    return await new Sandbox(new Connection({ api: new Api(opts), sandboxId })).betaPause();
  }

  static async pause(sandboxId: string, opts: ConnectionOpts = {}): Promise<boolean> {
    return await Sandbox.betaPause(sandboxId, opts);
  }

  static async setTimeout(sandboxId: string, timeoutMs: number, opts: ConnectionOpts = {}): Promise<void> {
    await new Sandbox(new Connection({ api: new Api(opts), sandboxId })).setTimeout(timeoutMs);
  }

  /** Waits until the session runs, resuming it first if it is paused. */
  async connect(): Promise<this> {
    await resumeIfPaused(this.#conn);
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

  /**
   * Gregale only: replaces the session's environment, which every later
   * command gets under its own envs. It is kept in the session's memory, not
   * stored; an empty object clears it. Answers the names now set.
   */
  /**
   * Runs code in a code context's Jupyter kernel; the session must come from
   * template 'code-interpreter'. Variables and imports persist between runs
   * of one context.
   */
  async runCode(code: string, opts?: RunCodeOpts): Promise<Execution> {
    return this.#code.runCode(code, opts);
  }

  /** Creates a code context: its own kernel, in cwd (default /workspace). */
  async createCodeContext(opts?: { cwd?: string; language?: string; signal?: AbortSignal }): Promise<Context> {
    return this.#code.createCodeContext(opts);
  }

  async listCodeContexts(opts?: { signal?: AbortSignal }): Promise<Context[]> {
    return this.#code.listCodeContexts(opts);
  }

  async restartCodeContext(ctx: Context | string, opts?: { signal?: AbortSignal }): Promise<void> {
    return this.#code.restartCodeContext(ctx, opts);
  }

  async removeCodeContext(ctx: Context | string, opts?: { signal?: AbortSignal }): Promise<void> {
    return this.#code.removeCodeContext(ctx, opts);
  }

  async setEnvs(envs: Record<string, string>, opts: { signal?: AbortSignal } = {}): Promise<string[]> {
    const res = await this.#conn.json<{ names: string[] }>('PUT', '/env', { json: { env: envs }, signal: opts.signal });
    return res.names;
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

  /**
   * Pauses a persistent session: its memory and /workspace are kept and
   * Sandbox.connect resumes it (ADR-20261122132055). Resolves once it is
   * paused; false when it already was. An ephemeral session cannot pause:
   * it freezes on its own when idle.
   */
  async pause(opts: { signal?: AbortSignal } = {}): Promise<boolean> {
    const view = await this.#conn.read({ signal: opts.signal });
    if (view.kind !== 'persistent') {
      throw new UnsupportedError(
        'pause',
        'idle sessions freeze on their own and resume on the next call; create the session with kind: "persistent" to pause it and keep its memory',
      );
    }
    if (view.state === 'paused') return false;
    await this.#conn.control('POST', '/pause', { signal: opts.signal });
    const deadline = Date.now() + this.#conn.readyTimeoutMs;
    for (let delay = 250; ; delay = Math.min(delay * 2, 2_000)) {
      const now = await this.#conn.read({ signal: opts.signal });
      if (now.state === 'paused') return true;
      if (!PAUSED.has(now.state) && now.state !== 'ready' && now.state !== 'frozen') {
        throw new SandboxNotFoundError(`sandbox ${this.sandboxId} is ${now.state}${now.outcome ? ` (${now.outcome.code})` : ''}`);
      }
      if (Date.now() >= deadline) throw new TimeoutError(`sandbox ${this.sandboxId} did not pause within ${this.#conn.readyTimeoutMs} ms`);
      await sleep(delay);
    }
  }

  async betaPause(opts: { signal?: AbortSignal } = {}): Promise<boolean> {
    return await this.pause(opts);
  }

  /**
   * Gregale only: starts count new sessions from this one's memory and
   * /workspace as they are now, and waits until they run. A running
   * session is captured without stopping; a paused one forks from its
   * pause. Within the plan's SandboxForkMax.
   */
  async fork(count = 1, opts: ForkOpts = {}): Promise<Sandbox[]> {
    const res = await this.#conn.control<{ forks: SandboxView[] }>('POST', '/fork', {
      json: { count, kind: opts.kind, labels: opts.metadata },
      signal: opts.signal,
    });
    const api = opts.apiKey || opts.apiBase || opts.fetch ? new Api(opts) : this.#conn.api;
    const forks = res.forks.map(
      (v) => new (this.constructor as typeof Sandbox)(new Connection({ api, sandboxId: v.id, readyTimeoutMs: opts.readyTimeoutMs }), opts.portVisibility ?? this.#portVisibility),
    );
    await Promise.all(forks.map((f) => f.#conn.waitReady({ signal: opts.signal })));
    return forks;
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
