// Generated from sdk/node; run npm run build && npm run starter:sync.
import { ClientBinding } from './binding-client-runtime.js';
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
    #core;
    constructor(prefix, factory, options = {}) {
        if (!/^[A-Z][A-Z0-9_]{0,47}$/.test(prefix) || typeof factory !== 'function' || !options ||
            options.retire !== undefined && typeof options.retire !== 'function' || options.onRetireError !== undefined && typeof options.onRetireError !== 'function')
            throw new ObjectStorageBindingError('Invalid object-storage binding');
        this.#core = new ClientBinding(values => { const { config, bucket } = configuration(prefix, values); return { config, value: bucket }; }, factory, message => new ObjectStorageBindingError(`Object-storage ${message}`), options);
    }
    /** Adopt one atomic envelope; register secretConsumer() for coordinated ACKs. */
    reload(values, signal) { return this.#core.reload(values, signal); }
    invalidate() { this.#core.invalidate(); }
    secretConsumer() { return this.#core.secretConsumer(); }
    current() {
        const { client, value: bucket } = this.#core.current();
        return Object.freeze({ client, bucket });
    }
    acquire() {
        const inner = this.#core.acquire();
        const lease = {
            current: () => { const { client, value: bucket } = inner.current(); return Object.freeze({ client, bucket }); },
            release: () => inner.release(),
        };
        Object.defineProperty(lease, 'toJSON', { value: () => '[ObjectStorageLease redacted]' });
        Object.defineProperty(lease, Symbol.for('nodejs.util.inspect.custom'), { value: () => '[ObjectStorageLease redacted]' });
        return Object.freeze(lease);
    }
    /** Stop requests/reloads and invalidate before draining shutdown. */
    waitForRetirement(signal) { return this.#core.waitForRetirement(signal); }
}
