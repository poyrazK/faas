import { decodedMappings, originalPositionFor, TraceMap } from '@jridgewell/trace-mapping';
import { isAbsolute } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

/** A flat Source Map v3 artifact shipped with the running deployment. */
export interface IssueSourceMap {
  /** Exact absolute bundle path or file URL as it appears in error stacks. */
  generatedFile: string;
  /** JSON supplied for manual registration. Capture never reads files or fetches map URLs. */
  payload: string;
}

export interface ReportedIssueFrame {
  file: string;
  function: string;
  line: number;
  in_app: boolean;
}

// Mirror pkg/api/limits.go, ADR-1113. Count decoded rows AND segments to bound
// maps containing many empty lines as well as dense minified bundles.
export const issueSourceMapLimits = Object.freeze({
  maps: 32, mapBytes: 512 << 10, totalBytes: 4 << 20,
  entries: 100000, symbols: 8192, fileBytes: 512,
});
const { maps: maxMaps, mapBytes: maxMapBytes, totalBytes: maxTotalBytes,
  entries: maxEntries, symbols: maxSymbols, fileBytes: maxFileBytes } = issueSourceMapLimits;

function invalid(): never {
  throw new Error('Invalid issue source map: expected bounded flat Source Map v3 JSON');
}

function fileKey(file: string): string | undefined {
  try {
    const url = isAbsolute(file) ? pathToFileURL(file) : new URL(file);
    if (url.protocol !== 'file:' || url.username || url.password || url.search || url.hash) return undefined;
    // Validate host/encoding and make file://localhost and absolute paths agree.
    return pathToFileURL(fileURLToPath(url)).href;
  } catch {
    return undefined;
  }
}

function originalSource(source: string, sourceRoot: string, generatedFile: string): string {
  // sourceRoot is a directory even when the producer omitted a trailing slash.
  const root = sourceRoot === '' ? generatedFile
    : new URL(sourceRoot.endsWith('/') ? sourceRoot : sourceRoot + '/', generatedFile).href;
  const url = new URL(source, root);
  if (url.username || url.password) invalid();
  return url.protocol === 'file:' ? fileURLToPath(url) : url.href;
}

function strings(value: unknown): value is string[] {
  return Array.isArray(value) && value.length <= maxSymbols &&
    value.every(v => typeof v === 'string' && Buffer.byteLength(v, 'utf8') <= maxFileBytes);
}

// The codec is intentionally permissive. Reject incomplete/overflowing VLQs
// and invalid segment shapes before decoding can turn them into plausible data.
function validateMappings(encoded: string): void {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  let fields = 0;
  let digits = 0;
  let afterComma = false;
  const validFields = () => fields === 0 || fields === 1 || fields === 4 || fields === 5;
  for (const char of encoded) {
    if (char === ',' || char === ';') {
      if (digits !== 0 || !validFields() || afterComma || (char === ',' && fields === 0)) invalid();
      fields = 0;
      afterComma = char === ',';
      continue;
    }
    const digit = alphabet.indexOf(char);
    digits++;
    if (digit < 0 || digits > 7 || (digits === 7 && digit > 3)) invalid();
    afterComma = false;
    if ((digit & 32) === 0) {
      fields++;
      digits = 0;
      if (fields > 5) invalid();
    }
  }
  if (digits !== 0 || afterComma || !validFields()) invalid();
}

/** Register once; capture only performs exact matching and a position lookup. */
export function registerIssueSourceMaps(registrations: readonly IssueSourceMap[] = []) {
  if (!Array.isArray(registrations) || registrations.length > maxMaps) invalid();
  const maps = new Map<string, TraceMap>();
  let bytes = 0;
  let entries = 0;
  let symbols = 0;
  for (const registration of registrations) {
    if (!registration || typeof registration.generatedFile !== 'string' || typeof registration.payload !== 'string' ||
        Buffer.byteLength(registration.generatedFile, 'utf8') > maxFileBytes) invalid();
    const key = fileKey(registration.generatedFile);
    if (!key || maps.has(key)) invalid();
    const size = Buffer.byteLength(registration.payload, 'utf8');
    bytes += size;
    if (size > maxMapBytes || bytes > maxTotalBytes) invalid();
    let payload: Record<string, unknown>;
    try {
      payload = JSON.parse(registration.payload) as Record<string, unknown>;
    } catch {
      invalid();
    }
    if (!payload || Array.isArray(payload) || payload.version !== 3 || 'sections' in payload ||
        !strings(payload.sources) || payload.sources.some(v => v === '') ||
        !strings(payload.names) || typeof payload.mappings !== 'string' ||
        (payload.sourceRoot !== undefined && (typeof payload.sourceRoot !== 'string' ||
          Buffer.byteLength(payload.sourceRoot, 'utf8') > maxFileBytes))) invalid();
    symbols += payload.sources.length + payload.names.length;
    if (symbols > maxSymbols) invalid();
    validateMappings(payload.mappings);
    try {
      const sources = payload.sources.map(source => originalSource(source, (payload.sourceRoot ?? '') as string, key));
      if (sources.some(source => Buffer.byteLength(source, 'utf8') > maxFileBytes)) invalid();
      // Whitelist mapping metadata. sourcesContent and all producer extensions
      // are discarded and cannot be included in outgoing exception evidence.
      const map = new TraceMap({ version: 3, sources, names: [...payload.names], mappings: payload.mappings });
      const lines = decodedMappings(map);
      entries += lines.length;
      if (entries > maxEntries) invalid();
      for (const line of lines) {
        entries += line.length;
        if (entries > maxEntries) invalid();
        for (const segment of line) {
          if (![1, 4, 5].includes(segment.length) || segment.some(v => !Number.isSafeInteger(v) || v < 0)) invalid();
          if (segment.length === 1) continue;
          if (segment[1] >= sources.length || (segment.length === 5 && segment[4] >= payload.names.length)) invalid();
        }
      }
      maps.set(key, map);
    } catch {
      invalid();
    }
  }
  return {
    enabled: maps.size > 0,
    resolve(frame: ReportedIssueFrame, column: number): ReportedIssueFrame | undefined {
      if (maps.size === 0 || !Number.isSafeInteger(frame.line) || frame.line < 1 ||
          !Number.isSafeInteger(column) || column < 1) return undefined;
      const key = fileKey(frame.file);
      const map = key === undefined ? undefined : maps.get(key);
      if (!map) return undefined;
      const position = originalPositionFor(map, { line: frame.line, column: column - 1 });
      if (position.source === null || position.line === null) return undefined;
      return {
        file: position.source, function: position.name?.trim() || '<anonymous>', line: position.line,
        in_app: !position.source.startsWith('node:') &&
          !position.source.replaceAll('\\', '/').includes('/node_modules/'),
      };
    },
  };
}
