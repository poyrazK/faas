import { createHash } from 'node:crypto';
import { AsyncLocalStorage } from 'node:async_hooks';

export interface ProgressiveRollout {
  stages: number[]; current_stage: number; minimum_used_requests: number;
  maximum_http_5xx_rate_basis_points: number; maximum_p95_latency_ms: number; window_seconds: number;
}
export interface FlagRule { id: string; customers?: string[]; group?: string; rollout?: number; value: boolean; progression?: ProgressiveRollout }
export interface VariantFlagRule { id: string; customers?: string[]; group?: string; rollout?: number; value?: string }
export interface WeightedVariant { key: string; weight: number }
export interface FeatureFlag { key: string; description?: string; type?: 'boolean'; enabled: boolean; default: boolean; seed: string; rules: FlagRule[]; variants?: never }
export type BooleanFeatureFlag = FeatureFlag;
export interface VariantFeatureFlag { key: string; description?: string; type: 'variant'; enabled: boolean; default: string; seed: string; rules: VariantFlagRule[]; variants: WeightedVariant[] }
export type FlagDefinition = FeatureFlag | VariantFeatureFlag;
export interface FlagsBundle { environment_id: string; version: number; flags: FlagDefinition[]; groups: Record<string, string[]> }
export interface FlagDecision {
  flag: string; value: boolean; config_version: number; rule_id?: string;
  reason: string; bucket?: number; source: 'configuration' | 'fallback' | 'inherited'; inherited_from?: FlagDecisionOrigin;
}
export interface VariantFlagDecision {
  flag: string; type: 'variant'; value: string; config_version: number; rule_id?: string;
  reason: string; bucket?: number; rollout_bucket?: number; source: 'configuration' | 'fallback' | 'inherited'; inherited_from?: FlagDecisionOrigin;
}
export interface FlagDecisionOrigin { app_id: string; environment_id: string }
export type BooleanFlagDecision = FlagDecision;
export interface VariantFlagEvidence extends VariantFlagDecision { used: boolean }
export interface FlagEvidence extends FlagDecision { used: boolean }
export type AnyFlagDecision = FlagDecision | VariantFlagDecision;
export type AnyFlagEvidence = FlagEvidence | VariantFlagEvidence;
export type FlagRequestHeaders = HeadersInit | Record<string, string | string[] | undefined>;
export const GREGALE_FLAG_EVIDENCE_HEADER = 'X-Faas-Flag-Evidence';
export const GREGALE_FLAG_CONTEXT_HEADER = 'X-Faas-Platform-Tenant-Id';
export const GREGALE_FLAG_PROPAGATION_HEADER = 'X-Faas-Flag-Context';
const CUSTOMER_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const FLAG_KEY = /^[a-z][a-z0-9_-]{0,63}$/;

