import { createHash, createPublicKey, verify as verifySignature, type KeyObject } from 'node:crypto';
import { isIP } from 'node:net';

/** Header stamped by Gregale's internal service proxy. Treat it as untrusted until verified. */
export const SERVICE_CALLER_ASSERTION_HEADER = 'X-Faas-Caller-Assertion';

const ISSUER = 'gregale.svc';
const DEFAULT_CACHE_TTL_MS = 5_000;
const DEFAULT_FETCH_TIMEOUT_MS = 5_000;
const UNKNOWN_KID_REFRESH_MS = 1_000;
const MAX_ASSERTION_BYTES = 16 * 1024;
const MAX_JWKS_BYTES = 4 * 1024 * 1024;
const MAX_JWKS_KEYS = 20_000;

export type ServiceCallerErrorCode =
  | 'missing_assertion'
  | 'malformed'
  | 'unknown_key'
  | 'bad_signature'
  | 'expired'
  | 'wrong_audience'
  | 'wrong_issuer'
  | 'jwks_unavailable';

/** A safe-to-log verification failure. Error messages never include the token. */
export class ServiceCallerVerificationError extends Error {
  constructor(readonly code: ServiceCallerErrorCode, message: string) {
    super(`servicecaller: ${message}`);
    this.name = 'ServiceCallerVerificationError';
  }
}

/** Caller identity from a verified Gregale assertion. */
export interface VerifiedServiceCaller {
  callerAppId: string;
  targetAppId: string;
  accountId: string;
  callerInstanceId: string;
  callerEnv: string;
  id: string;
}

export interface ServiceCallerVerifierOptions {
  /** Public Gregale /v1/service-caller-keys endpoint. HTTPS is required outside loopback tests. */
  jwksUrl: string;
  /** This target app's platform-injected FAAS_APP_ID. */
  audience: string;
  /** Optional rollout mode. Missing/invalid assertions are ignored but never returned as identity. */
  require?: boolean;
  /** Primarily useful for tests and custom Node fetch implementations. */
  fetch?: typeof globalThis.fetch;
  /** Called for missing or invalid assertions. Do not block or throw from this callback. */
  onFailure?: (error: ServiceCallerVerificationError) => void;
}

/**
 * Verify Gregale's signed caller assertion against its public JWKS.
 * Verification is Ed25519/EdDSA-only and checks issuer, target audience, and
 * the assertion validity window. It authenticates a caller but does not
 * authorize that caller; applications still need their own caller policy.
 */
export interface ServiceCallerVerifier {
  /** Verify one compact assertion, throwing a typed error on any failure. */
  verify(token: string): Promise<VerifiedServiceCaller>;
  /**
   * Verify an inbound header value. Optional mode returns undefined for an
   * absent or invalid assertion; required mode throws and should produce 401.
   * Arrays are rejected so duplicate header values cannot be ambiguous.
   */
  verifyHeader(value: string | readonly string[] | undefined): Promise<VerifiedServiceCaller | undefined>;
}

/**
 * Create a verifier with a five-second JWKS cache and single-flight refresh.
 * Unknown key IDs trigger an immediate refresh, throttled to one per second.
 */
export function createServiceCallerVerifier(options: ServiceCallerVerifierOptions): ServiceCallerVerifier {
  const audience = options.audience.trim();
  if (!audience) throw new TypeError('servicecaller: verifier audience is required');
  const jwksUrl = validateJwksUrl(options.jwksUrl);
  const fetchImpl = options.fetch ?? globalThis.fetch;
  if (typeof fetchImpl !== 'function') throw new TypeError('servicecaller: fetch is unavailable');

  const cache = new CallerKeyCache(jwksUrl, fetchImpl);
  const required = options.require ?? false;
  const onFailure = options.onFailure;

  const verify = async (token: string): Promise<VerifiedServiceCaller> => {
    if (typeof token !== 'string' || !token || Buffer.byteLength(token, 'utf8') > MAX_ASSERTION_BYTES) {
      throw new ServiceCallerVerificationError('malformed', 'assertion is empty or too large');
    }

    const parsed = parseCompactAssertion(token);
    const keys = await cache.forKid(parsed.kid);
    const key = keys.get(parsed.kid);
    if (!key) throw new ServiceCallerVerificationError('unknown_key', 'assertion names an untrusted signing key');

    const valid = verifySignature(null, parsed.signingInput, key, parsed.signature);
    if (!valid) throw new ServiceCallerVerificationError('bad_signature', 'assertion signature is invalid');

    const claims = parseClaims(parsed.payload);
    if (claims.iss !== ISSUER) throw new ServiceCallerVerificationError('wrong_issuer', 'assertion issuer is not Gregale');
    if (!claims.aud.includes(audience)) {
      throw new ServiceCallerVerificationError('wrong_audience', 'assertion is for a different target app');
    }
    const now = Math.floor(Date.now() / 1000);
    if (claims.exp === 0 || now >= claims.exp || (claims.nbf !== 0 && now < claims.nbf)) {
      throw new ServiceCallerVerificationError('expired', 'assertion is outside its validity window');
    }
    if (!claims.sub.trim()) throw new ServiceCallerVerificationError('malformed', 'assertion subject is empty');

    return {
      callerAppId: claims.sub,
      targetAppId: audience,
      accountId: claims.account_id,
      callerInstanceId: claims.caller_instance_id,
      callerEnv: claims.caller_env,
      id: claims.jti,
    };
  };

  return {
    verify,
    async verifyHeader(value) {
      try {
        if (value === undefined) {
          if (!required) return undefined;
          throw new ServiceCallerVerificationError('missing_assertion', 'assertion header is missing');
        }
        if (Array.isArray(value) || typeof value !== 'string') {
          throw new ServiceCallerVerificationError('malformed', 'expected exactly one assertion header');
        }
        return await verify(value.trim());
      } catch (error) {
        const failure = error instanceof ServiceCallerVerificationError
          ? error
          : new ServiceCallerVerificationError('jwks_unavailable', 'could not verify assertion');
        onFailure?.(failure);
        if (required) throw failure;
        return undefined;
      }
    },
  };
}

