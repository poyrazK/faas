// sandbox.commands and sandbox.pty: commands in the session, foreground or
// background, and terminals. Gregale names a background command by a
// handle; this module gives each a number, which is what callers know as its
// pid. A terminal is a background command on a pseudo-terminal: its output is
// polled like any background command's, and its input, end of input and
// resizes go to POST …/exec/{handle}/input.

import { type RequestArgs } from './api.js';
import { sleep, type Connection } from './connection.js';
import { CommandExitError, NotFoundError, UnsupportedError, type CommandResult } from './errors.js';

/** User a command runs as: "root", or the session's unprivileged user. */
export type Username = string;

export type CommandRequestOpts = { requestTimeoutMs?: number; signal?: AbortSignal };

export type CommandStartOpts = CommandRequestOpts & {
  background?: boolean;
  cwd?: string;
  user?: Username;
  envs?: Record<string, string>;
  onStdout?: (data: string) => void | Promise<void>;
  onStderr?: (data: string) => void | Promise<void>;
  /** Background commands always take input (sendStdin); a foreground one cannot. */
  stdin?: boolean;
  /** How long the command may run; 60 s by default, within the plan's cap. */
  timeoutMs?: number;
  /**
   * Gregale extension, background commands only: the command gets SIGUSR2 in
   * each fork of the session, so it can reseed its own random state.
   */
  reseedOnFork?: boolean;
};

export type ProcessInfo = { pid: number; tag?: string; cmd: string; args: string[]; envs: Record<string, string>; cwd?: string };

export type { CommandResult };

type ExecResponse = { stdout: string; stderr: string; exit_code: number; timed_out?: boolean; killed?: boolean; wall_time_s: number };
type GuestCommand = { handle: string; argv: string[]; running: boolean; tty?: boolean; exit_code?: number };

/** A terminal's size. */
export type PtySize = { cols: number; rows: number };

export type PtyCreateOpts = CommandRequestOpts &
  PtySize & {
    /** Called with the terminal's output as it arrives. */
    onData: (data: Uint8Array) => void | Promise<void>;
    cwd?: string;
    envs?: Record<string, string>;
    user?: Username;
    /** How long the terminal may run; 60 s by default, within the plan's cap. */
    timeoutMs?: number;
  };

type InputBody = { data?: string; eof?: boolean; rows?: number; cols?: number };

const base64 = (data: string | Uint8Array) => Buffer.from(data).toString('base64');
type ExecOutput = GuestCommand & { stdout: string | null; stderr: string | null; stdout_offset: number; stderr_offset: number };

const POLL_MS = 200;

function runAs(user: Username | undefined): string | undefined {
  if (user === undefined || user === 'user' || user === 'app') return undefined;
  if (user === 'root') return 'root';
  throw new UnsupportedError(`user "${user}"`, 'Gregale sandboxes run commands as "user" (unprivileged) or "root"');
}

function resultOf(res: ExecResponse): CommandResult {
  const error = res.timed_out ? 'the command timed out' : res.killed ? 'the command was killed' : undefined;
  return { exitCode: res.exit_code, stdout: res.stdout, stderr: res.stderr, ...(error ? { error } : {}) };
}

/** A background command, polled for its output. */
export class CommandHandle {
  readonly pid: number;
  readonly #conn: Connection;
  readonly #handle: string;
  readonly #onStdout?: CommandStartOpts['onStdout'];
  readonly #onStderr?: CommandStartOpts['onStderr'];
  readonly #onData?: PtyCreateOpts['onData'];
  #stdout = '';
  #stderr = '';
  #stdoutOffset = 0;
  #stderrOffset = 0;
  #exitCode?: number;
  #disconnected = false;
  #wait?: Promise<CommandResult>;

  constructor(args: {
    pid: number;
    conn: Connection;
    handle: string;
    onStdout?: CommandStartOpts['onStdout'];
    onStderr?: CommandStartOpts['onStderr'];
    onData?: PtyCreateOpts['onData'];
  }) {
    this.pid = args.pid;
    this.#conn = args.conn;
    this.#handle = args.handle;
    this.#onStdout = args.onStdout;
    this.#onStderr = args.onStderr;
    this.#onData = args.onData;
    if (this.#onStdout || this.#onStderr || this.#onData) {
      // Callbacks see output as it arrives, as with a streaming client.
      this.#wait = this.#follow();
      this.#wait.catch(() => undefined);
    }
  }

  get stdout(): string {
    return this.#stdout;
  }

  get stderr(): string {
    return this.#stderr;
  }

  get exitCode(): number | undefined {
    return this.#exitCode;
  }

  get error(): string | undefined {
    return undefined;
  }

  /** Waits for the command to end. A non-zero exit throws CommandExitError. */
  async wait(): Promise<CommandResult> {
    this.#wait ??= this.#follow();
    const result = await this.#wait;
    if (result.exitCode !== 0) throw new CommandExitError(result);
    return result;
  }

  /** Stops following the output; the command keeps running. */
  async disconnect(): Promise<void> {
    this.#disconnected = true;
  }

