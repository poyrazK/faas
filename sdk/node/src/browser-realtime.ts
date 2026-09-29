/** Browser credential carrier for managed realtime v2 handshakes. */

import {
  REALTIME_RESUME_SUBPROTOCOL,
  type RealtimeSocket,
} from './realtime-resume.js';

export const REALTIME_RESUME_BEARER_SUBPROTOCOL_PREFIX = 'gregale.realtime.bearer.';

export type BrowserRealtimeSocketFactory =
  (url: string, protocols: string[]) => Promise<RealtimeSocket>;

/**
 * Create the socket factory used by `consumeRealtimeChannel` in a browser.
 * `getOIDCToken` runs on every reconnect so an expired JWT can be refreshed.
 * Pass `createGregaleBrowserFetch(...).webSocket` as `openSocket` when the app
 * also needs project release pinning on its WebSocket handshakes.
 */
export function createBrowserRealtimeSocketFactory(
  getOIDCToken: () => string | Promise<string>,
  openSocket: (url: string, protocols: string[]) => RealtimeSocket =
    (url, protocols) => new WebSocket(url, protocols),
): BrowserRealtimeSocketFactory {
  return async (url, protocols) => {
    if (!protocols.includes(REALTIME_RESUME_SUBPROTOCOL)) {
      throw new TypeError('browser realtime requires the v2 subprotocol');
    }
    const token = await getOIDCToken();
    if (typeof token !== 'string' || token.length > 3072 ||
        !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(token)) {
      throw new TypeError('browser realtime requires a bounded signed OIDC JWT');
    }
    return openSocket(url, [...protocols, `${REALTIME_RESUME_BEARER_SUBPROTOCOL_PREFIX}${token}`]);
  };
}
