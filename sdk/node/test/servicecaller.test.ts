import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash, generateKeyPairSync, sign } from 'node:crypto';

import {
  createServiceCallerVerifier,
  SERVICE_CALLER_ASSERTION_HEADER,
  ServiceCallerVerificationError,
} from '../src/index.js';

const audience = 'target-app-id';

function signingKey() {
  const pair = generateKeyPairSync('ed25519');
  const jwk = pair.publicKey.export({ format: 'jwk' });
  if (typeof jwk.x !== 'string') throw new Error('test key has no public x coordinate');
  const x = jwk.x;
  const raw = Buffer.from(x, 'base64url');
  const kid = createHash('sha256').update(raw).digest().subarray(0, 16).toString('base64url');
  return {
    privateKey: pair.privateKey,
    jwk: { kty: 'OKP', crv: 'Ed25519', kid, x, alg: 'EdDSA', use: 'sig' },
  };
}

function assertion(
  key: ReturnType<typeof signingKey>,
  overrides: Record<string, unknown> = {},
): string {
  const header = Buffer.from(JSON.stringify({ alg: 'EdDSA', kid: key.jwk.kid, typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(JSON.stringify({
    iss: 'gregale.svc',
    sub: 'frontend-app-id',
    aud: [audience],
    exp: Math.floor(Date.now() / 1000) + 30,
    iat: Math.floor(Date.now() / 1000),
    nbf: Math.floor(Date.now() / 1000),
    jti: 'assertion-id',
    account_id: 'account-id',
    caller_instance_id: 'instance-id',
    ...overrides,
  })).toString('base64url');
  const input = `${header}.${payload}`;
  return `${input}.${sign(null, Buffer.from(input), key.privateKey).toString('base64url')}`;
}

function jwksResponse(keys: Array<ReturnType<typeof signingKey>['jwk']>): Response {
  return new Response(JSON.stringify({ keys }), {
    status: 200,
    headers: { 'Content-Type': 'application/jwk-set+json' },
  });
}

function fetchSequence(responses: Response[]) {
  let calls = 0;
  const fetch = async () => {
    const response = responses[Math.min(calls, responses.length - 1)];
    calls += 1;
    if (!response) throw new Error('no test response configured');
    return response.clone();
  };
  return { fetch: fetch as typeof globalThis.fetch, calls: () => calls };
}

function errorCode(code: string) {
  return (error: unknown) => {
    assert.ok(error instanceof ServiceCallerVerificationError);
    assert.equal(error.code, code);
    return true;
  };
}

test('verifies a caller, enforces target audience, and caches trusted keys', async () => {
  const key = signingKey();
  const fixture = fetchSequence([jwksResponse([key.jwk])]);
  const verifier = createServiceCallerVerifier({
    jwksUrl: 'http://127.0.0.1/v1/service-caller-keys',
    audience,
    fetch: fixture.fetch,
    require: true,
  });

  const caller = await verifier.verify(assertion(key));
  assert.deepEqual(caller, {
    callerAppId: 'frontend-app-id',
    targetAppId: audience,
    accountId: 'account-id',
    callerInstanceId: 'instance-id',
    callerEnv: '',
    id: 'assertion-id',
  });
  await verifier.verify(assertion(key, { jti: 'another-assertion' }));
  assert.equal(fixture.calls(), 1);
  assert.equal(SERVICE_CALLER_ASSERTION_HEADER, 'X-Faas-Caller-Assertion');
});

test('concurrent first verifications share one JWKS request', async () => {
  const key = signingKey();
  let calls = 0;
  const verifier = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/v1/service-caller-keys',
    audience,
    fetch: async () => {
      calls += 1;
      await new Promise((resolve) => setTimeout(resolve, 5));
      return jwksResponse([key.jwk]);
    },
  });

  await Promise.all(Array.from({ length: 12 }, () => verifier.verify(assertion(key))));
  assert.equal(calls, 1);
});

test('refreshes a cached key set immediately when a new signing key appears', async () => {
  const oldKey = signingKey();
  const newKey = signingKey();
  const fixture = fetchSequence([jwksResponse([oldKey.jwk]), jwksResponse([oldKey.jwk, newKey.jwk])]);
  const verifier = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/v1/service-caller-keys',
    audience,
    fetch: fixture.fetch,
  });

  await verifier.verify(assertion(oldKey));
  assert.equal(fixture.calls(), 1);
  await verifier.verify(assertion(newKey));
  assert.equal(fixture.calls(), 2);
});