class CallerKeyCache {
  private keys = new Map<string, KeyObject>();
  private expiresAt = 0;
  private unknownKidRefreshUntil = 0;
  private retryAfter = 0;
  private lastError?: ServiceCallerVerificationError;
  private refreshPromise?: Promise<Map<string, KeyObject>>;

  constructor(private readonly endpoint: URL, private readonly fetchImpl: typeof globalThis.fetch) {}

  async forKid(kid: string): Promise<Map<string, KeyObject>> {
    const now = Date.now();
    if (now < this.expiresAt && (this.keys.has(kid) || now < this.unknownKidRefreshUntil)) {
      return this.keys;
    }
    if (now < this.retryAfter && this.lastError) throw this.lastError;

    if (!this.refreshPromise) {
      this.refreshPromise = this.refresh().finally(() => {
        this.refreshPromise = undefined;
      });
    }
    const keys = await this.refreshPromise;
    if (!keys.has(kid)) {
      this.unknownKidRefreshUntil = Date.now() + UNKNOWN_KID_REFRESH_MS;
      throw new ServiceCallerVerificationError('unknown_key', 'assertion names an untrusted signing key');
    }
    return keys;
  }

  private async refresh(): Promise<Map<string, KeyObject>> {
    try {
      const response = await this.fetchImpl(this.endpoint, {
        method: 'GET',
        headers: { Accept: 'application/jwk-set+json, application/json' },
        redirect: 'manual',
        signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
      });
      if (response.status !== 200) {
        throw new ServiceCallerVerificationError('jwks_unavailable', `key endpoint returned status ${response.status}`);
      }
      const raw = await readBoundedBody(response, MAX_JWKS_BYTES);
      const keys = parseJwks(raw);
      this.keys = keys;
      this.expiresAt = Date.now() + DEFAULT_CACHE_TTL_MS;
      this.unknownKidRefreshUntil = 0;
      this.retryAfter = 0;
      this.lastError = undefined;
      return keys;
    } catch (error) {
      const failure = error instanceof ServiceCallerVerificationError
        ? error
        : new ServiceCallerVerificationError('jwks_unavailable', 'could not fetch trusted signing keys');
      this.lastError = failure;
      this.retryAfter = Date.now() + UNKNOWN_KID_REFRESH_MS;
      throw failure;
    }
  }
}

interface ParsedAssertion {
  kid: string;
  signingInput: Buffer;
  signature: Buffer;
  payload: Buffer;
}

function parseCompactAssertion(token: string): ParsedAssertion {
  const parts = token.split('.');
  if (parts.length !== 3) throw new ServiceCallerVerificationError('malformed', 'assertion is not compact JWS');
  const headerBytes = decodeBase64Url(parts[0] ?? '');
  const payload = decodeBase64Url(parts[1] ?? '');
  const signature = decodeBase64Url(parts[2] ?? '');
  let header: unknown;
  try {
    header = JSON.parse(decodeUtf8(headerBytes));
  } catch {
    throw new ServiceCallerVerificationError('malformed', 'assertion header is invalid');
  }
  if (!isRecord(header) || header.alg !== 'EdDSA' || typeof header.kid !== 'string' || !header.kid) {
    throw new ServiceCallerVerificationError('malformed', 'assertion header must name an EdDSA key');
  }
  if (signature.byteLength !== 64) throw new ServiceCallerVerificationError('malformed', 'assertion signature has an invalid size');
  return {
    kid: header.kid,
    signingInput: Buffer.from(`${parts[0]}.${parts[1]}`, 'ascii'),
    signature,
    payload,
  };
}

interface AssertionClaims {
  iss: string;
  sub: string;
  aud: string[];
  exp: number;
  nbf: number;
  jti: string;
  account_id: string;
  caller_instance_id: string;
  caller_env: string;
}

