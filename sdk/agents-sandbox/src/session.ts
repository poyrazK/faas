// A Gregale sandbox session as an OpenAI Agents SDK SandboxSession
// (ADR-20261027090013 §8). Every call goes to the session's VM through the
// Gregale data plane, with a session token re-minted from the API key as it
// nears expiry; the token is never part of the session's state.

import { randomBytes } from 'node:crypto';
import { readFile as readLocalFile, readdir, stat as statLocal } from 'node:fs/promises';
import { join as joinLocal } from 'node:path';
import { applyDiff, type Editor, type ToolOutputImage } from '@openai/agents-core';
import {
  Manifest,
  SandboxError,
  SandboxLifecycleError,
  SandboxUnsupportedFeatureError,
  type Entry,
  type ExecCommandArgs,
  type ExposedPortEndpoint,
  type ListDirectoryArgs,
  type MaterializeEntryArgs,
  type ReadFileArgs,
  type SandboxDirectoryEntry,
  type SandboxExecResult,
  type SandboxSession,
  type SandboxSessionState,
  type ViewImageArgs,
  type WorkspaceArchiveData,
  type WriteStdinArgs,
} from '@openai/agents-core/sandbox';
import { GregaleApiError, type SandboxApi, type SandboxPortView, type SandboxView } from './api.js';
import { gzipped, tar, type TarEntry } from './tar.js';

/** The workspace every Gregale session has. */
export const WORKSPACE_ROOT = '/workspace';

/** What a Gregale session's state holds besides the SDK's own fields. */
export interface GregaleSandboxSessionState extends SandboxSessionState {
  backendId: 'gregale';
  sandboxId: string;
  apiBase: string;
  /** The manifest's environment, sent with every command. Never serialized. */
  environment?: Record<string, string>;
}

/** A server-side persisted workspace (workspace:persist). */
export type GregaleWorkspaceSnapshot = {
  snapshotId: string;
  bytes: number;
  sha256: string;
  entries: number;
  expiresAt: string;
};

export type GregaleSessionOptions = {
  /** Visibility of ports resolveExposedPort exposes; private by default. */
  portVisibility?: 'private' | 'public';
  /** How long to wait for a session to boot; 120 s by default. */
  readyTimeoutMs?: number;
};

type ExecResponse = {
  stdout: string;
  stderr: string;
  exit_code: number;
  timed_out?: boolean;
  wall_time_s: number;
};

type ExecOutput = {
  handle: string;
  running: boolean;
  exit_code?: number;
  stdout: string | null;
  stderr: string | null;
  stdout_offset: number;
  stderr_offset: number;
};

type FileInfo = { path: string; type: 'file' | 'dir' | 'symlink' | 'other'; size: number };

/** A background command the shell tool can poll by its session ID. */
type Process = { handle: string; stdoutOffset: number; stderrOffset: number; started: number };

