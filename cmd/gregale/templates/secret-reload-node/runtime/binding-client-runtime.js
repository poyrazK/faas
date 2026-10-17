// Generated from sdk/node; run npm run build && npm run starter:sync.
export class ClientBinding {
    #load;
    #error;
    #validate;
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
    constructor(load, factory, error, options) {
        this.#load = load;
        this.#factory = factory;
        this.#error = error;
        this.#validate = options.validate;
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
                throw this.#error('reload cancelled');
            this.#building++;
            this.#clearActive();
            try {
                if (signal?.aborted)
                    throw this.#error('reload cancelled');
                const { config, value } = this.#load(snapshot);
                let client;
                try {
                    client = await this.#factory(config, value, signal);
                }
                catch {
                    throw this.#error('client could not be configured');
                }
                if (client === undefined || client === null)
                    throw this.#error('client is unavailable');
                if (this.#retire && [...this.#retired].some(entry => Object.is(entry.pair.client, client))) {
                    throw this.#error('factory reused an owned client');
                }
                const candidate = { pair: Object.freeze({ client, value }), leases: 0, retired: false, scheduled: false };
                let published = false;
                try {
                    if (signal?.aborted || epoch !== this.#epoch)
                        throw this.#error('reload cancelled');
                    if (this.#validate) {
                        try {
                            const result = await this.#validate(client, signal);
                            if (result !== undefined)
                                throw this.#error('client validation failed');
                        }
                        catch {
                            throw this.#error('client validation failed');
                        }
                    }
                    if (signal?.aborted || epoch !== this.#epoch)
                        throw this.#error('reload cancelled');
                    this.#active = candidate;
                    published = true;
                }
                finally {
                    if (!published)
                        this.#retireClient(candidate);
                }
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
            throw this.#error('binding is not ready');
        if (this.#retire)
            throw this.#error('binding requires lease acquisition for managed retirement');
        return this.#active.pair;
    }
    acquire() {
        const entry = this.#active;
        if (!entry)
            throw this.#error('binding is not ready');
        entry.leases++;
        let released = false;
        const lease = {
            current: () => {
                if (released)
                    throw this.#error('lease is released');
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
        Object.defineProperty(lease, 'toJSON', { value: () => '[ClientLease redacted]' });
        Object.defineProperty(lease, Symbol.for('nodejs.util.inspect.custom'), { value: () => '[ClientLease redacted]' });
        return Object.freeze(lease);
    }
    /** Wait for admitted factories and retired leases/cleanup, ignoring the current
     * client. Stop serving/reloading and invalidate first for shutdown. Cancelling
     * only ends this wait. Callback failures are sticky; uncertain cleanup is not retried. */
    async waitForRetirement(signal) {
        while (true) {
            if (signal?.aborted)
                throw this.#error('retirement wait cancelled');
            if (this.#building === 0 && this.#retired.size === 0) {
                if (this.#retirementFailed)
                    throw this.#error('client retirement failed');
                return;
            }
            await new Promise((resolve, reject) => {
                const cleanup = () => { this.#waiters.delete(changed); signal?.removeEventListener('abort', aborted); };
                const changed = () => { cleanup(); resolve(); };
                const aborted = () => { cleanup(); reject(this.#error('retirement wait cancelled')); };
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
                await this.#onRetireError(this.#error('client retirement failed'));
            }
            catch { /* Observer cannot poison cleanup. */ }
        }
    }
}
