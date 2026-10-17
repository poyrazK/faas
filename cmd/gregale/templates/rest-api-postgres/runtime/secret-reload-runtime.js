// Generated from sdk/node; run npm run build && npm run starter:sync.
import { readFile, writeFile } from 'node:fs/promises';
import { request as requestHTTP } from 'node:http';
import { request as requestHTTPS } from 'node:https';
import { setTimeout as sleep } from 'node:timers/promises';
export class SecretReloadError extends Error {
    constructor(message) { super(message); this.name = 'SecretReloadError'; }
}
const revisionPattern = /^[0-9a-f]{64}$/;
const cancelled = () => new SecretReloadError('Secret reload cancelled');
function checkSignal(signal) { if (signal.aborted)
    throw cancelled(); }
// Use a dedicated transport, not global fetch: FaaSClient and applications can
// wrap fetch with API retries, logging and Problem decoding that hide a 409 ACK.
function requestAck(endpoint, body, signal) {
    return new Promise((resolve, reject) => {
        const request = (endpoint.startsWith('https:') ? requestHTTPS : requestHTTP)(endpoint, {
            method: 'POST', agent: false, signal,
            headers: { 'content-type': 'application/json', 'content-length': Buffer.byteLength(body) },
        }, response => {
            const status = response.statusCode ?? 500;
            response.destroy(); // Only status is authoritative; never parse or drain an unbounded body.
            resolve(status);
        });
        request.on('error', reject);
        request.end(body);
    });
}
// Observe shutdown even if a reader/factory ignores the signal. Shutdown is
// terminal: no subsequent callback can overlap a cancelled application call.
function abortable(operation, signal) {
    checkSignal(signal);
    return new Promise((resolve, reject) => {
        const aborted = () => { reject(cancelled()); };
        signal.addEventListener('abort', aborted, { once: true });
        Promise.resolve().then(() => { checkSignal(signal); return operation(); }).then(resolve, reject)
            .finally(() => signal.removeEventListener('abort', aborted));
    });
}
/** Node-only coordinator for Gregale's atomic snapshot + generation-fenced ACK
 * protocol. Construct once per workload process. start() installs SIGHUP before
 * readiness, then resolves only after all consumers apply and the host accepts
 * the exact revision. It never mutates process.env or exits the process. */