const LIVE_STATES = new Set(['queued', 'starting', 'ready', 'frozen']);
const PLACED_STATES = new Set(['ready', 'frozen']);
const DEFAULT_YIELD_MS = 10_000;
const POLL_MS = 200;
const TOKEN_MARGIN_MS = 60_000;

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** A shell word, single-quoted. */
export function shellQuote(word: string): string {
  return `'${word.replaceAll("'", `'\\''`)}'`;
}

/** The workspace-relative path of p, or undefined when p is outside it. */
export function workspaceRelative(p: string): string | undefined {
  let rel: string;
  if (p.startsWith('/')) {
    if (p === WORKSPACE_ROOT) return '.';
    if (!p.startsWith(`${WORKSPACE_ROOT}/`)) return undefined;
    rel = p.slice(WORKSPACE_ROOT.length + 1);
  } else {
    rel = p;
  }
  const parts: string[] = [];
  for (const part of rel.split('/')) {
    if (part === '' || part === '.') continue;
    if (part === '..') {
      if (parts.length === 0) return undefined;
      parts.pop();
    } else {
      parts.push(part);
    }
  }
  return parts.length === 0 ? '.' : parts.join('/');
}

function runAsArg(runAs: string | undefined): string | undefined {
  if (runAs === undefined || runAs === '' || runAs === 'app') return undefined;
  if (runAs === 'root') return 'root';
  throw new SandboxUnsupportedFeatureError(`Gregale sandboxes run commands as "app" or "root", not "${runAs}"`, { runAs });
}

function decode(b64: string | null | undefined): string {
  return b64 ? Buffer.from(b64, 'base64').toString('utf8') : '';
}

/** Output shortened to about maxTokens tokens, keeping its head and tail. */
function truncate(output: string, maxTokens?: number): { text: string; originalTokens?: number } {
  if (maxTokens === undefined) return { text: output };
  const bytes = Buffer.byteLength(output);
  const budget = Math.max(0, maxTokens) * 4;
  if (bytes <= budget) return { text: output };
  const half = Math.floor(budget / 2);
  const buf = Buffer.from(output);
  const head = buf.subarray(0, half).toString('utf8').replace(/�+$/, '');
  const tail = buf.subarray(bytes - half).toString('utf8').replace(/^�+/, '');
  const originalTokens = Math.ceil(bytes / 4);
  return { text: `${head}\n…${originalTokens - maxTokens} tokens truncated…\n${tail}`, originalTokens };
}

function formatExec(args: { output: string; wallSeconds: number; exitCode?: number; sessionId?: number; maxTokens?: number }): string {
  const { text, originalTokens } = truncate(args.output, args.maxTokens);
  const lines = [`Chunk ID: ${randomBytes(3).toString('hex')}`, `Wall time: ${args.wallSeconds.toFixed(4)} seconds`];
  if (args.sessionId !== undefined) lines.push(`Process running with session ID ${args.sessionId}`);
  else if (args.exitCode !== undefined) lines.push(`Process exited with code ${args.exitCode}`);
  if (originalTokens !== undefined) lines.push(`Original token count: ${originalTokens}`);
  lines.push('Output:', text);
  return lines.join('\n');
}

const MEDIA_TYPES: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
};

export class GregaleSandboxSession implements SandboxSession<GregaleSandboxSessionState> {
  readonly state: GregaleSandboxSessionState;
  readonly #api: SandboxApi;
  readonly #options: GregaleSessionOptions;
  #token?: { value: string; expires: number };
  readonly #processes = new Map<number, Process>();
  #nextProcess = 1;
  #closed = false;

  constructor(args: { state: GregaleSandboxSessionState; api: SandboxApi; options?: GregaleSessionOptions }) {
    this.state = args.state;
    this.#api = args.api;
    this.#options = args.options ?? {};
  }

  get sandboxId(): string {
    return this.state.sandboxId;
  }

  // ---- lifecycle -------------------------------------------------------

  /** Reads the session, keeping the token its read minted. */
  async refresh(): Promise<SandboxView> {
    const view = await this.#api.control<SandboxView>('GET', `/v1/sandboxes/${this.sandboxId}`);
    if (view.token && view.token_expires_at) {
      this.#token = { value: view.token, expires: Date.parse(view.token_expires_at) };
    } else {
      this.#token = undefined;
    }
    return view;
  }

  /** Waits until the session runs on a node, or fails if it ended. */
  async waitReady(): Promise<void> {
    const deadline = Date.now() + (this.#options.readyTimeoutMs ?? 120_000);
    let delay = 250;
    for (;;) {
      const view = await this.refresh();
      if (!LIVE_STATES.has(view.state) || view.close_requested_at) {
        throw new SandboxLifecycleError(
          `Gregale sandbox ${this.sandboxId} is ${view.state}${view.outcome ? `: ${view.outcome.code}${view.outcome.message ? ` (${view.outcome.message})` : ''}` : ''}`,
          { sandboxId: this.sandboxId, state: view.state },
        );
      }
      if (PLACED_STATES.has(view.state) && this.#token) return;
      if (Date.now() >= deadline) {
        throw new SandboxLifecycleError(`Gregale sandbox ${this.sandboxId} did not start within ${this.#options.readyTimeoutMs ?? 120_000} ms`, {
          sandboxId: this.sandboxId,
          state: view.state,
        });
      }
      await sleep(delay);
      delay = Math.min(delay * 2, 2_000);
    }
  }

  async running(): Promise<boolean> {
    if (this.#closed) return false;
    const view = await this.refresh();
    return LIVE_STATES.has(view.state) && !view.close_requested_at;
  }

  /** Closes the session; its VM and workspace go with it. */
  async delete(): Promise<void> {
    if (this.#closed) return;
    this.#closed = true;
    this.#processes.clear();
    try {
      await this.#api.control('DELETE', `/v1/sandboxes/${this.sandboxId}`);
    } catch (err) {
      if (!(err instanceof GregaleApiError && err.status === 404)) throw err;
    }
  }

  async stop(): Promise<void> {
    await this.delete();
  }

  async shutdown(): Promise<void> {
    await this.delete();
  }

  async close(): Promise<void> {
    await this.delete();
  }

  // ---- data plane ------------------------------------------------------

  async #currentToken(): Promise<string> {
    if (this.#closed) {
      throw new SandboxLifecycleError(`Gregale sandbox ${this.sandboxId} is closed`, { sandboxId: this.sandboxId });
    }
    if (!this.#token || this.#token.expires - TOKEN_MARGIN_MS <= Date.now()) {
      await this.waitReady();
    }
    return this.#token!.value;
  }

  /**
   * A data-plane call. A token the data plane no longer takes (expired, or
   * minted before the session was placed) is re-minted once.
   */
  async #call(method: string, path: string, args: Parameters<SandboxApi['data']>[3] = {}): Promise<Response> {
    const url = `/v1/sandboxes/${this.sandboxId}${path}`;
    try {
      return await this.#api.data(method, url, await this.#currentToken(), args);
    } catch (err) {
      const stale = err instanceof GregaleApiError && ((err.status === 404 && err.code === '') || err.code === 'sandbox_not_ready');
      if (!stale) throw err;
      this.#token = undefined;
      return await this.#api.data(method, url, await this.#currentToken(), args);
    }
  }

  async #json<T>(method: string, path: string, args: Parameters<SandboxApi['data']>[3] = {}): Promise<T> {
    return (await (await this.#call(method, path, args)).json()) as T;
  }

  // ---- commands --------------------------------------------------------

  #argv(args: ExecCommandArgs): string[] {
    if (args.tty) {
      throw new SandboxUnsupportedFeatureError('Gregale sandboxes do not attach a terminal to commands yet; run it without tty', {});
    }
    return [args.shell ?? '/bin/sh', args.login ? '-lc' : '-c', args.cmd];
  }

  #execBody(args: ExecCommandArgs, background: boolean) {
    return {
      cmd: this.#argv(args),
      workdir: args.workdir,
      env: this.state.environment && Object.keys(this.state.environment).length > 0 ? this.state.environment : undefined,
      run_as: runAsArg(args.runAs),
      background: background || undefined,
    };
  }

  /** Runs a command to completion. */
  async exec(args: ExecCommandArgs): Promise<SandboxExecResult> {
    const res = await this.#json<ExecResponse>('POST', '/exec', { json: this.#execBody(args, false) });
    const { text, originalTokens } = truncate(res.stdout + res.stderr, args.maxOutputTokens);
    return {
      output: text,
      stdout: res.stdout,
      stderr: res.stderr,
      wallTimeSeconds: res.wall_time_s,
      exitCode: res.exit_code,
      ...(originalTokens !== undefined ? { originalTokenCount: originalTokens } : {}),
    };
  }

  /**
   * Runs a command for up to yieldTimeMs. A command still running then keeps
   * running in the background under a session ID that writeStdin polls.
   */
  async execCommand(args: ExecCommandArgs): Promise<string> {
    const started = Date.now();
    const { handle } = await this.#json<{ handle: string }>('POST', '/exec', { json: this.#execBody(args, true) });
    const proc: Process = { handle, stdoutOffset: 0, stderrOffset: 0, started };
    const { output, exitCode } = await this.#poll(proc, args.yieldTimeMs ?? DEFAULT_YIELD_MS);
    const wallSeconds = (Date.now() - started) / 1000;
    if (exitCode !== undefined) {
      return formatExec({ output, wallSeconds, exitCode, maxTokens: args.maxOutputTokens });
    }
    const sessionId = this.#nextProcess++;
    this.#processes.set(sessionId, proc);
    return formatExec({ output, wallSeconds, sessionId, maxTokens: args.maxOutputTokens });
  }

  /** Polls a background command; Gregale commands take no stdin yet. */
  async writeStdin(args: WriteStdinArgs): Promise<string> {
    const proc = this.#processes.get(args.sessionId);
    if (!proc) {
      throw new SandboxError(`no running command has session ID ${args.sessionId}`, 'pty_session_not_found', { sessionId: args.sessionId });
    }
    if (args.chars) {
      throw new SandboxUnsupportedFeatureError(
        'Gregale sandbox commands do not take input yet: call write_stdin with empty chars to read output, or run the command non-interactively',
        { sessionId: args.sessionId },
      );
    }
    const started = Date.now();
    const { output, exitCode } = await this.#poll(proc, args.yieldTimeMs ?? 250);
    const wallSeconds = (Date.now() - started) / 1000;
    if (exitCode !== undefined) {
      this.#processes.delete(args.sessionId);
      return formatExec({ output, wallSeconds, exitCode, maxTokens: args.maxOutputTokens });
    }
    return formatExec({ output, wallSeconds, sessionId: args.sessionId, maxTokens: args.maxOutputTokens });
  }

  /** Output since the last read, until the command ends or yieldMs passes. */
  async #poll(proc: Process, yieldMs: number): Promise<{ output: string; exitCode?: number }> {
    const deadline = Date.now() + yieldMs;
    let output = '';
    for (;;) {
      const out = await this.#json<ExecOutput>('GET', `/exec/${proc.handle}`, {
        query: { stdout_offset: proc.stdoutOffset, stderr_offset: proc.stderrOffset },
      });
      const stdout = decode(out.stdout);
      const stderr = decode(out.stderr);
      output += stdout + stderr;
      const progressed = out.stdout_offset !== proc.stdoutOffset || out.stderr_offset !== proc.stderrOffset;
      proc.stdoutOffset = out.stdout_offset;
      proc.stderrOffset = out.stderr_offset;
      if (!out.running) {
        // Read on until the ended command's output is drained.
        if (progressed) continue;
        return { output, exitCode: out.exit_code ?? 0 };
      }
      const left = deadline - Date.now();
      if (left <= 0) return { output };
      await sleep(Math.min(POLL_MS, left));
    }
  }

  supportsPty(): boolean {
    // The shell capability offers write_stdin only to sessions with a PTY;
    // Gregale's answers polls of background commands and refuses input.
    return true;
  }

  // ---- files -----------------------------------------------------------

  async #stat(path: string): Promise<FileInfo | undefined> {
    const rel = workspaceRelative(path);
    if (rel === undefined) {
      const res = await this.exec({ cmd: `if [ -d ${shellQuote(path)} ]; then echo dir; elif [ -e ${shellQuote(path)} ]; then echo file; fi` });
      const kind = res.stdout.trim();
      return kind === 'dir' || kind === 'file' ? { path, type: kind, size: 0 } : undefined;
    }
    try {
      return await this.#json<FileInfo>('GET', '/files:stat', { query: { path: rel } });
    } catch (err) {
      if (err instanceof GregaleApiError && err.code === 'sandbox_entry_not_found') return undefined;
      throw err;
    }
  }

  async pathExists(path: string): Promise<boolean> {
    return (await this.#stat(path)) !== undefined;
  }

  async directoryExists(path: string): Promise<boolean> {
    return (await this.#stat(path))?.type === 'dir';
  }

  async readFile(args: ReadFileArgs): Promise<Uint8Array> {
    const rel = workspaceRelative(args.path);
    let data: Uint8Array;
    if (rel === undefined) {
      const res = await this.exec({ cmd: `base64 < ${shellQuote(args.path)}`, runAs: args.runAs });
      if (res.exitCode !== 0) {
        throw new SandboxError(`cannot read ${args.path}: ${res.stderr.trim()}`, 'workspace_read_not_found', { path: args.path });
      }
      data = new Uint8Array(Buffer.from(res.stdout.replace(/\s+/g, ''), 'base64'));
    } else {
      try {
        data = new Uint8Array(await (await this.#call('GET', '/files', { query: { path: rel }, accept: 'application/octet-stream' })).arrayBuffer());
      } catch (err) {
        if (err instanceof GregaleApiError && err.code === 'sandbox_entry_not_found') {
          throw new SandboxError(`no such file: ${args.path}`, 'workspace_read_not_found', { path: args.path });
        }
        throw err;
      }
    }
    if (args.maxBytes !== undefined && data.length > args.maxBytes) {
      throw new SandboxError(`${args.path} is ${data.length} bytes, over maxBytes ${args.maxBytes}`, 'workspace_archive_read_error', {
        path: args.path,
        bytes: data.length,
      });
    }
    return data;
  }

  /** Replaces a workspace file, creating its parent directories. */
  async writeFile(path: string, data: string | Uint8Array): Promise<void> {
    const rel = workspaceRelative(path);
    if (rel === undefined || rel === '.') {
      throw new SandboxError(`${path} is outside ${WORKSPACE_ROOT}`, 'path_resolution_error', { path });
    }
    const body = typeof data === 'string' ? new TextEncoder().encode(data) : data;
    await this.#call('PUT', '/files', { query: { path: rel, parents: true }, body });
  }

  async #remove(path: string): Promise<void> {
    const rel = workspaceRelative(path);
    if (rel === undefined || rel === '.') {
      throw new SandboxError(`${path} is outside ${WORKSPACE_ROOT}`, 'path_resolution_error', { path });
    }
    await this.#call('POST', '/files:remove', { json: { path: rel } });
  }

  async listDir(args: ListDirectoryArgs): Promise<SandboxDirectoryEntry[]> {
    const rel = workspaceRelative(args.path);
    if (rel === undefined) {
      throw new SandboxUnsupportedFeatureError(`Gregale lists directories inside ${WORKSPACE_ROOT} only; use a command for ${args.path}`, {
        path: args.path,
      });
    }
    const list = await this.#json<{ entries: FileInfo[] }>('GET', '/files:list', { query: { path: rel, depth: 1 } });
    return list.entries
      .filter((e) => e.path !== rel && e.path !== '.')
      .map((e) => ({
        name: e.path.slice(e.path.lastIndexOf('/') + 1),
        path: `${WORKSPACE_ROOT}/${e.path}`,
        type: e.type === 'file' || e.type === 'dir' ? e.type : 'other',
      }));
  }

  async viewImage(args: ViewImageArgs): Promise<ToolOutputImage> {
    const data = await this.readFile({ path: args.path, runAs: args.runAs, maxBytes: 20 << 20 });
    const ext = args.path.slice(args.path.lastIndexOf('.') + 1).toLowerCase();
    return { type: 'image', image: { data: Buffer.from(data).toString('base64'), mediaType: MEDIA_TYPES[ext] ?? 'image/png' } };
  }

  createEditor(runAs?: string): Editor {
    runAsArg(runAs);
    return {
      createFile: async (op) => {
        await this.writeFile(op.path, applyDiff('', op.diff, 'create'));
        return { status: 'completed', output: `Created ${op.path}` };
      },
      updateFile: async (op) => {
        const before = new TextDecoder().decode(await this.readFile({ path: op.path }));
        const target = op.moveTo ?? op.path;
        await this.writeFile(target, applyDiff(before, op.diff));
        if (op.moveTo && workspaceRelative(op.moveTo) !== workspaceRelative(op.path)) await this.#remove(op.path);
        return { status: 'completed', output: op.moveTo ? `Updated ${op.path} and moved it to ${op.moveTo}` : `Updated ${op.path}` };
      },
      deleteFile: async (op) => {
        await this.#remove(op.path);
        return { status: 'completed', output: `Deleted ${op.path}` };
      },
    };
  }

  // ---- manifest --------------------------------------------------------

  async materializeEntry(args: MaterializeEntryArgs): Promise<void> {
    await this.#materialize([[args.path, args.entry]]);
  }

  async applyManifest(manifest: Manifest): Promise<void> {
    validateManifest(manifest);
    await this.#materialize(Object.entries(manifest.validatedEntries()));
    this.state.manifest = manifest;
  }

  async #materialize(entries: [string, Entry][]): Promise<void> {
    const files: TarEntry[] = [];
    const repos: { path: string; entry: Extract<Entry, { type: 'git_repo' }> }[] = [];
    for (const [path, entry] of entries) {
      await collect(path, entry, files, repos);
    }
    if (files.length > 0) await this.hydrateWorkspace(tar(files));
    for (const repo of repos) await this.#clone(repo.path, repo.entry);
  }

  async #clone(path: string, entry: Extract<Entry, { type: 'git_repo' }>): Promise<void> {
    const rel = entryPath(path);
    const host = entry.host ?? 'github.com';
    const url = /^[a-z]+:\/\//.test(entry.repo) ? entry.repo : `https://${host}/${entry.repo.replace(/\.git$/, '')}.git`;
    const dest = `${WORKSPACE_ROOT}/${rel}`;
    const checkout = entry.subpath ? `/tmp/gregale-git-${randomBytes(4).toString('hex')}` : dest;
    const script = [
      'set -e',
      `mkdir -p ${shellQuote(checkout)}`,
      `cd ${shellQuote(checkout)}`,
      'git init -q',
      `git remote add origin ${shellQuote(url)}`,
      `git fetch -q --depth 1 origin ${shellQuote(entry.ref ?? 'HEAD')}`,
      'git checkout -q FETCH_HEAD',
      ...(entry.subpath
        ? [`mkdir -p ${shellQuote(dest)}`, `cp -a ${shellQuote(`${checkout}/${entry.subpath}`)}/. ${shellQuote(dest)}/`, `rm -rf ${shellQuote(checkout)}`]
        : []),
    ].join('\n');
    const res = await this.exec({ cmd: script });
    if (res.exitCode !== 0) {
      throw new SandboxError(`git checkout of ${entry.repo} into ${dest} failed: ${(res.stderr || res.stdout).trim()}`, 'git_clone_error', {
        repo: entry.repo,
        path: dest,
      });
    }
  }

  // ---- workspace -------------------------------------------------------

  /** The workspace as a tar archive, built in the guest and downloaded. */
  async persistWorkspace(): Promise<Uint8Array> {
    const name = `.gregale-persist-${randomBytes(6).toString('hex')}.tar`;
    const res = await this.exec({ cmd: `tar -C ${WORKSPACE_ROOT} --exclude=./${name} -cf ${WORKSPACE_ROOT}/${name} .` });
    try {
      if (res.exitCode !== 0) {
        throw new SandboxError(`archiving the workspace failed: ${res.stderr.trim()}`, 'workspace_archive_read_error', {});
      }
      return await this.readFile({ path: `${WORKSPACE_ROOT}/${name}` });
    } finally {
      await this.#remove(name).catch(() => undefined);
    }
  }

  /** Unpacks a tar (or tar.gz) archive into the workspace. */
  async hydrateWorkspace(data: WorkspaceArchiveData): Promise<void> {
    const bytes =
      typeof data === 'string' ? new Uint8Array(Buffer.from(data, 'latin1')) : data instanceof Uint8Array ? data : new Uint8Array(data);
    await this.#call('POST', '/workspace:hydrate', { body: gzipped(bytes), contentType: 'application/gzip' });
  }

  /**
   * Persists the workspace on Gregale's side. A later create with
   * `snapshotId` starts from it; nothing is downloaded.
   */
  async persistSnapshot(path?: string): Promise<GregaleWorkspaceSnapshot> {
    const res = await this.#json<{ snapshot_id: string; bytes: number; sha256: string; entries: number; expires_at: string }>(
      'POST',
      '/workspace:persist',
      { json: path ? { path } : {} },
    );
    return { snapshotId: res.snapshot_id, bytes: res.bytes, sha256: res.sha256, entries: res.entries, expiresAt: res.expires_at };
  }

  // ---- ports -----------------------------------------------------------

  /**
   * Exposes a guest port at its preview URL. A private port's endpoint
   * carries its preview token in `query`; it is not kept in the session's
   * state, so a serialized run never holds it.
   */
  async resolveExposedPort(port: number): Promise<ExposedPortEndpoint> {
    const view = await this.#api.control<SandboxPortView>('POST', `/v1/sandboxes/${this.sandboxId}/ports`, {
      json: { port, visibility: this.#options.portVisibility ?? 'private' },
    });
    const url = new URL(view.url);
    return {
      host: url.hostname,
      port: 443,
      tls: true,
      url: view.url,
      ...(view.preview_token ? { query: `gregale_preview_token=${encodeURIComponent(view.preview_token)}`, token: view.preview_token } : {}),
      ...(view.preview_token_expires_at ? { tokenExpiresAt: view.preview_token_expires_at } : {}),
    };
  }
}