export function flagBucket(seed: string, key: string, customer: string): number {
  return createHash('sha256').update(`${seed}\0${key}\0${customer}`).digest().readUInt32BE(0) % 10000;
}
export function flagVariantBucket(seed: string, key: string, customer: string): number {
  return createHash('sha256').update(`${seed}\0${key}\0variant\0${customer}`).digest().readUInt32BE(0) % 10000;
}
/** Pure evaluator. customer must come from trusted server-side middleware. */
export function evaluateFlag(bundle: FlagsBundle, key: string, customer: string | undefined, fallback: boolean): FlagDecision {
  const d: FlagDecision = { flag: key, value: fallback, config_version: bundle.version, reason: 'flag_missing', source: 'fallback' };
  const f = bundle.flags.find(flag => flag.key === key);
  if (!f) return d;
  if (f.type === 'variant') return { ...d, reason: 'type_mismatch' };
  d.value = f.default; d.source = 'configuration'; d.reason = 'default';
  if (!f.enabled) return { ...d, reason: 'disabled' };
  if (!customer) return { ...d, reason: 'customer_missing' };
  for (const rule of f.rules) {
    if (rule.customers?.length && !rule.customers.includes(customer)) continue;
    if (rule.group && !bundle.groups[rule.group]?.includes(customer)) continue;
    let bucket: number | undefined;
    if (rule.rollout !== undefined) {
      bucket = flagBucket(f.seed, f.key, customer);
      if (bucket >= rule.rollout) continue;
    }
    return { ...d, value: rule.value, rule_id: rule.id, reason: 'rule_match', ...(bucket === undefined ? {} : { bucket }) };
  }
  return d;
}
/** Pure named-variant evaluator with deterministic weighted allocation. */
export function evaluateVariant(bundle: FlagsBundle, key: string, customer: string | undefined, fallback: string): VariantFlagDecision {
  const d: VariantFlagDecision = { flag: key, type: 'variant', value: fallback, config_version: bundle.version, reason: 'flag_missing', source: 'fallback' };
  const f = bundle.flags.find(flag => flag.key === key);
  if (!f) return d;
  if (f.type !== 'variant') return { ...d, reason: 'type_mismatch' };
  d.value = f.default; d.source = 'configuration'; d.reason = 'default';
  if (!f.enabled) return { ...d, reason: 'disabled' };
  if (!customer) return { ...d, reason: 'customer_missing' };
  for (const rule of f.rules) {
    if (rule.customers?.length && !rule.customers.includes(customer)) continue;
    if (rule.group && !bundle.groups[rule.group]?.includes(customer)) continue;
    let rolloutBucket: number | undefined;
    if (rule.rollout !== undefined) {
      rolloutBucket = flagBucket(f.seed, f.key, customer);
      if (rolloutBucket >= rule.rollout) continue;
    }
    if (rule.value !== undefined) return { ...d, value: rule.value, rule_id: rule.id, reason: 'rule_match', ...(rolloutBucket === undefined ? {} : { rollout_bucket: rolloutBucket }) };
    const bucket = flagVariantBucket(f.seed, f.key, customer);
    return { ...d, value: chooseVariant(f.variants, bucket), rule_id: rule.id, reason: 'rule_match', bucket, ...(rolloutBucket === undefined ? {} : { rollout_bucket: rolloutBucket }) };
  }
  return d;
}
function chooseVariant(variants: WeightedVariant[], bucket: number): string {
  for (const variant of variants) {
    if (bucket < variant.weight) return variant.key;
    bucket -= variant.weight;
  }
  return '';
}

export interface GregaleFlagsOptions {
  /** Public apid base URL, e.g. https://api.gregale.dev. HTTPS required. */
  apiURL: string;
  /** Loopback URL supplied by guest-init. Defaults to FAAS_WORKLOAD_IDENTITY_ENDPOINT. */
  identityEndpoint?: string;
  fetch?: typeof globalThis.fetch;
  refreshMs?: number;
  /** At most 60s. Once stale, evaluation returns the call's explicit fallback. */
  maxStaleMs?: number;
  timeoutMs?: number;
  now?: () => number;
}
type PropagatedDecision =
  | (FlagDecision & { source: 'configuration' | 'fallback'; origin: FlagDecisionOrigin })
  | (VariantFlagDecision & { source: 'configuration' | 'fallback'; origin: FlagDecisionOrigin });
type RequestFlags = { customer?: string; bundle?: FlagsBundle; fresh: boolean; evidence: Map<string, AnyFlagEvidence>; inherited: Map<string, PropagatedDecision> };

/** Server-only client. Refresh is asynchronous; all checks in a request share one snapshot. */
export class GregaleFlags {
  private bundle?: FlagsBundle;
  private refreshedAt = Number.NEGATIVE_INFINITY;
  private refreshing?: Promise<void>;
  private timer?: ReturnType<typeof setInterval>;
  private readonly requests = new AsyncLocalStorage<RequestFlags>();
  private readonly fetchImpl: typeof globalThis.fetch;
  private readonly now: () => number;
  private readonly maxStaleMs: number;
  private readonly timeoutMs: number;
  private readonly refreshMs: number;
  private readonly apiURL: URL;
  private readonly identityURL: URL;

