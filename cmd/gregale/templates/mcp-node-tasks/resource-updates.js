const MAX_RESOURCE_URI_BYTES = 16 * 1024;

// Bound notification output independently of the number of admitted streams.
// Node's write() false result starts a deadline that only drain can reset.
export function createSubscriptionOutput(response, onBlocked, {
  maxBufferedBytes = 1 << 20, writeTimeoutMs = 15_000,
  schedule = setTimeout, cancel = clearTimeout,
} = {}) {
  let timer;
  let closed = false;
  const drained = () => { cancel(timer); timer = undefined; };
  function close() {
    if (closed) return;
    closed = true;
    drained();
    response.removeListener('drain', drained);
  }
  function blocked() { if (!closed) { close(); onBlocked(); } }
  response.on('drain', drained);
  return {
    maxBufferedBytes,
    close,
    write(frame) {
      if (closed || response.destroyed || response.writableEnded) return false;
      if ((response.writableLength || 0) + Buffer.byteLength(frame) > maxBufferedBytes) {
        blocked();
        return false;
      }
      if (!response.write(frame) && timer === undefined) {
        timer = schedule(blocked, writeTimeoutMs);
        timer.unref?.();
      }
      return true;
    },
  };
}

export function validResourceUpdateURI(uri) {
  return typeof uri === 'string' && uri.length > 0
    && Buffer.byteLength(uri) <= MAX_RESOURCE_URI_BYTES
    && !/[\u0000\r\n]/.test(uri);
}

// This in-memory default serves one Gregale web process. Multi-replica apps
// can supply an adapter backed by their shared pub/sub service.
export function createResourceUpdateHub() {
  const subscribers = new Map();
  return {
    subscribe(uri, listener) {
      if (!validResourceUpdateURI(uri) || typeof listener !== 'function') return () => {};
      let listeners = subscribers.get(uri);
      if (!listeners) {
        listeners = new Set();
        subscribers.set(uri, listeners);
      }
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
        if (listeners.size === 0) subscribers.delete(uri);
      };
    },
    publish(uri) {
      if (!validResourceUpdateURI(uri)) return;
      for (const listener of [...(subscribers.get(uri) || [])]) {
        try { listener(uri); } catch { /* One stream must not block other subscribers. */ }
      }
    },
  };
}