// ---- manifest entries ----------------------------------------------------

/** Refuses what a Gregale session cannot do, naming the field or entry. */
export function validateManifest(manifest: Manifest): void {
  if (manifest.root !== WORKSPACE_ROOT) {
    throw new SandboxUnsupportedFeatureError(`Gregale sessions have their workspace at ${WORKSPACE_ROOT}; the manifest's root is ${manifest.root}`, {
      root: manifest.root,
    });
  }
  for (const [field, list] of [
    ['users', manifest.users],
    ['groups', manifest.groups],
    ['extraPathGrants', manifest.extraPathGrants],
  ] as const) {
    if (list.length > 0) {
      throw new SandboxUnsupportedFeatureError(`Gregale sessions do not support manifest ${field} yet`, { field });
    }
  }
  const check = (path: string, entry: Entry) => {
    switch (entry.type) {
      case 'file':
      case 'local_file':
      case 'local_dir':
      case 'git_repo':
        break;
      case 'dir':
        for (const [name, child] of Object.entries(entry.children ?? {})) check(`${path}/${name}`, child);
        break;
      default:
        throw new SandboxUnsupportedFeatureError(`Gregale sessions do not support ${entry.type} entries (manifest entry "${path}")`, {
          path,
          type: entry.type,
        });
    }
    if ('group' in entry && entry.group !== undefined) {
      throw new SandboxUnsupportedFeatureError(`Gregale sessions do not set entry groups (manifest entry "${path}")`, { path });
    }
  };
  for (const [path, entry] of Object.entries(manifest.entries)) check(path, entry);
}

