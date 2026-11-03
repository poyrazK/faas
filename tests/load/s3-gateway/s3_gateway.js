// ADR-20261031122757 step 5: the one-node load test of the S3 gateway on a
// compute node. run.sh drives it; docs/runbooks/S3GatewayComputeLoadTest.md
// is the procedure and the pass criteria.
//
// SCENARIO picks one workload against S3_ENDPOINT/S3_BUCKET:
//   seed      writes the fixtures the read scenarios use (once per bucket);
//   small-get GETs small objects at RATE per second;
//   large-get GETs large objects with VUS parallel readers, to drive the
//             gateway to its port share;
//   put       PUTs small objects at RATE per second, as one account.
// Requests are signed with SigV4, path-style, as customers' SDKs send them.
import http from 'k6/http';
import { check, fail } from 'k6';
import { Endpoint, SignatureV4 } from 'https://jslib.k6.io/aws/0.14.0/signature.js';

const env = (name, fallback) => {
  const value = __ENV[name];
  if (value !== undefined && value !== '') {
    return value;
  }
  if (fallback === undefined) {
    fail(`set ${name}`);
  }
  return fallback;
};

const SCENARIO = env('SCENARIO');
const endpoint = new Endpoint(env('S3_ENDPOINT'));
const bucket = env('S3_BUCKET');
const rate = Number(env('RATE', '50'));
const vus = Number(env('VUS', '8'));
const duration = env('DURATION', '3m');
const smallObjects = Number(env('SMALL_OBJECTS', '200'));
const smallBytes = Number(env('SMALL_BYTES', '1024'));
const largeObjects = Number(env('LARGE_OBJECTS', '4'));
// A single proxied request carries at most 64 MiB (ADR §2).
const largeMiB = Number(env('LARGE_MIB', '32'));
// Each PUT VU rewrites its own keys in turn, so no two requests write one key
// at once (the gateway answers SlowDown to that).
const putKeysPerVU = Number(env('PUT_KEYS_PER_VU', '50'));

const signer = new SignatureV4({
  service: 's3',
  region: env('S3_REGION', 'us-east-1'),
  credentials: {
    accessKeyId: env('S3_ACCESS_KEY_ID'),
    secretAccessKey: env('S3_SECRET_ACCESS_KEY'),
  },
  uriEscapePath: false,
  applyChecksum: true,
});

function send(method, key, body, params = {}) {
  const signed = signer.sign({ method, endpoint, path: `/${bucket}/${key}`, headers: {}, body }, {});
  return http.request(method, signed.url, body === undefined ? null : body, {
    ...params,
    headers: signed.headers,
    tags: { op: method, scenario: SCENARIO },
  });
}

const scenarios = {
  seed: { executor: 'per-vu-iterations', vus: 1, iterations: 1, maxDuration: '30m' },
  'small-get': {
    executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration,
    preAllocatedVUs: Math.max(rate, 10), maxVUs: Math.max(rate * 4, 40),
  },
  'large-get': { executor: 'constant-vus', vus, duration },
  put: {
    executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration,
    preAllocatedVUs: Math.max(rate, 10), maxVUs: Math.max(rate * 4, 40),
  },
};
if (!scenarios[SCENARIO]) {
  fail(`SCENARIO must be one of ${Object.keys(scenarios).join(', ')}`);
}

export const options = {
  scenarios: { [SCENARIO]: { ...scenarios[SCENARIO], exec: SCENARIO.replace('-', '_') } },
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
  discardResponseBodies: SCENARIO === 'large-get',
};

export function seed() {
  const small = 'x'.repeat(smallBytes);
  for (let i = 0; i < smallObjects; i++) {
    const res = send('PUT', `small/${i}`, small);
    if (!check(res, { 'seed small 200': (r) => r.status === 200 })) {
      fail(`seed small/${i}: ${res.status} ${res.body}`);
    }
  }
  const large = 'y'.repeat(largeMiB * 1024 * 1024);
  for (let i = 0; i < largeObjects; i++) {
    const res = send('PUT', `large/${i}`, large, { timeout: '600s' });
    if (!check(res, { 'seed large 200': (r) => r.status === 200 })) {
      fail(`seed large/${i}: ${res.status} ${res.body}`);
    }
  }
}

export function small_get() {
  const res = send('GET', `small/${Math.floor(Math.random() * smallObjects)}`);
  check(res, { 'small GET 200': (r) => r.status === 200 && r.body.length === smallBytes });
}

export function large_get() {
  const res = send('GET', `large/${Math.floor(Math.random() * largeObjects)}`, undefined, { timeout: '600s' });
  check(res, { 'large GET 200': (r) => r.status === 200 });
}

const putBody = 'z'.repeat(smallBytes);

export function put() {
  const res = send('PUT', `put/${__VU}/${__ITER % putKeysPerVU}`, putBody);
  check(res, { 'PUT 200': (r) => r.status === 200 });
}