test('throttles repeated refreshes for an unknown key ID', async () => {
  const knownKey = signingKey();
  const unknownKey = signingKey();
  const fixture = fetchSequence([jwksResponse([knownKey.jwk]), jwksResponse([knownKey.jwk])]);
  const verifier = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/v1/service-caller-keys',
    audience,
    fetch: fixture.fetch,
  });

  await verifier.verify(assertion(knownKey));
  await assert.rejects(verifier.verify(assertion(unknownKey)), errorCode('unknown_key'));
  assert.equal(fixture.calls(), 2, 'unknown key triggered one immediate refresh');
  await assert.rejects(verifier.verify(assertion(unknownKey)), errorCode('unknown_key'));
  assert.equal(fixture.calls(), 2, 'repeat unknown key reused the refreshed set during throttle window');
});

test('rejects malformed, wrong-audience, wrong-issuer, expired, and tampered assertions', async () => {
  const key = signingKey();
  const fixture = fetchSequence([jwksResponse([key.jwk])]);
  const verifier = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/v1/service-caller-keys',
    audience,
    fetch: fixture.fetch,
    require: true,
  });

  await assert.rejects(verifier.verify('not-a-jwt'), errorCode('malformed'));
  await assert.rejects(verifier.verify(assertion(key, { aud: ['another-app'] })), errorCode('wrong_audience'));
  await assert.rejects(verifier.verify(assertion(key, { iss: 'another-issuer' })), errorCode('wrong_issuer'));
  await assert.rejects(verifier.verify(assertion(key, { exp: 1 })), errorCode('expired'));
  const token = assertion(key);
  const pieces = token.split('.');
  const changed = Buffer.from(pieces[1] ?? '', 'base64url');
  changed[0] = (changed[0] ?? 0) ^ 1;
  pieces[1] = changed.toString('base64url');
  await assert.rejects(verifier.verify(pieces.join('.')), errorCode('bad_signature'));
});

test('optional header mode never returns invalid identity; required mode fails closed', async () => {
  const failures: string[] = [];
  const optional = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/v1/service-caller-keys',
    audience,
    fetch: async () => new Response('unavailable', { status: 503 }),
    onFailure: (error) => failures.push(error.code),
  });
  assert.equal(await optional.verifyHeader(undefined), undefined);
  assert.equal(await optional.verifyHeader('not-a-jwt'), undefined);
  assert.deepEqual(failures, ['malformed']);

  const required = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/v1/service-caller-keys',
    audience,
    fetch: async () => new Response('unavailable', { status: 503 }),
    require: true,
    onFailure: (error) => failures.push(error.code),
  });
  await assert.rejects(required.verifyHeader(undefined), errorCode('missing_assertion'));
  await assert.rejects(required.verifyHeader(['first', 'second']), errorCode('malformed'));
  assert.deepEqual(failures, ['malformed', 'missing_assertion', 'malformed']);
});

test('fails closed on unsafe JWKS URLs, redirects, and oversized key documents', async () => {
  assert.throws(() => createServiceCallerVerifier({ jwksUrl: 'http://api.example.test/keys', audience }));
  assert.throws(() => createServiceCallerVerifier({ jwksUrl: 'https://user:pass@api.example.test/keys', audience }));
  assert.throws(() => createServiceCallerVerifier({ jwksUrl: 'https://api.example.test/keys#fragment', audience }));

  const key = signingKey();
  const redirect = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/keys',
    audience,
    fetch: async (_input, init) => {
      assert.equal(init?.redirect, 'manual');
      return new Response(null, { status: 302 });
    },
  });
  await assert.rejects(redirect.verify(assertion(key)), errorCode('jwks_unavailable'));

  const oversized = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/keys',
    audience,
    fetch: async () => new Response(' '.repeat(4 * 1024 * 1024 + 1), { status: 200 }),
  });
  await assert.rejects(oversized.verify(assertion(key)), errorCode('jwks_unavailable'));
});

test('rejects duplicate assertion header values', async () => {
  const verifier = createServiceCallerVerifier({
    jwksUrl: 'https://api.example.test/keys',
    audience,
    require: true,
  });
  await assert.rejects(verifier.verifyHeader(['first', 'second']), errorCode('malformed'));
});
