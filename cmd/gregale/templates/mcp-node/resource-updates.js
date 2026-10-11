const MAX_RESOURCE_URI_BYTES = 16 * 1024;

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