  constructor(options: GregaleFlagsOptions) {
    this.apiURL = new URL('/v1/runtime/flags', options.apiURL);
    if (this.apiURL.protocol !== 'https:') throw new Error('Flags API requires HTTPS');
    this.identityURL = new URL(options.identityEndpoint ?? process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT ?? '');
    if (this.identityURL.protocol !== 'http:' || !['127.0.0.1', '[::1]', 'localhost'].includes(this.identityURL.hostname)) throw new Error('Workload identity endpoint must be loopback HTTP');
    this.identityURL.searchParams.set('audience', 'gregale:flags');
    this.fetchImpl = options.fetch ?? globalThis.fetch;
    this.now = options.now ?? Date.now;
    this.maxStaleMs = options.maxStaleMs ?? 60_000;
    this.refreshMs = options.refreshMs ?? 15_000;
    this.timeoutMs = options.timeoutMs ?? 2000;
    if (!(this.maxStaleMs > 0 && this.maxStaleMs <= 60_000 && this.refreshMs > 0 && this.refreshMs <= this.maxStaleMs && this.timeoutMs > 0 && this.timeoutMs <= 10_000)) throw new Error('Invalid Flags refresh or timeout bounds');
  }
  async start(): Promise<void> {
    // Application startup remains available with explicit fallback behavior.
    await this.refresh().catch(() => {});
    if (!this.timer) { this.timer = setInterval(() => { void this.refresh().catch(() => {}); }, this.refreshMs); this.timer.unref(); }
  }
  close(): void { if (this.timer) clearInterval(this.timer); this.timer = undefined; }
  refresh(): Promise<void> {
    if (this.refreshing) return this.refreshing;
    this.refreshing = this.load().finally(() => { this.refreshing = undefined; });
    return this.refreshing;
  }
  private async load(): Promise<void> {
    const signal = AbortSignal.timeout(this.timeoutMs);
    const identity = await this.fetchImpl(this.identityURL, { signal, redirect: 'error', cache: 'no-store' });
    if (!identity.ok) throw new Error('Flags workload identity unavailable');
    const token = await boundedJSON(identity, 16_384) as { access_token?: string };
    if (typeof token.access_token !== 'string' || !token.access_token || token.access_token.length > 8192) throw new Error('Invalid workload identity');
    const response = await this.fetchImpl(this.apiURL, { signal, redirect: 'error', cache: 'no-store', headers: { Authorization: `Bearer ${token.access_token}` } });
    if (!response.ok) throw new Error(`Flags refresh failed (${response.status})`);
    // Configuration is bounded to 256 KiB; the runtime envelope adds scope/version.
    const next = validateBundle(await boundedJSON(response, 262_144 + 1024));
    if (this.bundle && (next.environment_id !== this.bundle.environment_id || next.version < this.bundle.version)) throw new Error('Flags configuration scope or version regressed');
    this.bundle = next;
    this.refreshedAt = this.now();
  }
  /** Call only on requests delivered by Gregale's gateway, which replaces reserved headers.
   * Refresh on resume when stale; a bounded refresh failure uses explicit fallbacks. */
  async runRequest<T>(headers: FlagRequestHeaders, handler: () => T | Promise<T>): Promise<T> {
    const age = this.now() - this.refreshedAt;
    if (!this.bundle || age < 0 || age > this.maxStaleMs) await this.refresh().catch(() => {});
    const currentAge = this.now() - this.refreshedAt;
    const normalized = headers instanceof Headers || Array.isArray(headers) ? new Headers(headers) : new Headers(
      Object.entries(headers).filter((entry): entry is [string, string | string[]] => entry[1] !== undefined).map(([key, value]): [string, string] => [key, Array.isArray(value) ? value.join(', ') : value]));
    const rawCustomer = normalized.get(GREGALE_FLAG_CONTEXT_HEADER) ?? '';
    const decodedPropagation = decodePropagationHeader(normalized.get(GREGALE_FLAG_PROPAGATION_HEADER));
    const propagated = decodedPropagation && rawCustomer && rawCustomer.toLowerCase() !== decodedPropagation.customer_id
      ? undefined
      : decodedPropagation;
    const customer = propagated?.customer_id ?? (CUSTOMER_ID.test(rawCustomer) ? rawCustomer : undefined);
    const request: RequestFlags = {
      customer, bundle: this.bundle,
      fresh: !!this.bundle && currentAge >= 0 && currentAge <= this.maxStaleMs,
      evidence: new Map(), inherited: propagated?.decisions ?? new Map(),
    };
    return this.requests.run(request, handler);
  }
  boolean(key: string, fallback: boolean): FlagDecision {
    const r = this.requests.getStore();
    if (!r) throw new Error('Flag checks require runRequest');
    const prior = r.evidence.get(key);
    if (prior) return typeof prior.value === 'boolean' ? { ...prior } : { flag: key, value: fallback, config_version: prior.config_version, reason: 'type_mismatch', source: 'fallback' };
    const inherited = r.inherited.get(key);
    if (inherited) {
      const d: FlagDecision = typeof inherited.value === 'boolean'
        ? { ...inherited, source: 'inherited', inherited_from: { ...inherited.origin } }
        : { flag: key, value: fallback, config_version: r.bundle?.version ?? inherited.config_version, reason: 'type_mismatch', source: 'fallback' };
      if (r.evidence.size < 32) r.evidence.set(key, { ...d, used: false });
      return { ...d };
    }
    const d: FlagDecision = r.fresh && r.bundle ? evaluateFlag(r.bundle, key, r.customer, fallback) : { flag: key, value: fallback, config_version: r.bundle?.version ?? 0, reason: 'configuration_stale', source: 'fallback' };
    if (r.evidence.size < 32) r.evidence.set(key, { ...d, used: false });
    return d;
  }
  variant(key: string, fallback: string): VariantFlagDecision {
    const r = this.requests.getStore();
    if (!r) throw new Error('Flag checks require runRequest');
    const prior = r.evidence.get(key);
    if (prior) return typeof prior.value === 'string' ? { ...prior, type: 'variant' } : { flag: key, type: 'variant', value: fallback, config_version: prior.config_version, reason: 'type_mismatch', source: 'fallback' };
    const inherited = r.inherited.get(key);
    if (inherited) {
      const d: VariantFlagDecision = typeof inherited.value === 'string'
        ? { ...inherited, type: 'variant', source: 'inherited', inherited_from: { ...inherited.origin } }
        : { flag: key, type: 'variant', value: fallback, config_version: r.bundle?.version ?? inherited.config_version, reason: 'type_mismatch', source: 'fallback' };
      if (r.evidence.size < 32) r.evidence.set(key, { ...d, used: false });
      return { ...d };
    }
    const d: VariantFlagDecision = r.fresh && r.bundle ? evaluateVariant(r.bundle, key, r.customer, fallback) : { flag: key, type: 'variant', value: fallback, config_version: r.bundle?.version ?? 0, reason: 'configuration_stale', source: 'fallback' };
    if (r.evidence.size < 32) r.evidence.set(key, { ...d, used: false });
    return d;
  }
  /** Mark at the point the selected application path is entered. */
  used(key: string): void {
    const request = this.requests.getStore();
    const d = request?.evidence.get(key);
    // Evidence overflow must not interrupt the application's selected behavior.
    if (!d && request && request.evidence.size >= 32) return;
    if (!d) throw new Error('Flag must be evaluated before marking exposure');
    d.used = true;
  }
  evidence(): AnyFlagEvidence[] { return [...(this.requests.getStore()?.evidence.values() ?? [])].map(d => ({ ...d })); }
  /** Put on the app response before headers are sent; the gateway consumes and removes it. */
  responseEvidence(): string { return Buffer.from(JSON.stringify(this.evidence())).toString('base64url'); }
  /**
   * Build a bounded context for createGregaleFetch. Only explicitly used
   * decisions from this request are eligible for managed-service propagation.
   */
  propagationHeader(): string | undefined {
    const request = this.requests.getStore();
    if (!request?.customer) return undefined;
    const appID = process.env.FAAS_APP_ID ?? '';
    const decisions: PropagatedDecision[] = [];
    for (const evidence of request.evidence.values()) {
      if (!evidence.used) continue;
      const inherited = evidence.source === 'inherited' ? evidence.inherited_from : undefined;
      const origin = inherited ?? (UUID.test(appID) && request.bundle && UUID.test(request.bundle.environment_id)
        ? { app_id: appID, environment_id: request.bundle.environment_id }
        : undefined);
      if (!origin) continue;
      const source = evidence.source === 'inherited'
        ? (isFallbackReason(evidence.reason) ? 'fallback' : 'configuration')
        : evidence.source;
      const { used: _used, source: _source, inherited_from: _inheritedFrom, ...decision } = evidence;
      decisions.push({ ...decision, source, origin } as PropagatedDecision);
    }
    if (decisions.length === 0) return undefined;
    decisions.sort((a, b) => a.flag.localeCompare(b.flag));
    const encoded = Buffer.from(JSON.stringify({ version: 1, customer_id: request.customer, decisions })).toString('base64url');
    return Buffer.byteLength(Buffer.from(encoded, 'base64url')) <= MAX_PROPAGATION_CONTEXT_BYTES && encoded.length <= MAX_PROPAGATION_CONTEXT_HEADER_BYTES
      ? encoded
      : undefined;
  }
}

