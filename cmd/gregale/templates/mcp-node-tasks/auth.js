import { createLocalJWKSet, createRemoteJWKSet, customFetch, jwtVerify } from 'jose';
import { createPromptPolicy, createResourcePolicy, createToolPolicy, validateScopes } from './tool-policy.js';

const MAX_JWKS_BYTES = 1 << 20;

async function readJWKSet(response) {
  if (!response.body) throw new Error('Empty JWKS response');
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_JWKS_BYTES) throw new Error('JWKS response is too large');
      chunks.push(value);
    }
  } catch (error) {
    void reader.cancel().catch(() => {});
    throw error;
  } finally {
    reader.releaseLock();
  }
  return JSON.parse(Buffer.concat(chunks, size).toString('utf8'));
}

function createJWKResolver(url) {
  let retryAt = 0;
  return createRemoteJWKSet(new URL(url), {
    timeoutDuration: 5000,
    [customFetch]: async (...args) => {
      if (performance.now() < retryAt) throw new Error('JWKS refresh is cooling down');
      try {
        const response = await fetch(...args);
        if (response.status !== 200) throw new Error('JWKS refresh failed');
        const jwks = await readJWKSet(response);
        createLocalJWKSet(jwks); // Validate the payload before considering the fetch successful.
        return new Response(JSON.stringify(jwks), { status: 200 });
      } catch (error) {
        // jose coalesces concurrent fetches; failed fetches also need a cooldown.
        retryAt = performance.now() + 30_000;
        throw error;
      }
    },
  });
}

// Only call this with authInfo created by the verified bearer-token middleware.
// It intentionally omits the token and all raw JWT claims from handler-facing identity.
export function verifiedCallerIdentity(authInfo) {
  const subject = authInfo?.extra?.subject;
  if (typeof subject !== 'string' || !subject) return undefined;
  return Object.freeze({
    subject,
    clientId: typeof authInfo.clientId === 'string' ? authInfo.clientId : '',
  });
}

// The provider owns login, consent, PKCE, discovery and access-token issuance.
// This application is an OAuth resource server and never accepts CLI credentials.
export function createAuth(config, keyResolver) {
  const auth = config.auth;
  if (!auth || !['open', 'external-oauth'].includes(auth.mode)) throw new Error('Set auth.mode to open or external-oauth');
  const toolPolicy = createToolPolicy(auth);
  const resourcePolicy = createResourcePolicy(auth);
  const promptPolicy = createPromptPolicy(auth);
  function authorizeCall(req, res, next) {
    // Read the actual JSON-RPC call, never a caller-supplied Mcp-Name header.
    const request = req.body;
    const method = request?.method;
    let policy;
    let identifier;
    let kind;
    if (method === 'tools/call') {
      policy = toolPolicy;
      identifier = request?.params?.name;
      kind = 'tool';
    } else if (method === 'resources/read') {
      policy = resourcePolicy;
      identifier = request?.params?.uri;
      kind = 'resource';
    } else if (method === 'prompts/get') {
      policy = promptPolicy;
      identifier = request?.params?.name;
      kind = 'prompt';
    } else if (method === 'completion/complete') {
      const ref = request?.params?.ref;
      if (ref?.type === 'ref/prompt') {
        policy = promptPolicy;
        identifier = ref.name;
        kind = 'prompt';
      } else if (ref?.type === 'ref/resource') {
        policy = resourcePolicy;
        identifier = ref.uri;
        kind = 'resource';
      }
    }
    if (!policy || typeof identifier !== 'string' || policy.canAccess(identifier, req.auth)) return next();
    const scopes = policy.requiredScopes(identifier);
    if (auth.mode === 'external-oauth' && scopes) return reject(res, 403, 'insufficient_scope', [...new Set([...auth.scopes, ...scopes])]);
    res.setHeader('Cache-Control', 'no-store');
    return res.status(403).json({ error: `${kind}_access_denied` });
  }
  if (auth.mode === 'open') {
    if (auth.issuer || auth.jwks_url || auth.resource || auth.scopes?.length) throw new Error('Open auth must not contain OAuth settings');
    return { middleware: (req, _res, next) => { delete req.auth; next(); }, metadata: null, toolPolicy, resourcePolicy, promptPolicy, authorizeCall };
  }
  for (const key of ['issuer', 'jwks_url', 'resource']) {
    const url = new URL(auth[key]);
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash) throw new Error(`auth.${key} must be HTTPS without credentials, query or fragment`);
  }
  if (new URL(auth.resource).pathname !== config.endpoint) throw new Error('auth.resource must identify the MCP endpoint');
  validateScopes(auth.scopes);
  if (!auth.scopes.length) throw new Error('Set nonempty OAuth scopes');
  const metadataURL = new URL(`/.well-known/oauth-protected-resource${config.endpoint}`, auth.resource).href;
  const resolver = keyResolver ?? createJWKResolver(auth.jwks_url);
  function reject(res, status, error, scopes = auth.scopes) {
    const challenge = `Bearer resource_metadata="${metadataURL}", scope="${scopes.join(' ')}"${error ? `, error="${error}"` : ''}`;
    res.setHeader('WWW-Authenticate', challenge);
    res.setHeader('Cache-Control', 'no-store');
    return res.status(status).json({ error: error || 'authentication_required' });
  }
  const scopesSupported = [...new Set([
    ...auth.scopes,
    ...[toolPolicy, resourcePolicy, promptPolicy].flatMap(policy => [...policyScopes(policy)]),
  ])].sort();
  return {
    toolPolicy, resourcePolicy, promptPolicy, authorizeCall,
    metadata: { resource: auth.resource, authorization_servers: [auth.issuer], scopes_supported: scopesSupported, bearer_methods_supported: ['header'] },
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

function policyScopes(policy) {
  return policy.scopes();
}
