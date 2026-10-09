import {
  operationAPIBase, operationSubmissionIdentity, OperationHTTPError,
  type OperationReceipt, type OperationSubmissionFence, type OperationSubmissionLookup,
  type OperationSubmissionLookupOptions, type OperationTenantIdentity,
} from './customer-operations.js';

// Conservative protocol bounds mirror pkg/api/limits.go, not a customer's quota.
export const OPERATION_BROWSER_RECEIPT_MAX_BYTES = 8192;
export const OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS = 86400;
const MAX_INPUT = 1 << 20;

export interface OperationReceiptScope {
  api: string; accountID: string; tenantID: string; appID: string; scope: string; name: string;
}
export interface SavedOperationSubmission {
  version: 1; scope: OperationReceiptScope; definitionID: string; key: string;
  inputFingerprint: string; createdAt: string; replayNotAfter: string;
  acknowledgement?: OperationReceipt;
}
export interface OperationReceiptAccess {
  load(): unknown;
  save(receipt: SavedOperationSubmission): void;
  remove(): void;
}
/** Hold an exclusive lock through all reads, saves and awaited requests in action.
 * save must finish durably before returning; failures must reject the action. */
export interface OperationReceiptStore {
  withLock<T>(key: string, action: (access: OperationReceiptAccess) => Promise<T>): Promise<T>;
}
export interface BrowserOperationReceiptStoreOptions {
  storage?: Storage;
  locks?: Pick<LockManager, 'request'>;
  namespace?: string;
}

/** Explicit opt-in. A browser without storage or Web Locks fails before POST. */
export function createBrowserOperationReceiptStore(options: BrowserOperationReceiptStoreOptions = {}): OperationReceiptStore {
  const namespace = options.namespace ?? 'gregale.operation.submission.v1';
  if (!/^[a-zA-Z0-9._-]{1,128}$/.test(namespace)) throw new Error('Invalid operation receipt namespace');
  return {
    async withLock<T>(key: string, action: (access: OperationReceiptAccess) => Promise<T>): Promise<T> {
      const storage = options.storage ?? globalThis.localStorage;
      const locks = options.locks ?? globalThis.navigator?.locks;
      if (!storage || !locks || typeof locks.request !== 'function') throw new Error('Durable operation receipts require browser storage and Web Locks');
      const address = `${namespace}:${key}`;
      return locks.request(address, async () => action({
        load() {
          const raw = storage.getItem(address);
          if (raw === null) return undefined;
          if (new TextEncoder().encode(raw).length > OPERATION_BROWSER_RECEIPT_MAX_BYTES) throw new Error('Operation receipt exceeds its size bound');
          const value: unknown = JSON.parse(raw);
          // Only our canonical JSON is accepted: duplicate members and partially
          // edited metadata must not become another submission's identity.
          if (JSON.stringify(value) !== raw) throw new Error('Corrupt operation receipt');
          return value;
        },
        save(receipt) {
          const raw = JSON.stringify(receipt);
          if (new TextEncoder().encode(raw).length > OPERATION_BROWSER_RECEIPT_MAX_BYTES) throw new Error('Operation receipt exceeds its size bound');
          storage.setItem(address, raw);
          if (storage.getItem(address) !== raw) throw new Error('Operation receipt could not be saved durably');
        },
        remove() { storage.removeItem(address); },
      }));
    },
  };
}