const MAX_PROPAGATION_CONTEXT_BYTES = 8 * 1024;
const MAX_PROPAGATION_CONTEXT_HEADER_BYTES = 12 * 1024;

function isFallbackReason(reason: string): boolean {
  return reason === 'flag_missing' || reason === 'configuration_stale' || reason === 'type_mismatch';
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function hasOnlyKeys(value: Record<string, unknown>, allowed: string[]): boolean {
  return Object.keys(value).every(key => allowed.includes(key));
}

function decodePropagationHeader(value: string | null): { customer_id: string; decisions: Map<string, PropagatedDecision> } | undefined {
  if (!value || value.length > MAX_PROPAGATION_CONTEXT_HEADER_BYTES) return undefined;
  try {
    const raw = Buffer.from(value, 'base64url');
    if (raw.length === 0 || raw.length > MAX_PROPAGATION_CONTEXT_BYTES || raw.toString('base64url') !== value) return undefined;
    const envelope: unknown = JSON.parse(raw.toString('utf8'));
    if (!isRecord(envelope) || !hasOnlyKeys(envelope, ['version', 'customer_id', 'decisions']) || envelope.version !== 1 || typeof envelope.customer_id !== 'string' || !UUID.test(envelope.customer_id) || !Array.isArray(envelope.decisions) || envelope.decisions.length === 0 || envelope.decisions.length > 32) return undefined;
    const decisions = new Map<string, PropagatedDecision>();
    const reasons = new Set(['flag_missing', 'default', 'disabled', 'customer_missing', 'rule_match', 'configuration_stale', 'type_mismatch']);
    const decisionKeys = ['flag', 'value', 'type', 'config_version', 'rule_id', 'reason', 'bucket', 'rollout_bucket', 'source', 'origin'];
    for (const rawDecision of envelope.decisions) {
      if (!isRecord(rawDecision) || !hasOnlyKeys(rawDecision, decisionKeys)) return undefined;
      const d = rawDecision;
      if (typeof d.flag !== 'string' || !FLAG_KEY.test(d.flag) || decisions.has(d.flag) || !Number.isSafeInteger(d.config_version) || (d.config_version as number) < 0 || !Number.isSafeInteger(d.config_version) || (d.config_version as number) > Number.MAX_SAFE_INTEGER || typeof d.reason !== 'string' || !reasons.has(d.reason) || (d.source !== 'configuration' && d.source !== 'fallback')) return undefined;
      const isVariant = d.type === 'variant';
      if (d.type !== undefined && d.type !== 'boolean' && !isVariant) return undefined;
      if (isVariant ? typeof d.value !== 'string' || !FLAG_KEY.test(d.value) : typeof d.value !== 'boolean') return undefined;
      if (d.rule_id !== undefined && (typeof d.rule_id !== 'string' || !FLAG_KEY.test(d.rule_id))) return undefined;
      for (const bucket of [d.bucket, d.rollout_bucket]) if (bucket !== undefined && (!Number.isInteger(bucket) || (bucket as number) < 0 || (bucket as number) >= 10000)) return undefined;
      const fallbackReason = isFallbackReason(d.reason);
      if ((d.source === 'fallback') !== fallbackReason) return undefined;
      if (d.reason === 'rule_match' ? typeof d.rule_id !== 'string' : d.rule_id !== undefined || d.bucket !== undefined || d.rollout_bucket !== undefined) return undefined;
      if (!isRecord(d.origin) || !hasOnlyKeys(d.origin, ['app_id', 'environment_id']) || typeof d.origin.app_id !== 'string' || !UUID.test(d.origin.app_id) || typeof d.origin.environment_id !== 'string' || !UUID.test(d.origin.environment_id)) return undefined;
      const { origin: rawOrigin, ...decision } = d;
      decisions.set(d.flag, { ...decision, origin: rawOrigin } as unknown as PropagatedDecision);
    }
    return { customer_id: envelope.customer_id.toLowerCase(), decisions };
  } catch {
    return undefined;
  }
}

async function boundedJSON(response: Response, max: number): Promise<unknown> {
  if (!response.body) throw new Error('Missing Flags response body');
  const reader = response.body.getReader(); const chunks: Uint8Array[] = []; let size = 0;
  try {
    for (;;) { const { value, done } = await reader.read(); if (done) break; size += value.length; if (size > max) throw new Error('Flags response too large'); chunks.push(value); }
    return JSON.parse(Buffer.concat(chunks).toString('utf8')) as unknown;
  } finally { await reader.cancel(); }
}
function validateBundle(raw: unknown): FlagsBundle {
  const b = raw as FlagsBundle;
  const key = (v: unknown): v is string => typeof v === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.test(v);
  const ids = (v: unknown): v is string[] => Array.isArray(v) && v.length <= 1000 && v.every(id => typeof id === 'string' && CUSTOMER_ID.test(id)) && new Set(v).size === v.length;
  if (!b || typeof b.environment_id !== 'string' || !CUSTOMER_ID.test(b.environment_id) || !Number.isSafeInteger(b.version) || b.version < 0 || !Array.isArray(b.flags) || b.flags.length > 100 || !b.groups || typeof b.groups !== 'object' || Array.isArray(b.groups) || Object.keys(b.groups).length > 100) throw new Error('Invalid Flags bundle');
  for (const [name, members] of Object.entries(b.groups)) if (!key(name) || !ids(members)) throw new Error('Invalid Flags group');
  const keys = new Set<string>();
  for (const f of b.flags) {
    const isVariant = f.type === 'variant';
    if (!key(f.key) || keys.has(f.key) || (f.type !== undefined && f.type !== 'boolean' && !isVariant) || typeof f.enabled !== 'boolean' || (isVariant ? typeof f.default !== 'string' : typeof f.default !== 'boolean') || typeof f.seed !== 'string' || !f.seed || f.seed.length > 128 || f.seed.includes('\0') || !Array.isArray(f.rules) || f.rules.length > 32) throw new Error('Invalid Flags definition');
    keys.add(f.key); const rules = new Set<string>();
    const variantKeys = new Set<string>();
    if (isVariant) {
      if (!Array.isArray(f.variants) || f.variants.length < 2 || f.variants.length > 16) throw new Error('Invalid Flags variants');
      let totalWeight = 0;
      for (const variant of f.variants) {
        if (!key(variant.key) || variantKeys.has(variant.key) || !Number.isInteger(variant.weight) || variant.weight < 0 || variant.weight > 10000) throw new Error('Invalid Flags variant');
        variantKeys.add(variant.key); totalWeight += variant.weight;
      }
      if (totalWeight !== 10000 || !variantKeys.has(f.default)) throw new Error('Invalid Flags variant weights or default');
    } else if ('variants' in f) throw new Error('Boolean flag cannot define variants');
    for (const r of f.rules) {
      const validRuleValue = isVariant ? (r.value === undefined || (typeof r.value === 'string' && variantKeys.has(r.value))) : typeof r.value === 'boolean';
      if (!key(r.id) || rules.has(r.id) || !validRuleValue || (r.customers !== undefined && !ids(r.customers)) || (r.group !== undefined && (!key(r.group) || !Object.hasOwn(b.groups, r.group))) || (r.rollout !== undefined && (!Number.isInteger(r.rollout) || r.rollout < 0 || r.rollout > 10000)) || (!r.customers?.length && !r.group && r.rollout === undefined)) throw new Error('Invalid Flags rule');
      rules.add(r.id);
    }
  }
  return b;
}
