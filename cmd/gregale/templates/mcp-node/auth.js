import { createRemoteJWKSet, jwtVerify } from 'jose';

// The provider owns login, consent, PKCE, discovery and access-token issuance.
// This application is an OAuth resource server and never accepts CLI credentials.
export function createAuth(config, keyResolver) {
  const auth = config.auth;
  if (!auth || !['open', 'external-oauth'].includes(auth.mode)) throw new Error('Set auth.mode to open or external-oauth');
  if (auth.mode === 'open') {
    if (auth.issuer || auth.jwks_url || auth.resource || auth.scopes?.length) throw new Error('Open auth must not contain OAuth settings');
    return { middleware: (_req, _res, next) => next(), metadata: null };
  }
  for (const key of ['issuer', 'jwks_url', 'resource']) {
    const url = new URL(auth[key]);
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash) throw new Error(`auth.${key} must be HTTPS without credentials, query or fragment`);
  }
  if (new URL(auth.resource).pathname !== config.endpoint) throw new Error('auth.resource must identify the MCP endpoint');
  if (!Array.isArray(auth.scopes) || !auth.scopes.length || auth.scopes.some(s => typeof s !== 'string' || !/^[\x21\x23-\x5b\x5d-\x7e]+$/.test(s))) throw new Error('Set nonempty OAuth scopes');
  const metadataURL = new URL(`/.well-known/oauth-protected-resource${config.endpoint}`, auth.resource).href;
  const resolver = keyResolver ?? createRemoteJWKSet(new URL(auth.jwks_url), { timeoutDuration: 5000 });
  function reject(res, status, error) {
    const challenge = `Bearer resource_metadata="${metadataURL}", scope="${auth.scopes.join(' ')}"${error ? `, error="${error}"` : ''}`;
    res.setHeader('WWW-Authenticate', challenge);
    res.setHeader('Cache-Control', 'no-store');
    return res.status(status).json({ error: error || 'authentication_required' });
  }
  return {
    metadata: { resource: auth.resource, authorization_servers: [auth.issuer], scopes_supported: auth.scopes, bearer_methods_supported: ['header'] },
    async middleware(req, res, next) {
      const authorization = req.headers.authorization;
      const bearer = typeof authorization === 'string' && /^Bearer +([^ ]+)$/i.exec(authorization);
      if (!bearer) return reject(res, 401);
      try {
        const { payload } = await jwtVerify(bearer[1], resolver, {
          issuer: auth.issuer, audience: auth.resource, algorithms: ['RS256', 'ES256'], requiredClaims: ['exp', 'sub'],
        });
        if (typeof payload.sub !== 'string' || !payload.sub.length) return reject(res, 401, 'invalid_token');
        const granted = new Set(typeof payload.scope === 'string' ? payload.scope.split(' ') : []);
        if (auth.scopes.some(scope => !granted.has(scope))) return reject(res, 403, 'insufficient_scope');
        // Add tool-specific authorization here. Never trust a tenant ID from arguments.
        return next();
      } catch {
        return reject(res, 401, 'invalid_token');
      }
    },
  };
}