function uuid(value: unknown): string {
  if (typeof value !== 'string' || !/^(?:[0-9a-fA-F]{32}|[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12})$/.test(value)) throw new Error('Invalid operation receipt identity');
  const hex = value.replaceAll('-', '').toLowerCase();
  if (/^0+$/.test(hex)) throw new Error('Invalid operation receipt identity');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
function sameScope(a: OperationReceiptScope, b: OperationReceiptScope): boolean {
  return a.api === b.api && a.accountID === b.accountID && a.tenantID === b.tenantID && a.appID === b.appID && a.scope === b.scope && a.name === b.name;
}
function exactFields(value: object, fields: string[]): boolean { return Object.keys(value).every(key => fields.includes(key)); }
function receipt(value: OperationReceipt): OperationReceipt {
  if (!value || !exactFields(value, ['id', 'status_url', 'events_url']) || uuid(value.id) !== value.id || value.status_url !== `/v1/platform-tenant-self/customer-operations/${value.id}` || value.events_url !== `${value.status_url}/events`) throw new Error('Invalid operation acceptance receipt');
  return { ...value };
}
function validateSaved(value: unknown, scope: OperationReceiptScope): SavedOperationSubmission | undefined {
  if (value === undefined) return undefined;
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Corrupt operation receipt');
  const r = value as SavedOperationSubmission;
  if (!exactFields(r, ['version', 'scope', 'definitionID', 'key', 'inputFingerprint', 'createdAt', 'replayNotAfter', 'acknowledgement']) || r.version !== 1 || !r.scope || !exactFields(r.scope, ['api', 'accountID', 'tenantID', 'appID', 'scope', 'name']) || !sameScope(r.scope, scope) || typeof r.inputFingerprint !== 'string' || !/^[0-9a-f]{64}$/.test(r.inputFingerprint)) throw new Error('Operation receipt belongs to another customer or feature, or is corrupt');
  operationSubmissionIdentity(uuid(r.definitionID), r.key);
  const created = Date.parse(r.createdAt), expires = Date.parse(r.replayNotAfter);
  if (!Number.isFinite(created) || !Number.isFinite(expires) || new Date(created).toISOString() !== r.createdAt || new Date(expires).toISOString() !== r.replayNotAfter || expires <= created || expires - created > OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS * 1000) throw new Error('Invalid operation receipt replay window');
  if (r.acknowledgement) receipt(r.acknowledgement);
  return structuredClone(r);
}
async function sha256(value: string): Promise<string> {
  const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value));
  return Array.from(new Uint8Array(hash), byte => byte.toString(16).padStart(2, '0')).join('');
}

export interface DurableOperationSubmissionClient {
  readonly apiURL: string;
  identity(signal?: AbortSignal): Promise<OperationTenantIdentity>;
  lookupSubmission(options: OperationSubmissionLookupOptions, signal?: AbortSignal): Promise<OperationSubmissionLookup>;
  start(definition: string, input: unknown, key: string, signal?: AbortSignal, fence?: OperationSubmissionFence): Promise<OperationReceipt>;
}
export interface DurableOperationSubmissionOptions {
  client: DurableOperationSubmissionClient; store: OperationReceiptStore;
  appID: string; scope: string; name: string; definitionID: string;
}
export interface OperationSubmissionResume {
  state: 'empty' | 'unresolved' | 'accepted'; receipt?: OperationReceipt;
}

/** @internal Coordinates one feature's durable pending identity. Raw input is
 * never sent to its store. Acceptance is saved even if the UI closes meanwhile. */