function parseClaims(payload: Buffer): AssertionClaims {
  let value: unknown;
  try {
    value = JSON.parse(decodeUtf8(payload));
  } catch {
    throw new ServiceCallerVerificationError('malformed', 'assertion claims are invalid');
  }
  if (!isRecord(value)
    || typeof value.iss !== 'string'
    || typeof value.sub !== 'string'
    || !Array.isArray(value.aud)
    || !value.aud.every((entry) => typeof entry === 'string')
    || !Number.isSafeInteger(value.exp)
    || !Number.isSafeInteger(value.nbf ?? 0)) {
    throw new ServiceCallerVerificationError('malformed', 'assertion claims have an invalid shape');
  }
  return {
    iss: value.iss,
    sub: value.sub,
    aud: value.aud,
    exp: value.exp as number,
    nbf: (value.nbf ?? 0) as number,
    jti: typeof value.jti === 'string' ? value.jti : '',
    account_id: typeof value.account_id === 'string' ? value.account_id : '',
    caller_instance_id: typeof value.caller_instance_id === 'string' ? value.caller_instance_id : '',
    caller_env: typeof value.caller_env === 'string' ? value.caller_env : '',
  };
}

function parseJwks(raw: string): Map<string, KeyObject> {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new ServiceCallerVerificationError('jwks_unavailable', 'key endpoint returned invalid JSON');
  }
  if (!isRecord(value) || !Array.isArray(value.keys)) {
    throw new ServiceCallerVerificationError('jwks_unavailable', 'key endpoint returned an invalid JWK set');
  }
  const jwks = value.keys;
  if (jwks.length > MAX_JWKS_KEYS) throw new ServiceCallerVerificationError('jwks_unavailable', 'key set is too large');

  const keys = new Map<string, KeyObject>();
  for (const value of jwks) {
    if (!isRecord(value)
      || value.kty !== 'OKP'
      || value.crv !== 'Ed25519'
      || value.alg !== 'EdDSA'
      || value.use !== 'sig'
      || typeof value.kid !== 'string'
      || !value.kid
      || typeof value.x !== 'string') {
      throw new ServiceCallerVerificationError('jwks_unavailable', 'key set contains an unsupported key');
    }
    if (keys.has(value.kid)) throw new ServiceCallerVerificationError('jwks_unavailable', 'key set contains duplicate key IDs');
    let publicKey: Buffer;
    try {
      publicKey = decodeBase64Url(value.x);
    } catch {
      throw new ServiceCallerVerificationError('jwks_unavailable', 'key set contains an invalid public key');
    }
    if (publicKey.byteLength !== 32 || keyId(publicKey) !== value.kid) {
      throw new ServiceCallerVerificationError('jwks_unavailable', 'public key does not match its key ID');
    }
    try {
      const key = createPublicKey({ key: { kty: 'OKP', crv: 'Ed25519', x: value.x }, format: 'jwk' });
      keys.set(value.kid, key);
    } catch {
      throw new ServiceCallerVerificationError('jwks_unavailable', 'could not import public key');
    }
  }
  return keys;
}

async function readBoundedBody(response: Response, maxBytes: number): Promise<string> {
  if (!response.body) throw new ServiceCallerVerificationError('jwks_unavailable', 'key endpoint returned no body');
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxBytes) {
        await reader.cancel();
        throw new ServiceCallerVerificationError('jwks_unavailable', 'key set exceeds the size limit');
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  try {
    return decodeUtf8(Buffer.concat(chunks, size));
  } catch {
    throw new ServiceCallerVerificationError('jwks_unavailable', 'key endpoint body is not valid UTF-8');
  }
}

function decodeBase64Url(value: string): Buffer {
  if (!value || !/^[A-Za-z0-9_-]+$/.test(value)) {
    throw new ServiceCallerVerificationError('malformed', 'assertion contains invalid base64url');
  }
  const decoded = Buffer.from(value, 'base64url');
  if (decoded.toString('base64url') !== value) {
    throw new ServiceCallerVerificationError('malformed', 'assertion contains non-canonical base64url');
  }
  return decoded;
}

function keyId(publicKey: Buffer): string {
  return createHash('sha256').update(publicKey).digest().subarray(0, 16).toString('base64url');
}

function decodeUtf8(value: Buffer): string {
  return new TextDecoder('utf-8', { fatal: true }).decode(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function validateJwksUrl(value: string): URL {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new TypeError('servicecaller: invalid JWKS URL');
  }
  if (!url.hostname || url.username || url.password || url.hash) {
    throw new TypeError('servicecaller: invalid JWKS URL');
  }
  if (url.protocol === 'https:') return url;
  const host = url.hostname.replace(/^\[|\]$/g, '');
  const loopback = isIP(host) !== 0 && (host === '::1' || host.startsWith('127.'));
  if (url.protocol === 'http:' && loopback) return url;
  throw new TypeError('servicecaller: JWKS URL must use HTTPS');
}
