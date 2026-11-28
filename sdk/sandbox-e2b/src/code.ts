// The code interpreter (ADR-20261027090013 step 14): runCode and code
// contexts, with E2B Code Interpreter's names. A session created with
// template 'code-interpreter' runs code in Jupyter kernels; each context is
// its own kernel, so variables and imports persist between runs.

import { type Connection } from './connection.js';
import { UnsupportedError } from './errors.js';

/** A rich output of a run: a display, or the run's value (isMainResult). */
export class Result {
  readonly text?: string;
  readonly html?: string;
  readonly markdown?: string;
  readonly svg?: string;
  /** Base64 PNG. */
  readonly png?: string;
  /** Base64 JPEG. */
  readonly jpeg?: string;
  /** Base64 PDF. */
  readonly pdf?: string;
  readonly latex?: string;
  readonly json?: unknown;
  readonly javascript?: string;
  readonly isMainResult: boolean;
  /** Every MIME type the output came in, as sent. */
  readonly raw: Record<string, unknown>;

  constructor(data: Record<string, unknown>, isMainResult: boolean) {
    const str = (k: string) => (typeof data[k] === 'string' ? (data[k] as string) : undefined);
    this.text = str('text/plain');
    this.html = str('text/html');
    this.markdown = str('text/markdown');
    this.svg = str('image/svg+xml');
    this.png = str('image/png');
    this.jpeg = str('image/jpeg');
    this.pdf = str('application/pdf');
    this.latex = str('text/latex');
    this.json = data['application/json'];
    this.javascript = str('application/javascript');
    this.isMainResult = isMainResult;
    this.raw = data;
  }

  /** The formats this result is available in. */
  formats(): string[] {
    const names: [string, unknown][] = [
      ['text', this.text],
      ['html', this.html],
      ['markdown', this.markdown],
      ['svg', this.svg],
      ['png', this.png],
      ['jpeg', this.jpeg],
      ['pdf', this.pdf],
      ['latex', this.latex],
      ['json', this.json],
      ['javascript', this.javascript],
    ];
    return names.filter(([, v]) => v !== undefined).map(([n]) => n);
  }
}

/** The exception a run raised. */
export type ExecutionError = { name: string; value: string; traceback: string };

/** A chunk of a run's stdout or stderr, as E2B's onStdout and onStderr receive it. */
export type OutputMessage = { line: string; timestamp: number; error: boolean };

export type Logs = { stdout: string[]; stderr: string[] };

/** What a run produced. */
export class Execution {
  constructor(
    readonly results: Result[] = [],
    readonly logs: Logs = { stdout: [], stderr: [] },
    readonly error?: ExecutionError,
    readonly executionCount?: number,
  ) {}

  /** The text of the run's value, when it had one. */
  get text(): string | undefined {
    return this.results.find((r) => r.isMainResult)?.text;
  }
}

/** A code context: its own kernel, language and working directory. */
export type Context = { id: string; language: string; cwd: string };

export type RunCodeOpts = {
  /** Only "python"; the default context when no context is given. */
  language?: string;
  context?: Context | string;
  onStdout?: (msg: OutputMessage) => void | Promise<void>;
  onStderr?: (msg: OutputMessage) => void | Promise<void>;
  onResult?: (result: Result) => void | Promise<void>;
  onError?: (error: ExecutionError) => void | Promise<void>;
  /** Not supported: set the session's environment with setEnvs. */
  envs?: Record<string, string>;
  /** How long the run may take; 60 s by default, within the plan's cap. */
  timeoutMs?: number;
  signal?: AbortSignal;
};

type WireOutput = { data: Record<string, unknown>; is_main_result?: boolean };
type WireExecution = {
  results: WireOutput[];
  logs: Logs;
  error?: ExecutionError;
  execution_count?: number;
};

function contextId(ctx: Context | string): string {
  return typeof ctx === 'string' ? ctx : ctx.id;
}

function execution(w: WireExecution, logs?: Logs): Execution {
  return new Execution(
    w.results.map((r) => new Result(r.data, r.is_main_result ?? false)),
    logs ?? w.logs,
    w.error,
    w.execution_count,
  );
}

export class CodeInterpreter {
  readonly #conn: Connection;

  constructor(conn: Connection) {
    this.#conn = conn;
  }

  async runCode(code: string, opts: RunCodeOpts = {}): Promise<Execution> {
    if (opts.envs && Object.keys(opts.envs).length > 0) {
      throw new UnsupportedError('runCode({ envs })', 'set the session environment with sandbox.setEnvs before the first run');
    }
    const body = {
      code,
      context: opts.context ? contextId(opts.context) : undefined,
      language: opts.language,
      timeout_ms: opts.timeoutMs || undefined,
    };
    const streaming = opts.onStdout || opts.onStderr;
    const exec = streaming ? await this.#stream(body, opts) : execution(await this.#conn.json<WireExecution>('POST', '/code', { json: body, signal: opts.signal }));
    for (const r of exec.results) await opts.onResult?.(r);
    if (exec.error) await opts.onError?.(exec.error);
    return exec;
  }

  async #stream(body: Record<string, unknown>, opts: RunCodeOpts): Promise<Execution> {
    const res = await this.#conn.call('POST', '/code', { json: body, signal: opts.signal, accept: 'text/event-stream' });
    const logs: Logs = { stdout: [], stderr: [] };
    let final: WireExecution | undefined;
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
        if (event === 'stdout' || event === 'stderr') {
          logs[event].push(value as string);
          const msg: OutputMessage = { line: value as string, timestamp: Date.now() * 1000, error: event === 'stderr' };
          await (event === 'stdout' ? opts.onStdout : opts.onStderr)?.(msg);
        } else if (event === 'result') {
          final = value as WireExecution;
        } else if (event === 'error') {
          throw new Error((value as { detail?: string }).detail ?? 'the run failed');
        }
      }
    }
    if (!final) throw new Error('the run ended without a result');
    return execution(final, logs);
  }

  async createCodeContext(opts: { cwd?: string; language?: string; signal?: AbortSignal } = {}): Promise<Context> {
    return this.#conn.json<Context>('POST', '/code/contexts', { json: { cwd: opts.cwd, language: opts.language }, signal: opts.signal });
  }

  async listCodeContexts(opts: { signal?: AbortSignal } = {}): Promise<Context[]> {
    return (await this.#conn.json<{ contexts: Context[] }>('GET', '/code/contexts', { signal: opts.signal })).contexts;
  }

  /** Restarts a context's kernel; its variables and imports are lost. */
  async restartCodeContext(ctx: Context | string, opts: { signal?: AbortSignal } = {}): Promise<void> {
    await this.#conn.call('POST', `/code/contexts/${contextId(ctx)}/restart`, { signal: opts.signal });
  }

  async removeCodeContext(ctx: Context | string, opts: { signal?: AbortSignal } = {}): Promise<void> {
    await this.#conn.call('DELETE', `/code/contexts/${contextId(ctx)}`, { signal: opts.signal });
  }
}
