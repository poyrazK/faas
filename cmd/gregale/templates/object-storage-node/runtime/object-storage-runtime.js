// Generated from sdk/node; run npm run build && npm run starter:sync.
export class ObjectStorageBindingError extends Error {
    constructor(message) { super(message); this.name = 'ObjectStorageBindingError'; }
}
function configuration(prefix, values) {
    const read = (suffix) => {
        const value = values[`${prefix}_${suffix}`];
        if (typeof value !== 'string' || !value || /[\0\r\n]/.test(value))
            throw new ObjectStorageBindingError('Object-storage binding environment is incomplete or invalid');
        return value;
    };
    const endpoint = read('ENDPOINT'), region = read('REGION'), bucket = read('BUCKET');
    const accessKeyId = read('ACCESS_KEY_ID'), secretAccessKey = read('SECRET_ACCESS_KEY');
    let url;
    try {
        url = new URL(endpoint);
    }
    catch {
        throw new ObjectStorageBindingError('Object-storage binding configuration is invalid');
    }
    if (!url.hostname || !['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash ||
        read('ADDRESSING_STYLE') !== 'path' || /[/\\ \t?#]/.test(region + bucket))
        throw new ObjectStorageBindingError('Object-storage binding configuration is invalid');
    const credentials = Object.freeze({ accessKeyId, secretAccessKey });
    const config = { endpoint, region, forcePathStyle: true, credentials,
        requestChecksumCalculation: 'WHEN_REQUIRED', responseChecksumValidation: 'WHEN_REQUIRED' };
    // Keep credentials enumerable: S3 constructors commonly spread their options.
    // Redact ordinary JSON and Node inspection without changing factory inputs.
    Object.defineProperty(config, 'toJSON', { value: () => '[ObjectStorageClientConfig redacted]' });
    Object.defineProperty(config, Symbol.for('nodejs.util.inspect.custom'), { value: () => '[ObjectStorageClientConfig redacted]' });
    return { config: Object.freeze(config), bucket };
}
export class ObjectStorageBinding {
    #prefix;
    #factory;
    #active;
    #pending = Promise.resolve();
    #epoch = 0;
    #retire;
    #onRetireError;
    #retired = new Set();
    #waiters = new Set();
    #building = 0;
    #retirementFailed = false;
    constructor(prefix, factory, options = {}) {
        if (!/^[A-Z][A-Z0-9_]{0,47}$/.test(prefix) || typeof factory !== 'function' || !options ||
            options.retire !== undefined && typeof options.retire !== 'function' || options.onRetireError !== undefined && typeof options.onRetireError !== 'function')
            throw new ObjectStorageBindingError('Invalid object-storage binding');
        this.#prefix = prefix;
        this.#factory = factory;
        this.#retire = options.retire;
        this.#onRetireError = options.onRetireError;
    }
    /** Use one atomic secret envelope. ACK its revision only after this resolves
     * and all other consumers apply. Acquire leases through stream closure.
     * Without a retirement callback, current() retains caller-owned behavior.
     * Failure disables new acquisition, including revocation. */
    reload(values, signal) {
        const snapshot = { ...values };
        const epoch = this.#epoch;
        const apply = async () => {
            if (epoch !== this.#epoch)
                throw new ObjectStorageBindingError('Object-storage reload cancelled');
            this.#building++;
            this.#clearActive();
            try {
                if (signal?.aborted)
                    throw new ObjectStorageBindingError('Object-storage reload cancelled');
                const { config, bucket } = configuration(this.#prefix, snapshot);
                let client;
                try {
                    client = await this.#factory(config, bucket, signal);
                }
                catch {
                    throw new ObjectStorageBindingError('Object-storage client could not be configured');
                }
                if (client === undefined || client === null)
                    throw new ObjectStorageBindingError('Object-storage client is unavailable');
                if (this.#retire && [...this.#retired].some(entry => Object.is(entry.pair.client, client))) {
                    throw new ObjectStorageBindingError('Object-storage factory reused an owned client');
                }
                const candidate = { pair: Object.freeze({ client, bucket }), leases: 0, retired: false, scheduled: false };
                if (signal?.aborted || epoch !== this.#epoch) {
                    this.#retireClient(candidate);
                    throw new ObjectStorageBindingError('Object-storage reload cancelled');
                }
                this.#active = candidate;
            }
            finally {
                this.#building--;
                this.#notify();
            }
        };
        const result = this.#pending.then(apply);
        this.#pending = result.catch(() => { });
        return result;
    }
    /** Disable new acquisition and fence in-progress/queued candidates. Existing
     * outstanding leases keep their clients alive. A future reload may recover. */
    invalidate() {
        this.#epoch++;
        this.#clearActive();
    }
    /** Register with SecretReloadCoordinator alongside every other secret user. */
    secretConsumer() {
        const consumer = {
            apply: (snapshot, signal) => this.reload(snapshot.secrets, signal),
            invalidate: () => this.invalidate(),
        };
        return Object.freeze(consumer);
    }
    current() {
        if (!this.#active)
            throw new ObjectStorageBindingError('Object-storage binding is not ready');
        if (this.#retire)
            throw new ObjectStorageBindingError('Object-storage binding requires lease acquisition for managed retirement');
        return this.#active.pair;
    }
    acquire() {
        const entry = this.#active;
        if (!entry)
            throw new ObjectStorageBindingError('Object-storage binding is not ready');
        entry.leases++;
        let released = false;
        const lease = {
            current: () => {
                if (released)
                    throw new ObjectStorageBindingError('Object-storage lease is released');
                return entry.pair;
            },
            release: () => {
                if (released)
                    return;
                released = true;
                entry.leases--;
                this.#scheduleRetirement(entry);
            },
        };
        Object.defineProperty(lease, 'toJSON', { value: () => '[ObjectStorageLease redacted]' });
        Object.defineProperty(lease, Symbol.for('nodejs.util.inspect.custom'), { value: () => '[ObjectStorageLease redacted]' });
        return Object.freeze(lease);
    }
    /** Wait for admitted factories and retired leases/cleanup, ignoring the current
     * client. Stop serving/reloading and invalidate first for shutdown. Cancelling
     * only ends this wait. Callback failures are sticky; uncertain cleanup is not retried. */
    async waitForRetirement(signal) {
        while (true) {
            if (signal?.aborted)
                throw new ObjectStorageBindingError('Object-storage retirement wait cancelled');
            if (this.#building === 0 && this.#retired.size === 0) {
                if (this.#retirementFailed)
                    throw new ObjectStorageBindingError('Object-storage client retirement failed');
                return;
            }
            await new Promise((resolve, reject) => {
                const cleanup = () => { this.#waiters.delete(changed); signal?.removeEventListener('abort', aborted); };
                const changed = () => { cleanup(); resolve(); };
                const aborted = () => { cleanup(); reject(new ObjectStorageBindingError('Object-storage retirement wait cancelled')); };
                this.#waiters.add(changed);
                signal?.addEventListener('abort', aborted, { once: true });
                if (signal?.aborted)
                    aborted();
            });
        }
    }
    #notify() {
        const waiters = [...this.#waiters];
        this.#waiters.clear();
        for (const wake of waiters)
            wake();
    }
    #clearActive() {
        if (!this.#active)
            return;
        const entry = this.#active;
        this.#active = undefined;
        this.#retireClient(entry);
    }
    #retireClient(entry) {
        if (entry.retired)
            return;
        entry.retired = true;
        this.#retired.add(entry);
        this.#scheduleRetirement(entry);
        this.#notify();
    }
    #scheduleRetirement(entry) {
        if (!entry.retired || entry.leases !== 0 || entry.scheduled)
            return;
        entry.scheduled = true;
        if (!this.#retire) {
            this.#retired.delete(entry);
            this.#notify();
            return;
        }
        void this.#disposeClient(entry);
    }
    async #disposeClient(entry) {
        let failed = false;
        // Invoke outside the synchronous state mutation, even for a sync disposer.
        try {
            await Promise.resolve().then(() => this.#retire(entry.pair.client));
        }
        catch {
            failed = true;
        }
        this.#retired.delete(entry);
        this.#retirementFailed ||= failed;
        this.#notify();
        if (failed && this.#onRetireError) {
            try {
                await this.#onRetireError(new ObjectStorageBindingError('Object-storage client retirement failed'));
            }
            catch { /* Observer cannot poison cleanup. */ }
        }
    }
}
