import { notifyRealtimeMessageExpiry, type RealtimeMessageExpiryObserver } from './realtime-expiry.js';
import { parseFrame } from './sse.js';
import type { RealtimeOccupancy } from './realtime-resume.js';

export interface RealtimeSSEOptions {
  onMessageExpired?: RealtimeMessageExpiryObserver;
  /** Managed endpoint URL over HTTP(S); tokens are sent only in headers. */
  url: string;
  channel: string;
  token: () => string | Promise<string>;
  after?: string;
  signal?: AbortSignal;
  fetch?: typeof fetch;
  /**
   * Receive the channel's subscriber count across all realtime nodes after
   * subscribing and whenever it changes, at most once a second (ADR-1324).
   * Errors thrown here are ignored. When the node already follows its maximum
   * of live-count channels, the stream continues without counts.
   */
  onOccupancy?(occupancy: RealtimeOccupancy): void;
}
export interface RealtimeSSEMessage {
  /** Expired position: advance/persist cursor without processing a payload. */
  checkpoint?: boolean;
  expires_at?: string | null;
  channel: string;
  sequence: string;
  data_base64: string;
  binary: boolean;
  metadata?: Record<string, string>;
  target_message_id?: string;
  version?: number;
  message_event?: string;
  deleted?: boolean;
}
export class RealtimeSSEError extends Error {
  constructor(public readonly code: string, public readonly details?: unknown) { super(code); this.name = 'RealtimeSSEError'; }
}

/** Cursor advances after the consumer resumes iteration; duplicates are possible. */
export async function* subscribeRealtimeSSE(options: RealtimeSSEOptions): AsyncGenerator<RealtimeSSEMessage> {
  const url = new URL(options.url);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) throw new TypeError('Invalid SSE URL');
  if (!options.channel || /[\x00\r\n/?#]/.test(options.channel) || options.channel.trim() !== options.channel || new TextEncoder().encode(options.channel).length > 256) throw new TypeError('Invalid channel');
  let after = options.after ?? '0';
  if (!/^(0|[1-9][0-9]*)$/.test(after) || BigInt(after) > 9223372036854775807n) throw new TypeError('Invalid cursor');
  url.searchParams.set('transport', 'sse');
  url.searchParams.set('channel', options.channel);
  url.searchParams.delete('after');
  if (options.onOccupancy) url.searchParams.set('occupancy', '1');
  else url.searchParams.delete('occupancy');
  const fetcher = options.fetch ?? globalThis.fetch;
  const stop = new AbortController();
  const signal = options.signal ? AbortSignal.any([options.signal, stop.signal]) : stop.signal;
  let retry = 500;
  try {
    while (!signal.aborted) {
      let response: Response | undefined;
      let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
      try {
        let token: string;
        try { token = await options.token(); } catch { throw new RealtimeSSEError("credential_provider_failed"); }
        if (typeof token !== 'string' || !token || /[\r\n]/.test(token)) throw new RealtimeSSEError('invalid_token');
        response = await fetcher(url, { headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream', 'Last-Event-ID': after }, signal, cache: 'no-store', credentials: 'omit', redirect: 'error' });
        if (!response.ok) {
          if ([400, 401, 403, 404, 405].includes(response.status)) throw new RealtimeSSEError(`http_${response.status}`);
          if (response.status !== 429 && response.status < 500) throw new RealtimeSSEError(`http_${response.status}`);
          throw new Error('Transient SSE failure');
        }
        if (!response.headers.get('content-type')?.startsWith('text/event-stream') || !response.body) throw new RealtimeSSEError('invalid_stream');
        reader = response.body.getReader();
        const decoder = new TextDecoder('utf-8', { fatal: true });
        let buffer = '';
        while (!signal.aborted) {
          const chunk = await reader.read();
          if (chunk.done) break;
          buffer += decoder.decode(chunk.value, { stream: true });
          let parsed: ReturnType<typeof parseFrame>;
          while ((parsed = parseFrame(buffer)) !== null) {
            if (parsed.consumed > 262144) throw new RealtimeSSEError('frame_too_large');
            buffer = buffer.slice(parsed.consumed);
            const event = parsed.event;
            if (event.event === 'heartbeat' || event.event === 'subscribed' || !event.data) continue;
            let data: unknown;
            try { data = JSON.parse(event.data); } catch { throw new RealtimeSSEError('invalid_frame'); }
            if (event.event === 'resync_required') throw new RealtimeSSEError('resync_required', data);
            if (event.event === 'occupancy') {
              const occupancy = data as { channel?: unknown; subscribers?: unknown };
              if (!occupancy || occupancy.channel !== options.channel || typeof occupancy.subscribers !== 'number'
                || !Number.isSafeInteger(occupancy.subscribers) || occupancy.subscribers < 0) throw new RealtimeSSEError('invalid_frame');
              try { options.onOccupancy?.({ channel: options.channel, subscribers: occupancy.subscribers }); } catch { /* ignored, see onOccupancy */ }
              continue;
            }
            if (event.event === 'error' && (data as { code?: unknown } | null)?.code === 'occupancy_limit') continue;
            if (event.event === 'error') throw new RealtimeSSEError('stream_error', data);
            if (event.event === 'checkpoint') {
              const checkpoint = data as { channel?: unknown; sequence?: unknown; code?: unknown };
              if (!checkpoint || checkpoint.channel !== options.channel || checkpoint.code !== 'message_expired' || typeof checkpoint.sequence !== 'string'
                || !/^[1-9][0-9]*$/.test(checkpoint.sequence) || BigInt(checkpoint.sequence) !== BigInt(after) + 1n
                || BigInt(checkpoint.sequence) > 9223372036854775807n || event.id !== checkpoint.sequence) throw new RealtimeSSEError('invalid_checkpoint');
              notifyRealtimeMessageExpiry(options.onMessageExpired, {code: 'message_expired', channel: options.channel, sequence: checkpoint.sequence, transport: 'sse'});
              retry = 500;
              yield { channel: options.channel, sequence: checkpoint.sequence, data_base64: '', binary: false, checkpoint: true };
              after = checkpoint.sequence;
              continue;
            }
            if (event.event !== 'message') throw new RealtimeSSEError('invalid_frame');
            const message = data as RealtimeSSEMessage;
            if (!message || typeof message !== 'object' || message.channel !== options.channel || typeof message.sequence !== 'string'
              || !/^[1-9][0-9]*$/.test(message.sequence) || BigInt(message.sequence) !== BigInt(after) + 1n || BigInt(message.sequence) > 9223372036854775807n
              || event.id !== message.sequence || typeof message.data_base64 !== 'string' || typeof message.binary !== 'boolean') throw new RealtimeSSEError('invalid_frame');
            retry = 500;
            yield message;
            after = message.sequence;
          }
          if (buffer.length > 262144) throw new RealtimeSSEError('frame_too_large');
        }
      } catch (error) {
        if (signal.aborted) return;
        if (error instanceof RealtimeSSEError) throw error;
      } finally {
        if (reader) { await reader.cancel().catch(() => {}); reader.releaseLock(); }
        else if (response?.body) await response.body.cancel().catch(() => {});
      }
      if (signal.aborted) return;
      await new Promise<void>((resolve) => {
        const done = () => { clearTimeout(timer); signal.removeEventListener('abort', done); resolve(); };
        const timer = setTimeout(done, retry + Math.random() * retry / 4);
        signal.addEventListener('abort', done, { once: true });
      });
      retry = Math.min(retry * 2, 10000);
    }
  } finally { stop.abort(); }
}
