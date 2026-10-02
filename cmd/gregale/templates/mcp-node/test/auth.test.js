import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPair, SignJWT } from 'jose';
import { createAuth } from '../auth.js';

const config = { endpoint: '/mcp', auth: { mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks', resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'] } };
const { publicKey, privateKey } = await generateKeyPair('RS256');
const other = await generateKeyPair('RS256');
const auth = createAuth(config, publicKey);

async function token({ audience = config.auth.resource, issuer = config.auth.issuer, scope = 'mcp:tools', expires = '5m', key = privateKey, subject = 'customer' } = {}) {
  const jwt = new SignJWT({ scope }).setProtectedHeader({ alg: 'RS256' }).setAudience(audience).setIssuer(issuer).setExpirationTime(expires);
  if (subject !== null) jwt.setSubject(subject);
  return jwt.sign(key);
}
async function invoke(value, scheme = 'Bearer') {
  const result = { status: 200, accepted: false, headers: {} };
  const res = { setHeader: (k,v) => { result.headers[k] = v; }, status: n => { result.status = n; return res; }, json: body => { result.body = body; return res; } };
  const req = { headers: value ? { authorization: `${scheme} ${value}` } : {}, auth: { scopes: ['forged'] } };
  await auth.middleware(req, res, () => { result.accepted = true; });
  result.authInfo = req.auth;
  return result;
}
test('protected resource metadata and bearer challenge identify the canonical endpoint', async () => {
  assert.equal(auth.metadata.resource, config.auth.resource);
  assert.deepEqual(auth.metadata.authorization_servers, [config.auth.issuer]);
  const result = await invoke();
  assert.equal(result.status, 401);
  assert.match(result.headers['WWW-Authenticate'], /resource_metadata="https:\/\/mcp.example\/\.well-known\/oauth-protected-resource\/mcp"/);
});
test('valid scoped, signed token authorizes a request', async () => assert.equal((await invoke(await token())).accepted, true));
test('only verified claims populate the SDK request context', async () => {
  const result = await invoke(await token({ scope: 'mcp:tools files:read' }));
  assert.deepEqual(result.authInfo.scopes, ['mcp:tools', 'files:read']);
  assert.equal(result.authInfo.extra.subject, 'customer');
  assert.equal((await invoke('malformed')).authInfo, undefined);
});
test('bearer scheme is case insensitive', async () => assert.equal((await invoke(await token(), 'bearer')).accepted, true));
for (const [name, options, status] of [
  ['wrong audience', { audience: 'https://other.example/mcp' }, 401],
  ['wrong issuer', { issuer: 'https://other.example' }, 401],
  ['wrong signature', { key: other.privateKey }, 401],
  ['expired', { expires: Math.floor(Date.now()/1000)-1 }, 401],
  ['missing scope', { scope: 'other' }, 403],
  ['missing subject', { subject: null }, 401],
  ['empty subject', { subject: '' }, 401],
]) test(name, async () => {
  const value = await token(options);
  const result = await invoke(value);
  assert.equal(result.status, status);
  assert.equal(result.accepted, false);
  assert.ok(!JSON.stringify(result).includes(value));
});
test('malformed token fails without reflecting the value', async () => assert.equal((await invoke('malformed')).status, 401));
test('auth mode and scopes must be explicit', () => {
  assert.throws(() => createAuth({ endpoint: '/mcp', auth: { mode: 'external-oauth' } }));
  assert.throws(() => createAuth({ ...config, auth: { ...config.auth, scopes: [] } }));
  assert.throws(() => createAuth({ endpoint: '/mcp', auth: { mode: 'open', issuer: 'https://issuer.example' } }));
});
