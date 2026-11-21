import { consumeRealtimeChannel, type RealtimeConnectionOptions } from './realtime-resume.js';
import type { LiveJobSource } from './live-jobs.js';

/** Native retained transport adapter. Renew private app-issued tickets in the
 * socket factory. History gaps are safe: messages only invalidate snapshots.
 */
export function realtimeLiveJobSource(options: Omit<RealtimeConnectionOptions, 'signal'> & { channel: string }): LiveJobSource {
  options = Object.freeze({ ...options });
  return (sink, signal) => {
    const abort = new AbortController();
    let cursor = 0;
    const stop = () => abort.abort();
    signal.addEventListener('abort', stop, { once: true });
    if (signal.aborted) stop();
    void consumeRealtimeChannel({ ...options, signal: abort.signal, cursorStore: { load: () => cursor, save: sequence => { cursor = sequence; } },
      onConnectionChange(state) { options.onConnectionChange?.(state); if (state === 'disconnected') sink.disconnected(); },
      onSubscribed() { sink.ready(); },
      onResync(error) { sink.ready(); return error.latestSequence; },
      onMessage(message) {
        if (message.binary || message.deleted || message.data.byteLength > 4096) return;
        try { sink.changed(JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(message.data))); } catch { /* unrelated application event */ }
      },
    }).catch(() => { if (!abort.signal.aborted) sink.failed(); });
    return () => { signal.removeEventListener('abort', stop); stop(); };
  };
}
