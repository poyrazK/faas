// Load on a compute node's public edge (gatewayd-public) with no app behind
// it. run.sh drives one k6 process per node at that node's entry address;
// docs/runbooks/EdgeLoadTest.md is the procedure and the pass criteria.
//
// Every request names a host under DOMAIN that is not an app, so it is
// answered 404 by the edge and gatewayd-internal and never wakes a VM.
// SCENARIO picks the work:
//   miss      one fixed non-app host on kept-alive connections: the per-request
//             cost of the edge and gatewayd-internal, answered from their
//             remembered misses (no control-plane read after the first);
//   handshake the same host on a new TLS connection per request: the cost
//             of the handshake;
//   new-host  a never-seen host per request: each spends the edge's and the
//             gateway's new-host lookup budgets (25/s, burst 500 per node,
//             pkg/api/limits.go) and about two control-plane Postgres reads.
// Expected answer is 404; anything else counts as bad, and ABORT_BAD bad
// answers stop the run.
import http from 'k6/http';
import { Counter } from 'k6/metrics';
import { fail } from 'k6';

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
const targetIP = env('TARGET_IP');
const domain = env('DOMAIN');
const missHost = env('MISS_HOST');
const runID = env('RUN_ID');
const rate = Number(env('RATE'));
const duration = env('DURATION', '60s');
const ramp = env('RAMP', '10s');
const abortBad = Number(env('ABORT_BAD', '50'));

if (!['miss', 'handshake', 'new-host'].includes(SCENARIO)) {
  fail('SCENARIO must be one of miss, handshake, new-host');
}

const answers = {
  s404: new Counter('s404'),
  s429: new Counter('s429'),
  s503: new Counter('s503'),
  s5xx: new Counter('s5xx'),
  sother: new Counter('sother'),
  // Status 0: no HTTP answer (refused, reset, timeout).
  snone: new Counter('snone'),
};
const bad = new Counter('bad');

export const options = {
  // Every name under the domain goes to this node's entry address; TLS SNI
  // and Host stay the real names, as a customer's client sends them.
  hosts: { [`*.${domain}`]: targetIP },
  // A new host needs a new connection anyway (its own SNI), and k6 keeps an
  // idle one open per host it has seen: at 10 new hosts a second that reached
  // the edge's 512 open connections per address in 51 s (2026-11-09). So
  // only miss reuses connections.
  noConnectionReuse: SCENARIO !== 'miss',
  scenarios: {
    [SCENARIO]: {
      executor: 'ramping-arrival-rate',
      startRate: Math.max(1, Math.floor(rate / 5)),
      timeUnit: '1s',
      stages: [{ target: rate, duration: ramp }, { target: rate, duration }],
      preAllocatedVUs: Math.max(10, Math.ceil(rate / 2)),
      // Each VU holds its own connection; stay under the edge's 512 open
      // connections per address (EdgeClientOpenConnsPerAddrMax).
      maxVUs: Math.min(400, Math.max(40, rate * 2)),
      exec: 'hit',
    },
  },
  thresholds: { bad: [{ threshold: `count<${abortBad}`, abortOnFail: true, delayAbortEval: '5s' }] },
  summaryTrendStats: ['avg', 'p(50)', 'p(90)', 'p(99)', 'max'],
  discardResponseBodies: true,
  userAgent: 'gregale-edge-loadtest/1',
};

function host() {
  if (SCENARIO !== 'new-host') {
    return missHost;
  }
  // A valid app slug (pkg/api/app_slug.go) that no app has: the edge and the
  // gateway must look it up rather than reject it by shape.
  return `lt-edge-${runID}-${__VU}-${__ITER}.${domain}`;
}

export function hit() {
  const res = http.get(`https://${host()}/`, { timeout: env('REQ_TIMEOUT', '10s'), tags: { name: SCENARIO } });
  if (res.status === 404) {
    answers.s404.add(1);
    return;
  }
  bad.add(1);
  if (res.status === 0) answers.snone.add(1);
  else if (res.status === 429) answers.s429.add(1);
  else if (res.status === 503) answers.s503.add(1);
  else if (res.status >= 500) answers.s5xx.add(1);
  else answers.sother.add(1);
}
