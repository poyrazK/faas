import { GregaleOperationClient } from './customer-operations.js';
import { CustomerOperationAuth, type CustomerOperationAuthProvider } from './operation-auth.js';
import { GregaleOperationSession, type OperationSessionUpdate } from './operation-session.js';
import type { OperationReceiptStore, OperationSubmissionResume } from './operation-submission.js';

export interface CustomerOperationFeatureOptions<TInput = unknown, TOutput = unknown> {
  apiURL: string;
  appID: string;
  scope: string;
  definitionID: string;
  name?: string;
  provider?: CustomerOperationAuthProvider | null;
  fallbackCredential?: () => string | Promise<string>;
  receiptStore?: OperationReceiptStore;
  fetch?: typeof globalThis.fetch;
  onChange?: (update: OperationSessionUpdate<TOutput>) => void;
  /** Called after the active session is closed when the host identity changes. */
  onIdentityChange?: () => void;
}

export interface CustomerOperationFeatureConnection<TInput = unknown, TOutput = unknown> {
  session: GregaleOperationSession<TOutput, TInput>;
  restored: OperationSubmissionResume;
}

/** Owns the authenticated lifecycle shared by customer-facing Operations features.
 * UI code still controls rendering and explicit business actions through `session`. */
export class CustomerOperationFeature<TInput = unknown, TOutput = unknown> {
  private readonly options: CustomerOperationFeatureOptions<TInput, TOutput>;
  private readonly auth: CustomerOperationAuth;
  private readonly unsubscribeIdentity: () => void;
  private activeSession?: GregaleOperationSession<TOutput, TInput>;
  private epoch = 0;
  private disposed = false;

  constructor(options: CustomerOperationFeatureOptions<TInput, TOutput>) {
    this.options = { ...options };
    this.auth = new CustomerOperationAuth({
      provider: options.provider,
      fallbackCredential: options.fallbackCredential,
    });
    this.unsubscribeIdentity = this.auth.onIdentityChange(() => this.identityChanged());
  }

  get integrated(): boolean { return this.auth.integrated; }
  get connected(): boolean { return this.activeSession !== undefined; }
  get session(): GregaleOperationSession<TOutput, TInput> | undefined { return this.activeSession; }

  /** Connects the current customer, loads history and resumes a saved receipt.
   * Returns undefined when a newer connection or identity change supersedes it. */
  async connect(): Promise<CustomerOperationFeatureConnection<TInput, TOutput> | undefined> {
    if (this.disposed) throw new Error('Customer Operations feature has been disposed');
    this.closeSession();
    const epoch = ++this.epoch;

    await this.auth.getCredential();
    if (!this.current(epoch)) return undefined;

    const client = new GregaleOperationClient({
      apiURL: this.options.apiURL,
      credential: () => this.auth.getCredential(),
      ...(this.options.fetch ? { fetch: this.options.fetch } : {}),
    });
    const session = new GregaleOperationSession<TOutput, TInput>({
      client,
      appID: this.options.appID,
      scope: this.options.scope,
      definitionID: this.options.definitionID,
      ...(this.options.name ? { name: this.options.name } : {}),
      ...(this.options.receiptStore ? { receiptStore: this.options.receiptStore } : {}),
      onChange: update => {
        if (this.current(epoch, session)) this.options.onChange?.(update);
      },
    });
    this.activeSession = session;

    try {
      await session.history();
      if (!this.current(epoch, session)) return undefined;
      const restored = await session.resume();
      if (!this.current(epoch, session)) return undefined;
      return { session, restored };
    } catch (error) {
      if (this.current(epoch, session)) this.closeSession();
      throw error;
    }
  }

  /** Closes the current session while keeping the host identity listener active. */
  close(): void {
    ++this.epoch;
    this.closeSession();
  }

  /** Releases the session and the host application's identity subscription. */
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.close();
    this.unsubscribeIdentity();
  }

  private identityChanged(): void {
    if (this.disposed) return;
    this.close();
    this.options.onIdentityChange?.();
  }

  private closeSession(): void {
    const previous = this.activeSession;
    this.activeSession = undefined;
    previous?.close();
  }

  private current(epoch: number, session?: GregaleOperationSession<TOutput, TInput>): boolean {
    return !this.disposed && epoch === this.epoch && (session === undefined || this.activeSession === session);
  }
}
