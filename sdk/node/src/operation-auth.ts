/** Supplies a current customer Operations token and reports identity changes. */
export interface CustomerOperationAuthProvider {
  getCredential: () => string | Promise<string>;
  onIdentityChange: (listener: () => void) => () => void;
}

export interface CustomerOperationAuthOptions {
  /** Application login adapter. Omit it only for a development token form. */
  provider?: CustomerOperationAuthProvider | null;
  /** Read the in-memory development credential when no provider is configured. */
  fallbackCredential?: () => string | Promise<string>;
}

/** Resolves fresh credentials and safely owns the application's identity listener. */
export class CustomerOperationAuth {
  private readonly provider: CustomerOperationAuthProvider | null;
  private readonly fallbackCredential: () => string | Promise<string>;

  constructor(options: CustomerOperationAuthOptions = {}) {
    const provider = options.provider ?? null;
    if (provider !== null && (typeof provider.getCredential !== 'function' || typeof provider.onIdentityChange !== 'function')) {
      throw new Error('Customer Operations auth provider must supply getCredential and onIdentityChange');
    }
    this.provider = provider;
    this.fallbackCredential = options.fallbackCredential ?? (() => '');
  }

  get integrated(): boolean {
    return this.provider !== null;
  }

  async getCredential(): Promise<string> {
    const value = this.provider
      ? await this.provider.getCredential()
      : await this.fallbackCredential();
    if (typeof value !== 'string' || !value.trim()) {
      throw new Error('Customer login did not provide an Operations credential');
    }
    return value;
  }

  onIdentityChange(listener: () => void): () => void {
    if (!this.provider) return () => {};
    const unsubscribe = this.provider.onIdentityChange(listener);
    if (typeof unsubscribe !== 'function') {
      throw new Error('Customer Operations auth onIdentityChange must return an unsubscribe function');
    }
    let active = true;
    return () => {
      if (!active) return;
      active = false;
      unsubscribe();
    };
  }
}