export class SecretReloadCoordinator {
    #consumers;
    #snapshotPath;
    #readyPath;
    #endpoint;
    #generation;
    #startupTimeout;
    #externalSignal;
    #onError;
    #read;
    #ready;
    #request;
    #sleep;
    #shutdown = new AbortController();
    #started = false;
    #stopped = false;
    #initial;
    #pending = Promise.resolve();
    #hangupPending = false;
    #hangupRunning = false;
    #onAbort = () => this.stop();
    #onHangup = () => {
        this.#hangupPending = true;
        if (!this.#hangupRunning)
            void this.#refreshFromSignal();
    };
    async #refreshFromSignal() {
        this.#hangupRunning = true;
        try {
            do {
                this.#hangupPending = false;
                try {
                    await this.reload();
                }
                catch {
                    if (!this.#stopped) {
                        try {
                            this.#onError?.(new SecretReloadError('Secret reload could not be completed'));
                        }
                        catch { /* Never log callback errors. */ }
                    }
                }
            } while (this.#hangupPending && !this.#stopped);
        }
        finally {
            this.#hangupRunning = false;
        }
    }
    constructor(options) {
        if (!options.consumers?.length || options.consumers.some(c => !c || typeof c.apply !== 'function' || typeof c.invalidate !== 'function')) {
            throw new SecretReloadError('Secret consumers are unavailable');
        }
        // Capture callbacks and process identity once; later environment mutation
        // must never lend an old process the next execution's generation.
        this.#consumers = options.consumers.map(c => Object.freeze({ apply: c.apply.bind(c), invalidate: c.invalidate.bind(c) }));
        const env = options.env ?? process.env;
        const path = (key) => {
            const value = env[key];
            if (typeof value !== 'string' || !value.startsWith('/') || /[\0\r\n]/.test(value))
                throw new SecretReloadError('Atomic secret reload paths are unavailable');
            return value;
        };
        this.#snapshotPath = path('FAAS_SECRETS_SNAPSHOT_FILE');
        this.#readyPath = path('FAAS_SECRETS_RELOAD_READY_FILE');
        const endpoint = env.FAAS_SECRETS_RELOAD_ACK_ENDPOINT;
        let url;
        try {
            if (typeof endpoint !== 'string' || /[\0\r\n]/.test(endpoint))
                throw new Error();
            url = new URL(endpoint);
            if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.hash)
                throw new Error();
        }
        catch {
            throw new SecretReloadError('Secret acknowledgement endpoint is unavailable');
        }
        this.#endpoint = url.href;
        const generation = env.FAAS_SECRETS_RELOAD_GENERATION;
        if (typeof generation !== 'string' || !/^[0-9a-f]{32}$/.test(generation))
            throw new SecretReloadError('Secret execution identity is unavailable');
        this.#generation = generation;
        this.#startupTimeout = options.startupTimeoutMs ?? 30_000;
        if (!Number.isSafeInteger(this.#startupTimeout) || this.#startupTimeout <= 0 || this.#startupTimeout > 2_147_483_647)
            throw new SecretReloadError('Initial secret wait deadline is invalid');
        this.#externalSignal = options.signal;
        this.#onError = options.onError;
        this.#read = options.io?.read ?? ((file, signal) => readFile(file, { encoding: 'utf8', signal }));
        this.#ready = options.io?.ready ?? ((file, signal) => writeFile(file, 'ready\n', { mode: 0o600, signal }));
        const fetch = options.io?.fetch;
        this.#request = fetch ? async (body, signal) => {
            const response = await fetch(this.#endpoint, {
                method: 'POST', headers: { 'content-type': 'application/json' }, body, signal, redirect: 'error',
            });
            void response.body?.cancel().catch(() => { });
            return response.status;
        } : (body, signal) => requestAck(this.#endpoint, body, signal);
        this.#sleep = options.io?.sleep ?? ((ms, signal) => sleep(ms, undefined, { signal }));
    }
    start() {
        if (this.#stopped || this.#externalSignal?.aborted) {
            this.stop();
            return Promise.reject(cancelled());
        }
        if (this.#initial)
            return this.#initial;
        this.#started = true;
        process.on('SIGHUP', this.#onHangup);
        this.#externalSignal?.addEventListener('abort', this.#onAbort, { once: true });
        this.#initial = this.#enqueue(true).catch(error => { this.stop(); throw error; });
        return this.#initial;
    }
    /** Manual refresh and SIGHUP share the same queue, including startup.
     * A caller signal bounds one refresh, including queueing and ACK transport.
     * Cancellation after admission disables acquisition and fences late publication. */
    reload(signal) {
        if (!this.#started || this.#stopped)
            return Promise.reject(new SecretReloadError('Secret reload is not running'));
        if (signal?.aborted)
            return Promise.reject(cancelled());
        return this.#enqueue(false, signal);
    }
    /** Terminal shutdown. Detaches only this coordinator's handler and disables
     * registered consumers; it does not destroy acquired clients or await them. */
    stop() {
        if (this.#stopped)
            return;
        this.#stopped = true;
        this.#shutdown.abort();
        process.removeListener('SIGHUP', this.#onHangup);
        this.#externalSignal?.removeEventListener('abort', this.#onAbort);
        this.#disable();
    }
    #disable() {
        let success = true;
        for (const consumer of this.#consumers) {
            try {
                const result = consumer.invalidate();
                if (result !== undefined) {
                    // TS permits async callbacks where void is expected. Reject that
                    // contract at runtime without leaking an unhandled rejection.
                    void Promise.resolve(result).catch(() => { });
                    success = false;
                }
            }
            catch {
                success = false;
            }
        }
        return success;
    }
    #enqueue(initial, callerSignal) {
        const result = this.#pending.then(async () => {
            const signal = callerSignal ? AbortSignal.any([this.#shutdown.signal, callerSignal]) : this.#shutdown.signal;
            if (callerSignal?.aborted)
                throw cancelled();
            try {
                checkSignal(signal);
                if (!this.#disable())
                    throw new SecretReloadError('Secret consumers could not be disabled');
                if (initial)
                    await abortable(() => this.#ready(this.#readyPath, signal), signal);
                return await this.#apply(initial, signal);
            }
            catch {
                this.#disable();
                checkSignal(signal);
                throw new SecretReloadError('Secret reload could not be completed');
            }
        });
        this.#pending = result.then(() => { }, () => { });
        return callerSignal ? abortable(() => result, callerSignal) : result;
    }
    async #snapshot(signal, allowPending = false) {
        for (let attempt = 0; attempt < 5; attempt++) {
            let raw;
            try {
                raw = await abortable(() => this.#read(this.#snapshotPath, signal), signal);
            }
            catch (error) {
                checkSignal(signal);
                if (error?.code === 'ENOENT' && attempt < 4)
                    continue;
                throw new SecretReloadError('Secret snapshot could not be read');
            }
            let parsed;
            try {
                parsed = JSON.parse(raw);
            }
            catch {
                throw new SecretReloadError('Secret snapshot is invalid');
            }
            if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
                throw new SecretReloadError('Secret snapshot is invalid');
            const { revision, secrets } = parsed;
            if (typeof revision !== 'string' || (!(allowPending && revision === '') && !revisionPattern.test(revision)) ||
                !secrets || typeof secrets !== 'object' || Array.isArray(secrets) ||
                Object.entries(secrets).some(([key, value]) => !key || typeof value !== 'string'))
                throw new SecretReloadError('Secret snapshot is invalid');
            const snapshot = { revision, secrets: Object.freeze({ ...secrets }) };
            // Snapshots intentionally contain secrets. Prevent incidental logging;
            // explicit field access remains available to application consumers.
            Object.defineProperty(snapshot, 'toJSON', { value: () => '[SecretSnapshot redacted]' });
            Object.defineProperty(snapshot, Symbol.for('nodejs.util.inspect.custom'), { value: () => '[SecretSnapshot redacted]' });
            return Object.freeze(snapshot);
        }
        throw new SecretReloadError('Secret snapshot could not be read');
    }
    async #initialSnapshot(signal) {
        const deadline = AbortSignal.timeout(this.#startupTimeout);
        const waitSignal = AbortSignal.any([signal, deadline]);
        let backoff = 100;
        for (;;) {
            const snapshot = await this.#snapshot(waitSignal, true);
            if (snapshot.revision)
                return snapshot;
            await abortable(() => this.#sleep(backoff, waitSignal), waitSignal);
            backoff = Math.min(2000, backoff * 2);
        }
    }
    async #ack(revision, status, signal) {
        const body = JSON.stringify({ revision, status, generation: this.#generation });
        for (let attempt = 0; attempt < 10; attempt++) {
            let statusCode;
            const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(5000)]);
            try {
                statusCode = await abortable(() => this.#request(body, requestSignal), requestSignal);
            }
            catch {
                checkSignal(signal);
            }
            checkSignal(signal);
            if (statusCode === 202)
                return 'accepted';
            if (statusCode === 409)
                return 'stale';
            if ((statusCode !== undefined && statusCode !== 503) || attempt === 9)
                break;
            await abortable(() => this.#sleep(Math.min(2000, 100 * 2 ** attempt), signal), signal);
        }
        throw new SecretReloadError('Secret acknowledgement could not be confirmed');
    }
    async #apply(initial, signal) {
        for (let attempt = 0; attempt <= 4; attempt++) {
            if (!this.#disable())
                throw new SecretReloadError('Secret consumers could not be disabled');
            const snapshot = initial && attempt === 0 ? await this.#initialSnapshot(signal) : await this.#snapshot(signal);
            let status = 'applied';
            try {
                for (const consumer of this.#consumers) {
                    await abortable(() => consumer.apply(snapshot, signal), signal);
                    checkSignal(signal);
                }
            }
            catch {
                this.#disable();
                checkSignal(signal);
                status = 'failed';
            }
            const outcome = await this.#ack(snapshot.revision, status, signal);
            if (outcome === 'stale')
                continue;
            if (status === 'failed')
                throw new SecretReloadError('Secret snapshot could not be applied');
            return snapshot.revision;
        }
        throw new SecretReloadError('Secret projection changed too many times');
    }
}
