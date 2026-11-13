// A minimal ustar writer: what workspace:hydrate unpacks, built from a
// manifest's entries. Regular files and directories only.

import { gzipSync } from 'node:zlib';

export type TarEntry =
  | { type: 'file'; path: string; data: Uint8Array; mode?: number }
  | { type: 'dir'; path: string; mode?: number };

const BLOCK = 512;
const encoder = new TextEncoder();

function octal(value: number, width: number): string {
  return value.toString(8).padStart(width - 1, '0') + '\0';
}

function splitName(path: string): { name: string; prefix: string } {
  const bytes = encoder.encode(path);
  if (bytes.length <= 100) return { name: path, prefix: '' };
  // ustar keeps up to 155 bytes of the directory in prefix.
  for (let i = path.lastIndexOf('/'); i > 0; i = path.lastIndexOf('/', i - 1)) {
    const prefix = path.slice(0, i);
    const name = path.slice(i + 1);
    if (encoder.encode(prefix).length <= 155 && encoder.encode(name).length <= 100) {
      return { name, prefix };
    }
  }
  throw new Error(`path is too long for the workspace archive: ${path}`);
}

function header(entry: TarEntry, size: number): Uint8Array {
  const block = new Uint8Array(BLOCK);
  const path = entry.type === 'dir' && !entry.path.endsWith('/') ? `${entry.path}/` : entry.path;
  const { name, prefix } = splitName(path);
  const put = (offset: number, text: string) => block.set(encoder.encode(text), offset);
  put(0, name);
  put(100, octal(entry.mode ?? (entry.type === 'dir' ? 0o755 : 0o644), 8));
  put(108, octal(0, 8));
  put(116, octal(0, 8));
  put(124, octal(size, 12));
  put(136, octal(0, 12));
  put(148, '        ');
  put(156, entry.type === 'dir' ? '5' : '0');
  put(257, 'ustar\0');
  put(263, '00');
  put(345, prefix);
  let sum = 0;
  for (const b of block) sum += b;
  put(148, octal(sum, 7) + ' ');
  return block;
}

/** The entries as a tar archive. */
export function tar(entries: TarEntry[]): Uint8Array {
  const parts: Uint8Array[] = [];
  for (const entry of entries) {
    const data = entry.type === 'file' ? entry.data : new Uint8Array();
    parts.push(header(entry, data.length));
    if (data.length > 0) {
      parts.push(data);
      const pad = (BLOCK - (data.length % BLOCK)) % BLOCK;
      if (pad > 0) parts.push(new Uint8Array(pad));
    }
  }
  parts.push(new Uint8Array(BLOCK * 2));
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let offset = 0;
  for (const p of parts) {
    out.set(p, offset);
    offset += p.length;
  }
  return out;
}

/** data gzipped, unless it already is. */
export function gzipped(data: Uint8Array): Uint8Array {
  if (data.length >= 2 && data[0] === 0x1f && data[1] === 0x8b) return data;
  return new Uint8Array(gzipSync(data));
}
