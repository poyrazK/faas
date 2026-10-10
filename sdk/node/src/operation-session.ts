import { DurableOperationSubmission, type DurableOperationSubmissionClient, type OperationReceiptStore, type OperationSubmissionResume } from './operation-submission.js';
import type {
  Operation, OperationEvent, OperationList, OperationListOptions, OperationReceipt,
  OperationSubmissionFence, OperationSummary,
} from './customer-operations.js';
import { OperationHTTPError, operationSubmissionIdentity } from './customer-operations.js';

export interface OperationSessionClient<TOutput, TInput = unknown> {
  readonly apiURL?: string;
  identity?: DurableOperationSubmissionClient['identity'];
  lookupSubmission?: DurableOperationSubmissionClient['lookupSubmission'];
  start: (definition: string, input: TInput, key: string, signal?: AbortSignal, fence?: OperationSubmissionFence) => Promise<OperationReceipt>;
  list(options: OperationListOptions, signal?: AbortSignal): Promise<OperationList>;
  get(id: string, signal?: AbortSignal): Promise<Operation<TOutput>>;
  subscribe(id: string, options: { after?: number; signal: AbortSignal }): AsyncIterable<{ snapshot?: Operation<TOutput>; event?: OperationEvent }>;
  cancel(id: string, generation: number, signal?: AbortSignal): Promise<Operation<TOutput>>;
  download(id: string, artifact: string, signal?: AbortSignal): Promise<Response>;
}

export interface OperationSessionUpdate<TOutput> {
  history?: OperationSummary[];
  hasMore?: boolean;
  operation?: Operation<TOutput>;
  error?: string;
}

export interface OperationSessionOptions<TOutput, TInput = unknown> {
  client: OperationSessionClient<TOutput, TInput>;
  /** Explicit opt-in; stores only scoped pending identity and a local input hash. */
  receiptStore?: OperationReceiptStore;
  appID: string;
  scope: string;
  definitionID: string;
  name?: string;
  onChange?: (update: OperationSessionUpdate<TOutput>) => void;
}

/** One authenticated customer's feature session. Close it before changing
 * customers. Credentials and inputs are never persisted. Optional receipts retain only
 * scoped submission metadata and an acceptance acknowledgement. */
export class GregaleOperationSession<TOutput = unknown, TInput = unknown> {
  private readonly lifetime = new AbortController();
  private watch?: AbortController;
  private historyEpoch = 0;
  private selectionEpoch = 0;
  private pending?: { input: string; key: string; uncertain: boolean };
  private starting?: Promise<OperationReceipt>;
  private current?: Operation<TOutput>;
  private selectedID?: string;
  private historyRows: OperationSummary[] = [];
  private cursor?: string;
  watching?: Promise<void>;

  private readonly options: OperationSessionOptions<TOutput, TInput>;
  private readonly durable?: DurableOperationSubmission;

  constructor(options: OperationSessionOptions<TOutput, TInput>) {
    this.options = { ...options };
    if (options.receiptStore) {
      if (!options.name || !options.client.apiURL || !options.client.identity || !options.client.lookupSubmission) throw new Error('Durable receipts require a supported customer client and operation name');
      this.durable = new DurableOperationSubmission({ client: options.client as DurableOperationSubmissionClient, store: options.receiptStore, appID: options.appID, scope: options.scope, name: options.name, definitionID: options.definitionID });
    }
  }

  /** Read-only restore. An unresolved receipt needs matching input for an explicit start. */
  async resume(): Promise<OperationSubmissionResume> {
    this.active();
    if (!this.durable) return { state: 'empty' };
    const selection = this.selectionEpoch, result = await this.durable.resume(this.lifetime.signal);
    this.active();
    if (result.receipt) await this.presentAcceptance(result.receipt, selection);
    return result;
  }

  get snapshot(): Operation<TOutput> | undefined { return this.current; }
  get selected(): string | undefined { return this.selectedID; }
  get rows(): readonly OperationSummary[] { return this.historyRows; }