function entryPath(path: string): string {
  const rel = workspaceRelative(path);
  if (rel === undefined || rel === '.') {
    throw new SandboxError(`manifest entry "${path}" is outside ${WORKSPACE_ROOT}`, 'invalid_manifest_path', { path });
  }
  return rel;
}

async function collect(
  path: string,
  entry: Entry,
  files: TarEntry[],
  repos: { path: string; entry: Extract<Entry, { type: 'git_repo' }> }[],
): Promise<void> {
  const rel = entryPath(path);
  switch (entry.type) {
    case 'file':
      files.push({ type: 'file', path: rel, data: typeof entry.content === 'string' ? new TextEncoder().encode(entry.content) : entry.content });
      return;
    case 'dir':
      files.push({ type: 'dir', path: rel });
      for (const [name, child] of Object.entries(entry.children ?? {})) await collect(`${rel}/${name}`, child, files, repos);
      return;
    case 'local_file':
      files.push({ type: 'file', path: rel, data: new Uint8Array(await readLocalFile(entry.src)), mode: (await statLocal(entry.src)).mode & 0o777 });
      return;
    case 'local_dir':
      await collectLocalDir(entry.src, rel, files);
      return;
    case 'git_repo':
      repos.push({ path: rel, entry });
      return;
    default:
      throw new SandboxUnsupportedFeatureError(`Gregale sessions do not support ${entry.type} entries (manifest entry "${path}")`, { path });
  }
}

async function collectLocalDir(src: string, rel: string, files: TarEntry[]): Promise<void> {
  files.push({ type: 'dir', path: rel });
  for (const dirent of await readdir(src, { withFileTypes: true })) {
    const from = joinLocal(src, dirent.name);
    const to = `${rel}/${dirent.name}`;
    if (dirent.isDirectory()) {
      await collectLocalDir(from, to, files);
    } else if (dirent.isFile()) {
      files.push({ type: 'file', path: to, data: new Uint8Array(await readLocalFile(from)), mode: (await statLocal(from)).mode & 0o777 });
    }
    // Symlinks and special files on the developer's machine are not copied.
  }
}
