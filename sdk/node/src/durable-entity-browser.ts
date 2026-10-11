/** Browser-only transport. Obtain a grant from your authenticated application
 * backend, which must authorize the user for the entity before minting it.
 * Keep the credential in memory; do not put it in a URL or persistent outbox. */
export interface DurableEntityBrowserGrant {
  credential: string;
  connect_path: string;
  expires_at: string;
  protocol: 'gregale.entity.v1';
}
export function connectDurableEntityBrowserSession(apiOrigin: string, grant: DurableEntityBrowserGrant): WebSocket {
  const origin = new URL(apiOrigin);
  if (origin.username || origin.password || origin.search || origin.hash || origin.pathname !== '/' ||
      (origin.protocol !== 'https:' && !(origin.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(origin.hostname)))) {
    throw new TypeError('use an HTTPS API origin (HTTP localhost is allowed for development)');
  }
  if (grant.protocol !== 'gregale.entity.v1' || !/^\/v1\/apps\/[a-z0-9-]+\/entities\/session-connect$/.test(grant.connect_path) ||
      !/^(?:[a-f0-9]{64}\.)?[a-f0-9-]{36}\.[A-Za-z0-9_-]{43}$/.test(grant.credential) || !Number.isFinite(Date.parse(grant.expires_at)) || Date.parse(grant.expires_at) <= Date.now()) {
    throw new TypeError('invalid or expired session grant');
  }
  const target = new URL(grant.connect_path, origin);
  target.protocol = origin.protocol === 'https:' ? 'wss:' : 'ws:';
  return new WebSocket(target.href, [grant.protocol, `gregale.grant.${grant.credential}`]);
}