  private active(): void {
    if (this.lifetime.signal.aborted) throw new DOMException('Session closed', 'AbortError');
  }

  private emit(update: OperationSessionUpdate<TOutput>): void {
    if (!this.lifetime.signal.aborted) this.options.onChange?.(update);
  }

  async history({ more = false } = {}): Promise<readonly OperationSummary[]> {
    this.active();
    if (more && !this.cursor) return this.historyRows;
    const epoch = ++this.historyEpoch;
    const page = await this.options.client.list({ appID: this.options.appID, scope: this.options.scope,
      ...(this.options.name ? { name: this.options.name } : {}),
      ...(more && this.cursor ? { cursor: this.cursor } : {}),
    }, this.lifetime.signal);
    this.active();
    if (epoch !== this.historyEpoch) return this.historyRows;
    this.historyRows = [...new Map((more ? [...this.historyRows, ...page.operations] : page.operations).map(row => [row.id, row])).values()];
    this.cursor = page.next_cursor;
    this.emit({ history: this.historyRows, hasMore: Boolean(this.cursor) });
    return this.historyRows;
  }

  /** Double submissions share one promise. Uncertain responses retain the same
   * immutable input and key; no transport or notification error starts new work. */
  start(input: TInput, idempotencyKey?: string): Promise<OperationReceipt> {
    try {
      this.active();
      const encoded = JSON.stringify(input);
      if (encoded === undefined) throw new Error('Operation input must be JSON');
      const immutable = JSON.stringify(JSON.parse(encoded), (_key, value: unknown) => {
        if (value && typeof value === 'object' && !Array.isArray(value)) {
          return Object.fromEntries(Object.entries(value).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0));
        }
        return value;
      });
      if (this.pending && (this.pending.input !== immutable || (idempotencyKey !== undefined && this.pending.key !== idempotencyKey))) {
        throw new Error('Retry the pending submission with the same input and idempotency key first');
      }
      if (this.starting) return this.starting;
      if (this.durable) {
        if (idempotencyKey !== undefined) operationSubmissionIdentity(this.options.definitionID, idempotencyKey);
        const selection = this.selectionEpoch;
        this.pending ??= { input: immutable, key: idempotencyKey ?? '', uncertain: false };
        this.starting = this.durable.start(immutable!, idempotencyKey, this.lifetime.signal).then(async receipt => {
          this.active(); await this.presentAcceptance(receipt, selection); this.pending = undefined; return receipt;
        }).catch(error => { this.pending = undefined; throw error; }).finally(() => { this.starting = undefined; });
        return this.starting;
      }
      const key = this.pending?.key ?? idempotencyKey ?? crypto.randomUUID();
      operationSubmissionIdentity(this.options.definitionID, key);
      this.pending ??= { input: immutable, key, uncertain: false };
      const selection = this.selectionEpoch;
      this.starting = this.submit(selection).finally(() => { this.starting = undefined; });
      return this.starting;
    } catch (error) { return Promise.reject(error); }
  }

  private async submit(selection: number): Promise<OperationReceipt> {
    const pending = this.pending!;
    let receipt: OperationReceipt;
    try {
      receipt = await this.options.client.start(this.options.definitionID, JSON.parse(pending.input) as TInput, pending.key, this.lifetime.signal);
    } catch (error) {
      // A definite rejection can release a fresh request. It cannot establish
      // the outcome of an earlier lost response, even after credentials expire.
      const rejected = error instanceof OperationHTTPError && [400, 401, 403, 404, 413, 415, 422, 429].includes(error.status);
      if (rejected && !pending.uncertain) this.pending = undefined;
      else pending.uncertain = true;
      throw error;
    }
    this.active();
    await this.presentAcceptance(receipt, selection);
    this.pending = undefined;
    return receipt;
  }

  private async presentAcceptance(receipt: OperationReceipt, selection: number): Promise<void> {
    this.active();
    try {
      if (selection === this.selectionEpoch) await this.open(receipt.id);
      await this.history();
    } catch (error) {
      this.active();
      this.emit({ error: `Operation accepted (${receipt.id}). Reopen it from history: ${error instanceof Error ? error.message : 'status unavailable'}` });
    }
  }

  async open(id: string): Promise<void> {
    this.active();
    this.watch?.abort();
    const epoch = ++this.selectionEpoch;
    const watch = this.watch = new AbortController();
    this.selectedID = id;
    this.current = undefined;
    const snapshot = await this.options.client.get(id, watch.signal);
    if (watch.signal.aborted || epoch !== this.selectionEpoch) return;
    this.apply(snapshot, id);
    this.watching = this.observe(id, watch).catch(error => {
      if (!watch.signal.aborted) this.emit({ error: error instanceof Error ? error.message : 'Operation stream unavailable' });
    });
  }

  private apply(snapshot: Operation<TOutput>, id: string): void {
    if (snapshot.id !== id) throw new Error('Operation response identity changed');
    if (this.current && snapshot.latest_sequence < this.current.latest_sequence) return;
    this.current = snapshot;
    this.emit({ operation: snapshot });
  }

  private async observe(id: string, watch: AbortController): Promise<void> {
    for await (const update of this.options.client.subscribe(id, { after: this.current?.latest_sequence, signal: watch.signal })) {
      if (watch.signal.aborted) return;
      const snapshot = update.snapshot ?? await this.options.client.get(id, watch.signal);
      if (watch.signal.aborted) return;
      this.apply(snapshot, id);
    }
  }

  async refresh(): Promise<void> {
    this.active();
    const selected = this.selectedID, epoch = this.selectionEpoch;
    await this.history();
    if (selected) {
      const snapshot = await this.options.client.get(selected, this.lifetime.signal);
      this.active();
      if (epoch === this.selectionEpoch) this.apply(snapshot, selected);
    }
  }

  async cancel(): Promise<void> {
    this.active();
    const current = this.current, epoch = this.selectionEpoch;
    if (!current) return;
    const snapshot = await this.options.client.cancel(current.id, current.generation, this.lifetime.signal);
    this.active();
    if (epoch === this.selectionEpoch) this.apply(snapshot, current.id);
  }

  /** Refresh confirmed business success before using a result. Delivery state
   * never blocks result access. Closing or switching selection fences responses. */
  async result(): Promise<Operation<TOutput>> {
    this.active();
    const selected = this.selectedID, epoch = this.selectionEpoch;
    if (!selected) throw new Error('Select an operation first');
    const snapshot = await this.options.client.get(selected, this.lifetime.signal);
    this.active();
    if (epoch !== this.selectionEpoch) throw new DOMException('Session changed', 'AbortError');
    this.apply(snapshot, selected);
    if (snapshot.state !== 'succeeded') throw new Error('The operation is not confirmed complete');
    return snapshot;
  }

  async download(artifactID?: string): Promise<{ name: string; blob: Blob }> {
    const snapshot = await this.result(), epoch = this.selectionEpoch;
    const artifacts = snapshot.artifacts ?? [];
    const artifact = artifactID ? artifacts.find(file => file.id === artifactID) : artifacts.length === 1 ? artifacts[0] : undefined;
    if (!artifact) throw new Error('Select a retained result artifact');
    const response = await this.options.client.download(snapshot.id, artifact.id, this.lifetime.signal);
    const blob = await response.blob();
    this.active();
    if (epoch !== this.selectionEpoch) throw new DOMException('Session changed', 'AbortError');
    return { name: artifact.name, blob };
  }

  close(): void {
    this.lifetime.abort();
    this.watch?.abort();
    ++this.selectionEpoch;
    ++this.historyEpoch;
    this.selectedID = undefined;
    this.current = undefined;
    this.historyRows = [];
    this.cursor = undefined;
    this.pending = undefined;
  }
}
