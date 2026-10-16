// Generated from sdk/node; run npm run build && npm run starter:sync.
export class PostgresPoolError extends Error {
    code;
    constructor(code = 'database_unavailable') {
        super(code);
        this.code = code;
        this.name = 'PostgresPoolError';
    }
}
/** Keep pg's URL parser from overriding certificate and hostname verification.
 * A private CA belongs in NODE_EXTRA_CA_CERTS. Only explicit sslmode=disable
 * disables TLS (for local development). */
export function postgresPoolConfig(connectionString) {
    try {
        const url = new URL(connectionString);
        if (!['postgres:', 'postgresql:'].includes(url.protocol))
            throw new Error();
        const mode = url.searchParams.get('sslmode');
        if (mode && !['disable', 'require', 'verify-ca', 'verify-full'].includes(mode))
            throw new Error();
        for (const name of ['ssl', 'sslcert', 'sslkey', 'sslrootcert', 'uselibpqcompat']) {
            if (url.searchParams.has(name))
                throw new Error();
        }
        url.searchParams.delete('sslmode');
        return { connectionString: url.href, ssl: mode === 'disable' ? false : { rejectUnauthorized: true },
            max: 5, idleTimeoutMillis: 30_000, connectionTimeoutMillis: 5_000 };
    }
    catch {
        throw new PostgresPoolError();
    }
}
function positiveInteger(value, maximum = 2_147_483_647) {
    if (!Number.isSafeInteger(value) || value < 1 || value > maximum)
        throw new PostgresPoolError();
    return value;
}
function scope(parent, timeoutMs) {
    const controller = new AbortController();
    const abort = () => controller.abort();
    const timer = setTimeout(abort, timeoutMs);
    parent?.addEventListener('abort', abort, { once: true });
    if (parent?.aborted)
        abort();
    return { controller, signal: controller.signal, dispose() {
            clearTimeout(timer);
            parent?.removeEventListener('abort', abort);
        } };
}
function check(signal) {
    if (signal.aborted)
        throw new PostgresPoolError('database_cancelled');
}
// Always observe late completion/rejection, including an uncooperative driver.
function bounded(promise, signal) {
    return new Promise((resolve, reject) => {
        const abort = () => { signal.removeEventListener('abort', abort); reject(new PostgresPoolError('database_cancelled')); };
        if (signal.aborted)
            abort();
        else
            signal.addEventListener('abort', abort, { once: true });
        promise.then(value => { signal.removeEventListener('abort', abort); resolve(value); }, error => { signal.removeEventListener('abort', abort); reject(error); });
    });
}
export class RestoreAwarePostgresPool {
    #factory;
    #timeout;
    #lifecycleTimeout;
    #maxInFlight;
    #probe;
    #onEvent;
    #active = new Set();
    #pools = new Set();
    #owned = new WeakSet();
    #ends = new WeakMap();
    #pool;
    #state = 'ready';
    #generation = 0;
    #operation;
    #closing;
    constructor(factory, options = {}) {
        this.#factory = factory;
        this.#timeout = positiveInteger(options.timeoutMs ?? 5_000);
        this.#lifecycleTimeout = positiveInteger(options.lifecycleTimeoutMs ?? 1_500, 1_500);
        this.#maxInFlight = positiveInteger(options.maxInFlight ?? 16);
        const probe = options.readinessQuery ?? { text: 'SELECT 1' };
        if (typeof probe.text !== 'string' || !probe.text.trim())
            throw new PostgresPoolError();
        this.#probe = { text: probe.text, values: [...(probe.values ?? [])] };
        this.#onEvent = options.onEvent;
        this.#pool = this.#open();
    }
    get generation() { return this.#generation; }
    get diagnostics() {
        return Object.freeze({ state: this.#state, generation: this.#generation,
            activeRequests: this.#active.size, openPools: this.#pools.size });
    }
    #emit(type) {
        try {
            const result = this.#onEvent?.(Object.freeze({ type, ...this.diagnostics }));
            // Async observers must not introduce unhandled rejections either.
            void Promise.resolve(result).catch(() => { });
        }
        catch { /* Observers cannot affect resource ownership or readiness. */ }
    }
    #open() {
        try {
            const pool = this.#factory();
            if (this.#owned.has(pool))
                throw new Error();
            this.#owned.add(pool);
            this.#pools.add(pool);
            pool.on('error', () => this.#emit('idle_connection_lost'));
            return pool;
        }
        catch {
            throw new PostgresPoolError();
        }
    }
    #end(pool) {
        let pending = this.#ends.get(pool);
        if (!pending) {
            pending = Promise.resolve().then(() => pool.end()).then(() => { this.#pools.delete(pool); }, () => {
                this.#emit('pool_close_failed');
                throw new PostgresPoolError();
            });
            this.#ends.set(pool, pending);
        }
        return pending;
    }
    #drain() {
        this.#pool = undefined;
        return Promise.all([...this.#pools].map(pool => this.#end(pool))).then(() => undefined);
    }
    #cancelRequests() { for (const request of this.#active)
        request.controller.abort(); }
    async #transaction(target, text, values, signal, write, timeoutMs) {
        let client, released = false, disconnected = false;
        const release = (destroy = false) => {
            if (client && !released) {
                released = true;
                try {
                    client.release(destroy);
                }
                catch {
                    this.#emit('connection_release_failed');
                }
                finally {
                    client.off('error', connectionLost);
                }
            }
        };
        const connectionLost = () => {
            disconnected = true;
            this.#emit('active_connection_lost');
            release(true);
        };
        const abort = () => release(true);
        signal.addEventListener('abort', abort, { once: true });
        try {
            check(signal);
            const pending = target.connect().then(value => {
                client = value;
                // pg-pool removes its idle error listener during checkout.
                client.on('error', connectionLost);
                if (signal.aborted) {
                    release(true);
                    check(signal);
                }
                return value;
            });
            client = await bounded(pending, signal);
            const query = async (sql, params = []) => {
                check(signal);
                if (disconnected)
                    throw new PostgresPoolError();
                return bounded(client.query(sql, params), signal);
            };
            await query(write ? 'BEGIN' : 'BEGIN READ ONLY');
            // Transaction-local settings also work with transaction-mode poolers.
            await query("SELECT set_config('statement_timeout', $1, true)", [`${timeoutMs}ms`]);
            const result = await query(text, values);
            await query('COMMIT');
            check(signal);
            if (disconnected)
                throw new PostgresPoolError();
            return result;
        }
        catch {
            release(true); // A disconnect rolls back work that has not committed.
            throw new PostgresPoolError(signal.aborted ? 'database_cancelled' : 'database_unavailable');
        }
        finally {
            signal.removeEventListener('abort', abort);
            release();
        }
    }
    /** Execute once in a single-query transaction. A lost COMMIT response has an
     * uncertain outcome; application idempotency is required for safe retries. */
    async query(text, values = [], options = {}) {
        if (this.#state !== 'ready' || !this.#pool)
            throw new PostgresPoolError();
        if (this.#active.size >= this.#maxInFlight)
            throw new PostgresPoolError('database_busy');
        const request = scope(options.signal, this.#timeout);
        this.#active.add(request);
        try {
            return await this.#transaction(this.#pool, text, [...values], request.signal, options.write ?? false, this.#timeout);
        }
        catch (error) {
            if (request.signal.aborted)
                this.#emit('request_cancelled');
            throw error;
        }
        finally {
            this.#active.delete(request);
            request.dispose();
        }
    }
    /** Stop admission, cancel unfinished work and drain all sessions. Remains
     * checkpointed until afterRestore; repeated calls do not reopen a pool. */
    beforeCheckpoint(options = {}) { return this.#lifecycle('checkpoint', options.signal); }
    /** Used for snapshot restore AND frozen thaw, with or without a preceding
     * beforeCheckpoint. Only a fresh pool with a successful read probe is served. */
    afterRestore(options = {}) { return this.#lifecycle('restore', options.signal); }
    #lifecycle(kind, signal) {
        if (this.#state === 'closed')
            return Promise.reject(new PostgresPoolError());
        if (signal?.aborted)
            return Promise.reject(new PostgresPoolError('database_cancelled'));
        if (this.#operation) {
            if (this.#operation.kind !== kind)
                return Promise.reject(new PostgresPoolError('database_busy'));
            // A duplicate caller can stop waiting without cancelling the owner hook.
            return signal ? bounded(this.#operation.promise, signal) : this.#operation.promise;
        }
        if (kind === 'checkpoint' && this.#state === 'checkpointed')
            return Promise.resolve();
        this.#state = kind === 'checkpoint' ? 'checkpointing' : 'restoring';
        const request = scope(signal, this.#lifecycleTimeout);
        const promise = Promise.resolve().then(async () => {
            try {
                check(request.signal);
                await bounded(this.#drain(), request.signal);
                check(request.signal);
                if (kind === 'restore') {
                    const candidate = this.#open();
                    await this.#transaction(candidate, this.#probe.text, this.#probe.values, request.signal, false, this.#lifecycleTimeout);
                    check(request.signal);
                    this.#pool = candidate;
                    this.#generation += 1;
                    this.#state = 'ready';
                }
                else
                    this.#state = 'checkpointed';
                this.#emit(kind === 'restore' ? 'restore_succeeded' : 'checkpoint_succeeded');
            }
            catch {
                if (this.#state !== 'closed')
                    this.#state = 'unavailable';
                void this.#drain().catch(() => { });
                this.#emit(kind === 'restore' ? 'restore_failed' : 'checkpoint_failed');
                throw new PostgresPoolError();
            }
        }).finally(() => { request.dispose(); this.#operation = undefined; });
        this.#operation = { kind, request, promise };
        this.#cancelRequests();
        return promise;
    }
    /** Terminal, idempotent and bounded by lifecycleTimeoutMs. */
    close() {
        if (this.#closing)
            return this.#closing;
        this.#state = 'closed';
        this.#operation?.request.controller.abort();
        this.#cancelRequests();
        const request = scope(undefined, this.#lifecycleTimeout);
        this.#closing = bounded(this.#drain(), request.signal).catch(() => { throw new PostgresPoolError(); })
            .finally(() => request.dispose());
        return this.#closing;
    }
}
export function createPostgresPool(factory, options = {}) {
    return new RestoreAwarePostgresPool(factory, options);
}
