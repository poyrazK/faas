import { createHash } from 'node:crypto';
import { constants, closeSync, fstatSync, lstatSync, openSync, opendirSync, readSync, realpathSync } from 'node:fs';
import { isAbsolute, join, posix, relative, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { issueSourceMapLimits, registerIssueSourceMaps, type IssueSourceMap } from './issue-source-maps.js';

// Discovery and integrity verification run once, never on the capture path.
export const issueSourceMapDiscoveryLimits = Object.freeze({
  files: 4096, bundleBytes: 16 << 20, totalBundleBytes: 64 << 20,
  manifestBytes: issueSourceMapLimits.totalBytes + (64 << 10),
});

interface ManifestArtifact { generatedFile: string; sha256: string; payload: string }
interface Manifest { version: 1; artifacts: ManifestArtifact[] }
export interface IssueSourceMapManifestOptions {
  /** Manifest produced after building the exact deployed bundles. */
  file: string;
  /** Deployed directory corresponding to the directory scanned at build time. */
  directory: string;
}

function invalid(detail: string): never {
  throw new Error(`Invalid issue source-map manifest: ${detail}`);
}
function contained(root: string, file: string): boolean {
  const path = relative(root, file);
  return path !== '' && path !== '..' && !path.startsWith('..' + sep) && !isAbsolute(path);
}
function portablePath(value: unknown): value is string {
  return typeof value === 'string' && value !== '' && Buffer.byteLength(value) <= issueSourceMapLimits.fileBytes &&
    !value.includes('\\') && !value.includes(':') && !value.includes('\0') &&
    !posix.isAbsolute(value) && posix.normalize(value) === value && value !== '..' && !value.startsWith('../');
}
function readBounded(file: string, maximum: number, root?: string): Buffer {
  // Reject links, special files, and paths escaping the selected build tree.
  // These helpers operate on trusted, immutable build/deployment directories.
  if (root && !contained(root, realpathSync(file))) invalid('file escapes the selected directory');
  const fd = openSync(file, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const before = fstatSync(fd);
    if (!before.isFile() || before.size > maximum) invalid('file exceeds its byte bound or is not regular');
    const data = Buffer.alloc(before.size);
    let offset = 0;
    while (offset < data.length) {
      const length = readSync(fd, data, offset, data.length - offset, offset);
      if (!length) invalid('file changed during reading');
      offset += length;
    }
    const after = fstatSync(fd);
    if (after.size !== before.size || after.mtimeMs !== before.mtimeMs || after.ctimeMs !== before.ctimeMs) {
      invalid('file changed during reading');
    }
    return data;
  } finally { closeSync(fd); }
}
function digest(bytes: Buffer): string { return createHash('sha256').update(bytes).digest('hex'); }
function parse(text: string): unknown {
  try { return JSON.parse(text); } catch { invalid('expected JSON'); }
}
function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function portableMap(bytes: Buffer, generatedFile: string, mapFile: string): string {
  const map = parse(bytes.toString('utf8'));
  if (!record(map) || map.version !== 3 || 'sections' in map || !Array.isArray(map.sources)) {
    invalid('expected a flat Source Map v3 artifact');
  }
  const root = map.sourceRoot ?? '';
  const absoluteSource = (value: string) => value.startsWith('/') || value.includes('\\') ||
    /^file:/i.test(value) || /^[a-z]:/i.test(value);
  if (typeof root !== 'string' || Buffer.byteLength(root) > issueSourceMapLimits.fileBytes || absoluteSource(root) ||
      map.sources.some(source => typeof source !== 'string' || absoluteSource(source))) {
    invalid('use relative or virtual source paths for a portable build');
  }
  // Source Map sources are relative to the map URL, while the reporter resolves
  // them against the bundle. Rebase local roots, including maps in another folder.
  const sourceRoot = /^[a-z][a-z0-9+.-]*:/i.test(root) ? root
    : posix.join(posix.relative(posix.dirname(generatedFile), posix.dirname(mapFile)), root) + '/';
  const payload = JSON.stringify({ version: 3, sourceRoot, sources: map.sources, names: map.names, mappings: map.mappings });
  // Discard sourcesContent and producer extensions before writing the manifest.
  registerIssueSourceMaps([{ generatedFile: '/app/' + generatedFile, payload }]);
  return payload;
}

function mapReference(bundle: string): string | undefined {
  // Require complete source-map comments, rather than an arbitrary URL substring.
  const pattern = /^\s*\/\/[#@]\s*sourceMappingURL=([^\s]+)\s*$/gm;
  const block = /\/\*[#@]\s*sourceMappingURL=([^\s]+)\s*\*\//g;
  const matches = [...bundle.matchAll(pattern), ...bundle.matchAll(block)].sort((a, b) => a.index! - b.index!);
  return matches.at(-1)?.[1];
}

/** Build-time discovery: scan local JS bundles and emit a portable, content-free
 * manifest pinned to their exact bytes. No source files execute and no URL is fetched. */
export function buildIssueSourceMapManifest(directory: string): string {
  const root = realpathSync(directory);
  const files: string[] = [];
  let visited = 0;
  const pending = [root];
  while (pending.length) {
    const directory = pending.pop()!;
    if (directory !== root && !contained(root, realpathSync(directory))) invalid('directory escapes the build tree');
    const entries = opendirSync(directory);
    try {
      for (let entry = entries.readSync(); entry; entry = entries.readSync()) {
        if (++visited > issueSourceMapDiscoveryLimits.files) invalid('build tree exceeds the entry bound');
        if (entry.isSymbolicLink() || entry.name === 'node_modules' || entry.name === '.git') continue;
        const file = join(directory, entry.name);
        if (entry.isDirectory()) pending.push(file);
        else if (entry.isFile() && /\.(?:js|mjs|cjs)$/.test(entry.name)) files.push(file);
      }
    } finally { entries.closeSync(); }
  }
  files.sort();
  const artifacts: ManifestArtifact[] = [];
  let bundleBytes = 0, mapBytes = 0;
  for (const file of files) {
    const size = lstatSync(file).size;
    if (size > issueSourceMapDiscoveryLimits.bundleBytes || bundleBytes + size > issueSourceMapDiscoveryLimits.totalBundleBytes) {
      invalid('bundles exceed the discovery byte bound');
    }
    const bytes = readBounded(file, Math.min(issueSourceMapDiscoveryLimits.bundleBytes,
      issueSourceMapDiscoveryLimits.totalBundleBytes - bundleBytes), root);
    bundleBytes += bytes.length;
    const reference = mapReference(bytes.toString('utf8'));
    let mapFile: string;
    if (reference !== undefined) {
      if (reference.includes('\\') || isAbsolute(reference) || /^[a-z][a-z0-9+.-]*:/i.test(reference)) {
        invalid('sourceMappingURL must refer to a local relative map');
      }
      const url = new URL(reference, pathToFileURL(file));
      if (url.protocol !== 'file:' || url.search || url.hash) invalid('sourceMappingURL must be a plain local path');
      mapFile = fileURLToPath(url);
    } else {
      // Hidden-source-map builds omit the annotation but ship bundle.js.map.
      mapFile = file + '.map';
      try { lstatSync(mapFile); } catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ENOENT') continue;
        throw error;
      }
    }
    if (!contained(root, mapFile)) invalid('map escapes the build tree');
    if (artifacts.length === issueSourceMapLimits.maps) invalid('too many maps');
    const map = readBounded(mapFile, issueSourceMapLimits.mapBytes, root);
    mapBytes += map.length;
    if (mapBytes > issueSourceMapLimits.totalBytes) invalid('maps exceed the total byte bound');
    const generatedFile = relative(root, file).split(sep).join('/');
    if (!portablePath(generatedFile)) invalid('invalid generated bundle path');
    artifacts.push({ generatedFile, sha256: digest(bytes),
      payload: portableMap(map, generatedFile, relative(root, mapFile).split(sep).join('/')) });
  }
  registerIssueSourceMaps(artifacts.map(a => ({ generatedFile: '/app/' + a.generatedFile, payload: a.payload })));
  const manifest = JSON.stringify({ version: 1, artifacts } satisfies Manifest);
  if (Buffer.byteLength(manifest) > issueSourceMapDiscoveryLimits.manifestBytes) invalid('manifest exceeds its byte bound');
  return manifest;
}

/** Verify the deployed bundle bytes and bind portable paths to this deployment.
 * Returned maps register immutably in the reporter; capture performs no file I/O. */
export function loadIssueSourceMapManifest(options: IssueSourceMapManifestOptions): IssueSourceMap[] {
  const bytes = readBounded(options.file, issueSourceMapDiscoveryLimits.manifestBytes);
  const manifest = parse(bytes.toString('utf8'));
  if (!record(manifest) || manifest.version !== 1 || !Array.isArray(manifest.artifacts) ||
      manifest.artifacts.length > issueSourceMapLimits.maps ||
      Object.keys(manifest).some(key => !['version', 'artifacts'].includes(key))) invalid('unsupported manifest');
  const root = realpathSync(options.directory);
  const maps: IssueSourceMap[] = [];
  let bundleBytes = 0;
  for (const artifact of manifest.artifacts) {
    if (!record(artifact) || !portablePath(artifact.generatedFile) || typeof artifact.sha256 !== 'string' ||
        !/^[0-9a-f]{64}$/.test(artifact.sha256) || typeof artifact.payload !== 'string' ||
        Object.keys(artifact).some(key => !['generatedFile', 'sha256', 'payload'].includes(key))) invalid('invalid artifact');
    const generatedFile = resolve(root, artifact.generatedFile);
    const bundle = readBounded(generatedFile, Math.min(issueSourceMapDiscoveryLimits.bundleBytes,
      issueSourceMapDiscoveryLimits.totalBundleBytes - bundleBytes), root);
    bundleBytes += bundle.length;
    if (digest(bundle) !== artifact.sha256) invalid('bundle digest mismatch; rebuild the manifest for this deployment');
    maps.push({ generatedFile, payload: artifact.payload });
  }
  registerIssueSourceMaps(maps);
  return maps;
}
