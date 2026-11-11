// sandbox.files: the session's files. Paths inside /workspace, and relative
// paths (which are relative to /workspace), use Gregale's files API; other
// absolute paths go through a command and are capped at OUTSIDE_MAX_BYTES.

import { type Connection } from './connection.js';
import { InvalidArgumentError, NotFoundError, SandboxError, UnsupportedError } from './errors.js';

export const WORKSPACE = '/workspace';

/** Files outside /workspace are read and written through commands, up to this size. */
export const OUTSIDE_MAX_BYTES = 256 << 10;

export enum FileType {
  FILE = 'file',
  DIR = 'dir',
  SYMLINK = 'symlink',
}

export type WriteInfo = { name: string; type?: FileType; path: string };

export type EntryInfo = WriteInfo & {
  size: number;
  mode: number;
  permissions: string;
  owner: string;
  group: string;
  modifiedTime?: Date;
  symlinkTarget?: string;
};

export type FilesystemRequestOpts = { requestTimeoutMs?: number; signal?: AbortSignal; user?: string };
export type FilesystemListOpts = FilesystemRequestOpts & { depth?: number };
export type WriteData = string | ArrayBuffer | Uint8Array | Blob | ReadableStream<Uint8Array>;
export type WriteEntry = { path: string; data: WriteData };

type FileInfo = { path: string; type: string; size: number; mode: number; mtime_unix_ms: number; link_target?: string };

/** The workspace-relative form of p, or undefined when p is outside it. */
export function workspaceRelative(p: string): string | undefined {
  let rel = p;
  if (p.startsWith('/')) {
    if (p === WORKSPACE) return '.';
    if (!p.startsWith(`${WORKSPACE}/`)) return undefined;
    rel = p.slice(WORKSPACE.length + 1);
  }
  const parts: string[] = [];
  for (const part of rel.split('/')) {
    if (part === '' || part === '.') continue;
    if (part === '..') {
      if (parts.length === 0) return undefined;
      parts.pop();
    } else parts.push(part);
  }
  return parts.length === 0 ? '.' : parts.join('/');
}

