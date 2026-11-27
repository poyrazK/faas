import { createLocalJWKSet, createRemoteJWKSet, customFetch, jwtVerify } from 'jose';
import { createPromptPolicy, createResourcePolicy, createToolPolicy, validateScopes } from './tool-policy.js';

const MAX_JWKS_BYTES = 1 << 20;
const JWKS_FAILURE_BACKOFF_MS = 1_000;

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
      if (size > MAX_JWKS_BYTES) throw jwksFailure('JWKS response is too large', false);
      chunks.push(value);
    }
  } catch (error) {
    void reader.cancel().catch(() => {});
    // A connection lost mid-body can pass; an oversized document cannot.
    if (error?.message === 'JWKS response is too large') throw error;
    throw jwksFailure('JWKS body read failed', true);
  } finally {
    reader.releaseLock();
  }
  return JSON.parse(Buffer.concat(chunks, size).toString('utf8'));
}

// A token that could not be checked because the provider's JWKS could not be
// fetched: the server's failure, not a wrong token. It is answered with
// error_description "signing keys unavailable" and Retry-After.
export class SigningKeysUnavailableError extends Error {
  constructor() {
    super('signing keys unavailable');
    this.signingKeysUnavailable = true;
  }
}

// A token that could not be checked because the JWKS URL demanded
// credentials (HTTP 401/403): configuration, not a wrong token. An
// authorization server behind access protection (on Gregale, any app that
// requires a key) cannot serve its public keys. Retrying cannot fix it, so it
// is answered with error_description "signing keys refused" and no Retry-After.
export class SigningKeysRefusedError extends Error {
  constructor() {
    super('signing keys refused');
    this.signingKeysRefused = true;
  }
}

// Marks a JWKS URL that answered 401 or 403.
const refusedJWKSFailure = Symbol('refusedJWKSFailure');

// Marks a JWKS fetch failure that can pass (network, timeout, provider
// 5xx/429). A 404 or a bad document is configuration that retrying cannot fix.
const transientJWKSFailure = Symbol('transientJWKSFailure');

function jwksFailure(message, transient, refused = false) {
  const error = new Error(message);
  if (transient) error[transientJWKSFailure] = true;
  if (refused) error[refusedJWKSFailure] = true;
  return error;
}

function createJWKResolver(url) {
  let retryAt = 0;
  let lastTransient = false;
  let lastRefused = false;
  const remote = createRemoteJWKSet(new URL(url), {
    timeoutDuration: 5000,
    [customFetch]: async (...args) => {
      if (performance.now() < retryAt) throw jwksFailure('JWKS refresh is cooling down', lastTransient, lastRefused);
      try {
        let response;
        try {
          response = await fetch(...args);
        } catch (error) {
          throw jwksFailure(`JWKS fetch failed: ${error?.name ?? 'error'}`, true);
        }
        if (response.status !== 200) {
          throw jwksFailure('JWKS refresh failed', response.status >= 500 || response.status === 429, response.status === 401 || response.status === 403);
        }
        const jwks = await readJWKSet(response);
        createLocalJWKSet(jwks); // Validate the payload before considering the fetch successful.
        return new Response(JSON.stringify(jwks), { status: 200 });
      } catch (error) {
        // jose coalesces concurrent fetches; failed fetches also need a
        // backoff. Keep it short: a transient failure (the issuer waking, a
        // network blip) must not reject every valid token for 30 s.
        retryAt = performance.now() + JWKS_FAILURE_BACKOFF_MS;
        lastTransient = Boolean(error?.[transientJWKSFailure]);
        lastRefused = Boolean(error?.[refusedJWKSFailure]);
        throw error;
      }
    },
  });
  // The error this verification got (jose hands every coalesced waiter the
  // same one) decides: a transient fetch failure means the token was not
  // checked, so it is not reported as wrong.
  return async (...args) => {
    try {
      return await remote(...args);
    } catch (error) {
      if (error?.[transientJWKSFailure] || error?.code === 'ERR_JWKS_TIMEOUT') throw new SigningKeysUnavailableError();
      if (error?.[refusedJWKSFailure]) throw new SigningKeysRefusedError();
      throw error;
    }
  };
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
  function reject(res, status, error, scopes = auth.scopes, description = '', retryable = true) {
    const challenge = `Bearer resource_metadata="${metadataURL}", scope="${scopes.join(' ')}"${error ? `, error="${error}"` : ''}${description ? `, error_description="${description}"` : ''}`;
    res.setHeader('WWW-Authenticate', challenge);
    res.setHeader('Cache-Control', 'no-store');
    const body = { error: error || 'authentication_required' };
    if (description) {
      // The server could not check the token. A passing failure may let the
      // same token succeed; a refused JWKS URL will not until it is fixed.
      if (retryable) res.setHeader('Retry-After', '1');
      body.error_description = description;
    }
    return res.status(status).json(body);
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
      } catch (error) {
        if (error?.signingKeysUnavailable) return reject(res, 401, 'invalid_token', auth.scopes, error.message);
        if (error?.signingKeysRefused) return reject(res, 401, 'invalid_token', auth.scopes, error.message, false);
        return reject(res, 401, 'invalid_token');
      }
    },
  };
}

function policyScopes(policy) {
  return policy.scopes();
}
