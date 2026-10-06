import { createRemoteJWKSet, jwtVerify } from 'jose';
import { createToolPolicy, validateScopes } from './tool-policy.js';

// The provider owns login, consent, PKCE, discovery and access-token issuance.
// This application is an OAuth resource server and never accepts CLI credentials.
export function createAuth(config, keyResolver) {
  const auth = config.auth;
  if (!auth || !['open', 'external-oauth'].includes(auth.mode)) throw new Error('Set auth.mode to open or external-oauth');
  const toolPolicy = createToolPolicy(auth);
  function authorizeCall(req, res, next) {
    // Read the actual JSON-RPC call, never a caller-supplied Mcp-Name header.
    const name = req.body?.method === 'tools/call' ? req.body.params?.name : undefined;
    if (typeof name !== 'string' || toolPolicy.canAccess(name, req.auth)) return next();
    const scopes = toolPolicy.requiredScopes(name);
    if (auth.mode === 'external-oauth' && scopes) return reject(res, 403, 'insufficient_scope', [...new Set([...auth.scopes, ...scopes])]);
    res.setHeader('Cache-Control', 'no-store');
    return res.status(403).json({ error: 'tool_access_denied' });
  }
  if (auth.mode === 'open') {
    if (auth.issuer || auth.jwks_url || auth.resource || auth.scopes?.length) throw new Error('Open auth must not contain OAuth settings');
    return { middleware: (req, _res, next) => { delete req.auth; next(); }, metadata: null, toolPolicy, authorizeCall };
  }
  for (const key of ['issuer', 'jwks_url', 'resource']) {
    const url = new URL(auth[key]);
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash) throw new Error(`auth.${key} must be HTTPS without credentials, query or fragment`);
  }
  if (new URL(auth.resource).pathname !== config.endpoint) throw new Error('auth.resource must identify the MCP endpoint');
  validateScopes(auth.scopes);
  if (!auth.scopes.length) throw new Error('Set nonempty OAuth scopes');
  const metadataURL = new URL(`/.well-known/oauth-protected-resource${config.endpoint}`, auth.resource).href;
  const resolver = keyResolver ?? createRemoteJWKSet(new URL(auth.jwks_url), { timeoutDuration: 5000 });
  function reject(res, status, error, scopes = auth.scopes) {
    const challenge = `Bearer resource_metadata="${metadataURL}", scope="${scopes.join(' ')}"${error ? `, error="${error}"` : ''}`;
    res.setHeader('WWW-Authenticate', challenge);
    res.setHeader('Cache-Control', 'no-store');
    return res.status(status).json({ error: error || 'authentication_required' });
  }
  return {
    toolPolicy, authorizeCall,
    metadata: { resource: auth.resource, authorization_servers: [auth.issuer], scopes_supported: auth.scopes, bearer_methods_supported: ['header'] },
    async middleware(req, res, next) {
      delete req.auth;
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
        req.auth = {
          token: bearer[1], scopes: [...granted], expiresAt: payload.exp,
          clientId: typeof payload.client_id === 'string' ? payload.client_id : '',
          resource: new URL(auth.resource), resourceMetadataUrl: metadataURL,
          extra: { subject: payload.sub },
        };
        return next();
      } catch {
        return reject(res, 401, 'invalid_token');
      }
    },
  };
}