const quote = (s: string) => `'${s.replaceAll("'", `'\\''`)}'`;

function permissions(mode: number, type: string): string {
  const bits = 'rwxrwxrwx';
  let out = type === 'dir' ? 'd' : type === 'symlink' ? 'l' : '-';
  for (let i = 0; i < 9; i++) out += mode & (1 << (8 - i)) ? bits[i] : '-';
  return out;
}

async function bytesOf(data: WriteData): Promise<Uint8Array> {
  if (typeof data === 'string') return new TextEncoder().encode(data);
  if (data instanceof Uint8Array) return data;
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (typeof Blob !== 'undefined' && data instanceof Blob) return new Uint8Array(await data.arrayBuffer());
  return new Uint8Array(await new Response(data as ReadableStream<Uint8Array>).arrayBuffer());
}

export class Filesystem {
  readonly #conn: Connection;

  constructor(conn: Connection) {
    this.#conn = conn;
  }

  #entry(info: FileInfo): EntryInfo {
    const path = info.path === '.' ? WORKSPACE : `${WORKSPACE}/${info.path}`;
    const type = info.type === 'dir' ? FileType.DIR : info.type === 'symlink' ? FileType.SYMLINK : FileType.FILE;
    return {
      name: path.slice(path.lastIndexOf('/') + 1),
      path,
      type,
      size: info.size,
      mode: info.mode,
      permissions: permissions(info.mode, info.type),
      owner: 'user',
      group: 'user',
      modifiedTime: new Date(info.mtime_unix_ms),
      ...(info.link_target ? { symlinkTarget: info.link_target } : {}),
    };
  }

  /** Runs a shell command for a path outside /workspace and answers its stdout. */
  async #outside(script: string, opts: FilesystemRequestOpts, call: string): Promise<string> {
    if (opts.user !== undefined && opts.user !== 'root' && opts.user !== 'user') {
      throw new UnsupportedError(`${call} as "${opts.user}"`, 'Gregale sandboxes act as "user" or "root"');
    }
    const res = await this.#conn.json<{ stdout: string; stderr: string; exit_code: number }>('POST', '/exec', {
      json: { cmd: ['/bin/sh', '-c', script], run_as: opts.user === 'root' ? 'root' : undefined },
      signal: opts.signal,
    });
    if (res.exit_code === 2) throw new NotFoundError(`${call}: ${res.stderr.trim() || 'no such file or directory'}`);
    if (res.exit_code !== 0) throw new SandboxError(`${call}: ${res.stderr.trim()}`);
    return res.stdout;
  }

  /** Reads a file as text (the default), bytes, a Blob or a stream. */
  read(path: string, opts?: FilesystemRequestOpts & { format?: 'text' }): Promise<string>;
  read(path: string, opts: FilesystemRequestOpts & { format: 'bytes' }): Promise<Uint8Array>;
  read(path: string, opts: FilesystemRequestOpts & { format: 'blob' }): Promise<Blob>;
  read(path: string, opts: FilesystemRequestOpts & { format: 'stream' }): Promise<ReadableStream<Uint8Array>>;
  async read(
    path: string,
    opts: FilesystemRequestOpts & { format?: 'text' | 'bytes' | 'blob' | 'stream' } = {},
  ): Promise<string | Uint8Array | Blob | ReadableStream<Uint8Array>> {
    const rel = workspaceRelative(path);
    let res: Response;
    if (rel === undefined) {
      const b64 = await this.#outside(
        `[ -e ${quote(path)} ] || { echo "no such file: ${path.replaceAll('"', '')}" >&2; exit 2; }; ` +
          `[ "$(wc -c < ${quote(path)})" -le ${OUTSIDE_MAX_BYTES} ] || { echo "larger than ${OUTSIDE_MAX_BYTES} bytes outside ${WORKSPACE}" >&2; exit 1; }; ` +
          `base64 < ${quote(path)}`,
        opts,
        `files.read(${path})`,
      );
      res = new Response(Buffer.from(b64.replace(/\s+/g, ''), 'base64'));
    } else {
      res = await this.#conn.call('GET', '/files', { query: { path: rel }, accept: 'application/octet-stream', signal: opts.signal });
    }
    switch (opts.format ?? 'text') {
      case 'bytes':
        return new Uint8Array(await res.arrayBuffer());
      case 'blob':
        return await res.blob();
      case 'stream':
        return res.body ?? new Response(new Uint8Array()).body!;
      default:
        return await res.text();
    }
  }

  /** Writes one file, or several; parent directories are created. */
  write(path: string, data: WriteData, opts?: FilesystemRequestOpts): Promise<WriteInfo>;
  write(files: WriteEntry[], opts?: FilesystemRequestOpts): Promise<WriteInfo[]>;
  async write(pathOrFiles: string | WriteEntry[], dataOrOpts?: WriteData | FilesystemRequestOpts, maybeOpts?: FilesystemRequestOpts) {
    if (Array.isArray(pathOrFiles)) {
      const opts = (dataOrOpts ?? {}) as FilesystemRequestOpts;
      const out: WriteInfo[] = [];
      for (const f of pathOrFiles) out.push(await this.#writeOne(f.path, f.data, opts));
      return out;
    }
    return await this.#writeOne(pathOrFiles, dataOrOpts as WriteData, maybeOpts ?? {});
  }

  async writeFiles(files: WriteEntry[], opts: FilesystemRequestOpts = {}): Promise<WriteInfo[]> {
    return await this.write(files, opts);
  }

  async #writeOne(path: string, data: WriteData, opts: FilesystemRequestOpts): Promise<WriteInfo> {
    const bytes = await bytesOf(data);
    const rel = workspaceRelative(path);
    if (rel === '.') throw new InvalidArgumentError(`${path} is a directory`);
    if (rel === undefined) {
      if (bytes.length > OUTSIDE_MAX_BYTES) {
        throw new UnsupportedError(`files.write(${path}) of ${bytes.length} bytes`, `write files over ${OUTSIDE_MAX_BYTES} bytes inside ${WORKSPACE}`);
      }
      const dir = path.slice(0, path.lastIndexOf('/')) || '/';
      await this.#outside(
        `mkdir -p ${quote(dir)} && printf %s ${quote(Buffer.from(bytes).toString('base64'))} | base64 -d > ${quote(path)}`,
        opts,
        `files.write(${path})`,
      );
      return { name: path.slice(path.lastIndexOf('/') + 1), path, type: FileType.FILE };
    }
    await this.#conn.call('PUT', '/files', { query: { path: rel, parents: true }, body: bytes, signal: opts.signal });
    const abs = `${WORKSPACE}/${rel}`;
    return { name: abs.slice(abs.lastIndexOf('/') + 1), path: abs, type: FileType.FILE };
  }

  async list(path: string, opts: FilesystemListOpts = {}): Promise<EntryInfo[]> {
    const rel = this.#inside(path, 'files.list');
    const res = await this.#conn.json<{ entries: FileInfo[] }>('GET', '/files:list', { query: { path: rel, depth: opts.depth ?? 1 }, signal: opts.signal });
    return res.entries.filter((e) => e.path !== rel).map((e) => this.#entry(e));
  }

  async exists(path: string, opts: FilesystemRequestOpts = {}): Promise<boolean> {
    if (workspaceRelative(path) === undefined) {
      return (await this.#outside(`[ -e ${quote(path)} ] && echo yes || true`, opts, `files.exists(${path})`)).trim() === 'yes';
    }
    try {
      await this.getInfo(path, opts);
      return true;
    } catch (err) {
      if (err instanceof NotFoundError) return false;
      throw err;
    }
  }

  async getInfo(path: string, opts: FilesystemRequestOpts = {}): Promise<EntryInfo> {
    const rel = this.#inside(path, 'files.getInfo');
    return this.#entry(await this.#conn.json<FileInfo>('GET', '/files:stat', { query: { path: rel }, signal: opts.signal }));
  }

  /** Creates a directory and its parents; false when it already existed. */
  async makeDir(path: string, opts: FilesystemRequestOpts = {}): Promise<boolean> {
    if (workspaceRelative(path) === undefined) {
      const out = await this.#outside(`[ -d ${quote(path)} ] && echo existed || mkdir -p ${quote(path)}`, opts, `files.makeDir(${path})`);
      return out.trim() !== 'existed';
    }
    if (await this.exists(path, opts)) return false;
    await this.#conn.call('POST', '/files:mkdir', { json: { path: this.#inside(path, 'files.makeDir'), parents: true }, signal: opts.signal });
    return true;
  }

  async rename(oldPath: string, newPath: string, opts: FilesystemRequestOpts = {}): Promise<EntryInfo> {
    const from = this.#inside(oldPath, 'files.rename');
    const to = this.#inside(newPath, 'files.rename');
    await this.#conn.call('POST', '/files:rename', { json: { from, to }, signal: opts.signal });
    return await this.getInfo(newPath, opts);
  }

  async remove(path: string, opts: FilesystemRequestOpts = {}): Promise<void> {
    if (workspaceRelative(path) === undefined) {
      await this.#outside(`rm -rf -- ${quote(path)}`, opts, `files.remove(${path})`);
      return;
    }
    try {
      await this.#conn.call('POST', '/files:remove', { json: { path: this.#inside(path, 'files.remove'), recursive: true }, signal: opts.signal });
    } catch (err) {
      if (!(err instanceof NotFoundError)) throw err;
    }
  }

  async watchDir(): Promise<never> {
    throw new UnsupportedError('files.watchDir', 'poll files.list, or run a watcher as a background command');
  }

  #inside(path: string, call: string): string {
    const rel = workspaceRelative(path);
    if (rel === undefined) throw new UnsupportedError(`${call}(${path})`, `this call works inside ${WORKSPACE}; use a command for other paths`);
    return rel;
  }
}