export class DurableOperationSubmission {
  private resolvedKey?: string;
  private verifiedScope?: OperationReceiptScope;
  private readonly options: DurableOperationSubmissionOptions;
  constructor(options: DurableOperationSubmissionOptions) {
    if (!/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope) || !/^[a-z][a-z0-9-]{0,63}$/.test(options.name)) throw new Error('Durable receipts require explicit app, environment and operation name');
    this.options = { ...options, appID: uuid(options.appID), definitionID: uuid(options.definitionID) };
    operationAPIBase(options.client.apiURL);
  }
  private async scoped(signal: AbortSignal): Promise<{ scope: OperationReceiptScope; key: string }> {
    signal.throwIfAborted();
    const owner = await this.options.client.identity(signal);
    signal.throwIfAborted();
    const scope = { api: operationAPIBase(this.options.client.apiURL).origin, accountID: uuid(owner.account_id), tenantID: uuid(owner.platform_tenant_id), appID: this.options.appID, scope: this.options.scope, name: this.options.name };
    if (this.verifiedScope && !sameScope(this.verifiedScope, scope)) throw new Error('Authenticated customer changed; close this operation session');
    this.verifiedScope ??= scope;
    return { scope, key: await sha256(JSON.stringify(scope)) };
  }
  private fence(scope: OperationReceiptScope): OperationSubmissionFence {
    return { identity: { account_id: scope.accountID, platform_tenant_id: scope.tenantID }, scope: { app_id: scope.appID, scope: scope.scope, name: scope.name } };
  }
  private async checkIdentity(scope: OperationReceiptScope, signal: AbortSignal): Promise<void> {
    signal.throwIfAborted();
    const owner = await this.options.client.identity(signal);
    signal.throwIfAborted();
    if (uuid(owner.account_id) !== scope.accountID || uuid(owner.platform_tenant_id) !== scope.tenantID || operationAPIBase(this.options.client.apiURL).origin !== scope.api) throw new Error('Authenticated customer changed; close this operation session');
  }
  private async lookup(r: SavedOperationSubmission, signal: AbortSignal): Promise<OperationReceipt | undefined> {
    await this.checkIdentity(r.scope, signal);
    const result = await this.options.client.lookupSubmission({ ...this.fence(r.scope).scope, idempotency_key: r.key, expected_identity: this.fence(r.scope).identity }, signal);
    signal.throwIfAborted();
    if (result.state === 'expired') throw new Error('Saved submission has expired; inspect retained history before choosing new work');
    if (result.state === 'unresolved') {
      if (r.acknowledgement) throw new Error('Saved acceptance is unavailable; inspect retained history');
      return undefined;
    }
    if (result.state !== 'accepted' || !result.receipt || typeof result.accepted_at !== 'string' || !Number.isFinite(Date.parse(result.accepted_at)) || Date.parse(result.accepted_at) > Date.parse(r.replayNotAfter)) throw new Error('Invalid or replaced operation submission receipt');
    const accepted = receipt(result.receipt);
    if (r.acknowledgement && r.acknowledgement.id !== accepted.id) throw new Error('Operation submission identity was replaced');
    return accepted;
  }
  async resume(signal: AbortSignal): Promise<OperationSubmissionResume> {
    const { scope, key } = await this.scoped(signal);
    return this.options.store.withLock(key, async access => {
      signal.throwIfAborted();
      const saved = validateSaved(access.load(), scope);
      if (!saved) return { state: 'empty' };
      const accepted = await this.lookup(saved, signal);
      if (!accepted) return { state: 'unresolved' };
      access.save({ ...saved, acknowledgement: accepted });
      this.resolvedKey = saved.key;
      return { state: 'accepted', receipt: accepted };
    });
  }
  async start(input: string, idempotencyKey: string | undefined, signal: AbortSignal): Promise<OperationReceipt> {
    if (new TextEncoder().encode(input).length > MAX_INPUT) throw new Error('Operation input exceeds its size bound');
    if (idempotencyKey !== undefined) operationSubmissionIdentity(this.options.definitionID, idempotencyKey);
    const fingerprint = await sha256(input), { scope, key } = await this.scoped(signal);
    return this.options.store.withLock(key, async access => {
      signal.throwIfAborted();
      let saved = validateSaved(access.load(), scope), fresh = false;
      if (saved?.acknowledgement && saved.key === this.resolvedKey && (idempotencyKey === undefined || idempotencyKey !== saved.key)) saved = undefined;
      if (saved) {
        if (saved.inputFingerprint !== fingerprint || (idempotencyKey !== undefined && idempotencyKey !== saved.key)) throw new Error('Resolve the saved submission or retry with the same input and idempotency key first');
        const accepted = await this.lookup(saved, signal);
        if (accepted) {
          access.save({ ...saved, acknowledgement: accepted }); this.resolvedKey = saved.key; return accepted;
        }
      } else {
        const now = Date.now();
        saved = { version: 1, scope, definitionID: this.options.definitionID, key: idempotencyKey ?? crypto.randomUUID(), inputFingerprint: fingerprint, createdAt: new Date(now).toISOString(), replayNotAfter: new Date(now + OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS * 1000).toISOString() };
        operationSubmissionIdentity(saved.definitionID, saved.key);
        access.save(saved); fresh = true;
      }
      if (Date.now() < Date.parse(saved.createdAt) || Date.now() >= Date.parse(saved.replayNotAfter)) throw new Error('Unconfirmed operation receipt expired; inspect retained history before choosing new work');
      await this.checkIdentity(scope, signal);
      let accepted: OperationReceipt;
      try { accepted = receipt(await this.options.client.start(saved.definitionID, JSON.parse(input) as unknown, saved.key, signal, this.fence(scope))); }
      catch (error) {
        // A confirmed rejection of this first attempt can release the slot.
        // Any request loaded from storage may have an earlier uncertain effect.
        if (fresh && error instanceof OperationHTTPError && [400, 401, 403, 404, 413, 415, 422, 429].includes(error.status)) access.remove();
        throw error;
      }
      access.save({ ...saved, acknowledgement: accepted }); this.resolvedKey = saved.key;
      return accepted;
    });
  }
}
