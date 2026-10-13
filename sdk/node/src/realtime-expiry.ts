/** Payload-free observation of an authenticated, validated expired position. */
export interface RealtimeMessageExpiry {
  code: 'message_expired';
  channel: string;
  /** Decimal string on both transports, preserving the PostgreSQL sequence range. */
  sequence: string;
  transport: 'websocket' | 'sse';
}

export interface RealtimeInboxMessageExpiry {
 code: "message_expired";
 consumer: string;
 sequence: string;
 transport: "websocket";
 inbox: true;
}
export type RealtimeInboxMessageExpiryObserver = (event: Readonly<RealtimeInboxMessageExpiry>) => void;

export type RealtimeMessageExpiryObserver = (event: Readonly<RealtimeMessageExpiry>) => void;

/** Diagnostics are best effort and never control processing, persistence or ACKs. */
export function notifyRealtimeMessageExpiry<T extends RealtimeMessageExpiry | RealtimeInboxMessageExpiry>(observer: ((event: Readonly<T>) => void) | undefined, event: T): void {
  if (!observer) return;
  try {
    // Also isolate accidentally async observers without awaiting them.
    const result: unknown = observer(Object.freeze({ ...event }) as Readonly<T>);
    if (result && typeof (result as PromiseLike<unknown>).then === 'function') {
      void Promise.resolve(result).catch(() => {});
    }
  } catch { /* Diagnostic observer failure cannot interrupt recovery. */ }
}

/** Constant-memory process-local observations; reconnects can observe a position again. */
export class RealtimeExpiryDiagnostics {
  private websocket = 0;
  private sse = 0;
  private inbox = 0;

  readonly record = (event: Readonly<RealtimeMessageExpiry | RealtimeInboxMessageExpiry>): void => {
    if (event.code !== 'message_expired' || !['websocket', 'sse'].includes(event.transport)
      || !/^[1-9][0-9]*$/.test(event.sequence) || event.sequence.length > 19
      || BigInt(event.sequence) > 9223372036854775807n) throw new TypeError('Invalid expiry observation');
    if ('inbox' in event && event.inbox) {
      if (event.transport !== 'websocket') throw new TypeError('Invalid inbox expiry observation');
      this.inbox = Math.min(this.inbox + 1, Number.MAX_SAFE_INTEGER);
    } else if (event.transport === 'websocket') this.websocket = Math.min(this.websocket + 1, Number.MAX_SAFE_INTEGER);
    else this.sse = Math.min(this.sse + 1, Number.MAX_SAFE_INTEGER);
  };

  /** Identifiers, payloads and credentials are never retained or exported. */
  snapshot(): Readonly<{ websocketCheckpoints: number; sseCheckpoints: number; inboxCheckpoints: number }> {
    return Object.freeze({ websocketCheckpoints: this.websocket, sseCheckpoints: this.sse, inboxCheckpoints: this.inbox });
  }
}