  async kill(): Promise<boolean> {
    try {
      await this.#conn.call('DELETE', `/exec/${this.#handle}`);
      return true;
    } catch (err) {
      if (err instanceof NotFoundError) return false;
      throw err;
    }
  }

  /** Writes to the command's stdin. */
  async sendStdin(data: string, opts: CommandRequestOpts = {}): Promise<void> {
    await sendInput(this.#conn, this.#handle, { data: base64(data) }, opts);
  }

  async #follow(): Promise<CommandResult> {
    for (;;) {
      const out = await this.#conn.json<ExecOutput>('GET', `/exec/${this.#handle}`, {
        query: { stdout_offset: this.#stdoutOffset, stderr_offset: this.#stderrOffset },
      });
      const raw = out.stdout ? Buffer.from(out.stdout, 'base64') : undefined;
      if (raw && this.#onData) await this.#onData(new Uint8Array(raw));
      const stdout = raw ? raw.toString('utf8') : '';
      const stderr = out.stderr ? Buffer.from(out.stderr, 'base64').toString('utf8') : '';
      const progressed = out.stdout_offset !== this.#stdoutOffset || out.stderr_offset !== this.#stderrOffset;
      this.#stdoutOffset = out.stdout_offset;
      this.#stderrOffset = out.stderr_offset;
      if (stdout) {
        this.#stdout += stdout;
        await this.#onStdout?.(stdout);
      }
      if (stderr) {
        this.#stderr += stderr;
        await this.#onStderr?.(stderr);
      }
      if (!out.running && !progressed) {
        this.#exitCode = out.exit_code ?? 0;
        return { exitCode: this.#exitCode, stdout: this.#stdout, stderr: this.#stderr };
      }
      if (this.#disconnected) return { exitCode: -1, stdout: this.#stdout, stderr: this.#stderr, error: 'disconnected' };
      if (out.running) await sleep(POLL_MS);
    }
  }
}

async function sendInput(conn: Connection, handle: string, body: InputBody, opts: CommandRequestOpts): Promise<void> {
  await conn.call('POST', `/exec/${handle}/input`, { json: body, signal: opts.signal });
}

export class Commands {
  readonly #conn: Connection;
  readonly #pids = new Map<string, number>();
  readonly #handles = new Map<number, string>();
  #nextPid = 1;

  constructor(conn: Connection) {
    this.#conn = conn;
  }

  #pid(handle: string): number {
    let pid = this.#pids.get(handle);
    if (pid === undefined) {
      pid = this.#nextPid++;
      this.#pids.set(handle, pid);
      this.#handles.set(pid, handle);
    }
    return pid;
  }

  #handleOf(pid: number): string {
    const handle = this.#handles.get(pid);
    if (!handle) throw new NotFoundError(`no command with pid ${pid} in this client; commands.list() names the running ones`);
    return handle;
  }

  /**
   * Runs a shell command. In the foreground it answers the result, and a
   * non-zero exit throws CommandExitError; with `background: true` it
   * answers a handle at once.
   */
  run(cmd: string, opts: CommandStartOpts & { background: true }): Promise<CommandHandle>;
  run(cmd: string, opts?: CommandStartOpts & { background?: false }): Promise<CommandResult>;
  run(cmd: string, opts?: CommandStartOpts): Promise<CommandHandle | CommandResult>;
  async run(cmd: string, opts: CommandStartOpts = {}): Promise<CommandHandle | CommandResult> {
    if (opts.stdin && !opts.background) {
      throw new UnsupportedError('commands.run({ stdin: true })', 'only background commands take input; run it with background: true and use sendStdin');
    }
    if (opts.reseedOnFork && !opts.background) {
      throw new UnsupportedError('commands.run({ reseedOnFork: true })', 'only a background command runs on in a fork; run it with background: true');
    }
    const envs = { ...this.#conn.envs, ...opts.envs };
    const body = {
      cmd: ['/bin/bash', '-l', '-c', cmd],
      workdir: opts.cwd,
      env: Object.keys(envs).length > 0 ? envs : undefined,
      run_as: runAs(opts.user),
      timeout_ms: opts.timeoutMs || undefined,
      background: opts.background || undefined,
      reseed_on_fork: opts.reseedOnFork || undefined,
    };
    const req: RequestArgs = { json: body, signal: opts.signal };
    if (opts.background) {
      const { handle } = await this.#conn.json<{ handle: string }>('POST', '/exec', req);
      return new CommandHandle({ pid: this.#pid(handle), conn: this.#conn, handle, onStdout: opts.onStdout, onStderr: opts.onStderr });
    }
    const result =
      opts.onStdout || opts.onStderr ? await this.#stream(req, opts) : resultOf(await this.#conn.json<ExecResponse>('POST', '/exec', req));
    if (result.exitCode !== 0) throw new CommandExitError(result);
    return result;
  }

  /** A foreground command over server-sent events, calling back per chunk. */
  async #stream(req: RequestArgs, opts: CommandStartOpts): Promise<CommandResult> {
    const res = await this.#conn.call('POST', '/exec', { ...req, accept: 'text/event-stream' });
    let stdout = '';
    let stderr = '';
    let result: CommandResult | undefined;
    const decoder = new TextDecoder();
    let buffer = '';
    for await (const chunk of res.body as unknown as AsyncIterable<Uint8Array>) {
      buffer += decoder.decode(chunk, { stream: true });
      let end: number;
      while ((end = buffer.indexOf('\n\n')) >= 0) {
        const frame = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        const event = /^event: (.*)$/m.exec(frame)?.[1];
        const data = /^data: (.*)$/m.exec(frame)?.[1];
        if (!event || data === undefined) continue;
        const value = JSON.parse(data);
        if (event === 'stdout') {
          stdout += value;
          await opts.onStdout?.(value);
        } else if (event === 'stderr') {
          stderr += value;
          await opts.onStderr?.(value);
        } else if (event === 'result') {
          result = resultOf({ ...(value as ExecResponse), stdout, stderr });
        } else if (event === 'error') {
          throw new CommandExitError({ exitCode: -1, stdout, stderr, error: (value as { detail?: string }).detail ?? 'the command failed' });
        }
      }
    }
    return result ?? { exitCode: -1, stdout, stderr, error: 'the stream ended without a result' };
  }

  /** The session's running background commands. */
  async list(opts: CommandRequestOpts = {}): Promise<ProcessInfo[]> {
    const res = await this.#conn.json<{ commands: GuestCommand[] }>('GET', '/exec', { signal: opts.signal });
    return res.commands
      .filter((c) => c.running)
      .map((c) => ({ pid: this.#pid(c.handle), tag: c.handle, cmd: c.argv[0] ?? '', args: c.argv.slice(1), envs: {} }));
  }

  /** Kills a background command; false when there is none. */
  async kill(pid: number, opts: CommandRequestOpts = {}): Promise<boolean> {
    try {
      await this.#conn.call('DELETE', `/exec/${this.#handleOf(pid)}`, { signal: opts.signal });
      return true;
    } catch (err) {
      if (err instanceof NotFoundError) return false;
      throw err;
    }
  }

  /** Follows a background command again, from the start of its kept output. */
  async connect(pid: number, opts: Pick<CommandStartOpts, 'onStdout' | 'onStderr'> = {}): Promise<CommandHandle> {
    return new CommandHandle({ pid, conn: this.#conn, handle: this.#handleOf(pid), ...opts });
  }

  /** Writes to a background command's stdin. */
  async sendStdin(pid: number, data: string, opts: CommandRequestOpts = {}): Promise<void> {
    await sendInput(this.#conn, this.#handleOf(pid), { data: base64(data) }, opts);
  }

  /** Ends a background command's input: it reads end of file. */
  async closeStdin(pid: number, opts: CommandRequestOpts = {}): Promise<void> {
    await sendInput(this.#conn, this.#handleOf(pid), { eof: true }, opts);
  }

  /** @internal Starts an interactive login shell on a terminal (sandbox.pty). */
  async startTerminal(opts: PtyCreateOpts): Promise<CommandHandle> {
    const envs = { ...this.#conn.envs, ...opts.envs };
    const body = {
      cmd: ['/bin/bash', '-i', '-l'],
      workdir: opts.cwd,
      env: Object.keys(envs).length > 0 ? envs : undefined,
      run_as: runAs(opts.user),
      timeout_ms: opts.timeoutMs || undefined,
      background: true,
      tty: true,
      rows: opts.rows,
      cols: opts.cols,
    };
    const { handle } = await this.#conn.json<{ handle: string }>('POST', '/exec', { json: body, signal: opts.signal });
    return new CommandHandle({ pid: this.#pid(handle), conn: this.#conn, handle, onData: opts.onData });
  }

  /** @internal Sends input, end of input or a resize to a background command. */
  async input(pid: number, body: InputBody, opts: CommandRequestOpts = {}): Promise<void> {
    await sendInput(this.#conn, this.#handleOf(pid), body, opts);
  }
}

/** sandbox.pty: interactive terminals, as background commands on a pseudo-terminal. */
export class Pty {
  readonly #commands: Commands;

  constructor(commands: Commands) {
    this.#commands = commands;
  }

  /** Starts a login shell on a terminal of the given size; onData receives its output. */
  async create(opts: PtyCreateOpts): Promise<CommandHandle> {
    return this.#commands.startTerminal(opts);
  }

  /** Types into the terminal. */
  async sendInput(pid: number, data: Uint8Array, opts: CommandRequestOpts = {}): Promise<void> {
    await this.#commands.input(pid, { data: base64(data) }, opts);
  }

  async resize(pid: number, size: PtySize, opts: CommandRequestOpts = {}): Promise<void> {
    await this.#commands.input(pid, { rows: size.rows, cols: size.cols }, opts);
  }

  /** Kills the terminal's shell; false when there is none. */
  async kill(pid: number, opts: CommandRequestOpts = {}): Promise<boolean> {
    return this.#commands.kill(pid, opts);
  }
}
