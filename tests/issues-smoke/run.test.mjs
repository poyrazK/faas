import test from 'node:test';
import assert from 'node:assert/strict';
import { readSmokeConfig, assertIssueGroup, runIssuesSmoke } from './run.mjs';

function validEnv(overrides = {}) {
  return {
    GREGALE_API_URL: 'https://api.staging.gregale.dev',
    GREGALE_ISSUES_SMOKE_TARGET: 'api.staging.gregale.dev',
    GREGALE_APP: 'issues-smoke',
    GREGALE_ISSUES_SMOKE_ENVIRONMENT: 'staging',
    FAAS_TOKEN: 'fp_test_read_only',
    GREGALE_ISSUES_SMOKE_TOKEN_A: 'g_issue_release_a',
    GREGALE_ISSUES_SMOKE_TOKEN_B: 'g_issue_release_b',
    GREGALE_ISSUES_SMOKE_DEPLOYMENT_A: '00000000-0000-4000-8000-000000000001',
    GREGALE_ISSUES_SMOKE_DEPLOYMENT_B: '00000000-0000-4000-8000-000000000002',
    ...overrides,
  };
}

test('requires an explicit hostname confirmation and blocks the configured production API', () => {
  assert.throws(() => readSmokeConfig(validEnv({ GREGALE_ISSUES_SMOKE_TARGET: '' })), /exact staging API hostname/);
  assert.throws(() => readSmokeConfig(validEnv({
    GREGALE_API_URL: 'https://api.example.com',
    GREGALE_ISSUES_SMOKE_TARGET: 'api.example.com',
  })), /must identify staging/);
  assert.throws(() => readSmokeConfig(validEnv({
    GREGALE_API_URL: 'https://api.gregale.dev',
    GREGALE_ISSUES_SMOKE_TARGET: 'api.gregale.dev',
  })), /production API/);
});

test('requires distinct deployment-bound reporter credentials and releases', () => {
  assert.throws(() => readSmokeConfig(validEnv({ GREGALE_ISSUES_SMOKE_TOKEN_B: 'g_issue_release_a' })), /tokens must be distinct/);
  assert.throws(() => readSmokeConfig(validEnv({
    GREGALE_ISSUES_SMOKE_DEPLOYMENT_B: '00000000-0000-4000-8000-000000000001',
  })), /must be different/);
  assert.throws(() => readSmokeConfig(validEnv({ GREGALE_ISSUES_SMOKE_TOKEN_A: 'fp_live_owner_token' })), /deployment-bound/);
});

test('accepts loopback for a local staging-compatible API with explicit confirmation', () => {
  const config = readSmokeConfig(validEnv({
    GREGALE_API_URL: 'http://127.0.0.1:8080',
    GREGALE_ISSUES_SMOKE_TARGET: '127.0.0.1',
  }));
  assert.equal(config.apiURL.origin, 'http://127.0.0.1:8080');
});

test('requires both correlated event IDs and their separate release history in one issue', () => {
  const detail = {
    issue: { id: 'issue-1', state: 'open', event_count: 2 },
    releases: [
      { deployment_id: 'dep-a' },
      { deployment_id: 'dep-b' },
    ],
    events: [
      { event_id: 'event-a', deployment_id: 'dep-a', exception_type: 'SmokeError', source_kind: 'exception' },
      { event_id: 'event-b', deployment_id: 'dep-b', exception_type: 'SmokeError', source_kind: 'exception' },
    ],
  };
  assert.equal(assertIssueGroup(detail, {
    eventIDs: ['event-a', 'event-b'], deployments: ['dep-a', 'dep-b'],
    exceptionType: 'SmokeError', sourceKind: 'exception', requestID: '', invocationID: '',
  }), 'issue-1');
  assert.throws(() => assertIssueGroup({ ...detail, events: [detail.events[0]] }, {
    eventIDs: ['event-a', 'event-b'], deployments: ['dep-a', 'dep-b'],
    exceptionType: 'SmokeError', sourceKind: 'exception', requestID: '', invocationID: '',
  }), /did not contain smoke event/);
});

test('checks debugger and invocation evidence only when real IDs are supplied', () => {
  const detail = {
    issue: { id: 'issue-1', state: 'open', event_count: 2 },
    releases: [{ deployment_id: 'dep-a' }, { deployment_id: 'dep-b' }],
    events: [
      { event_id: 'event-a', deployment_id: 'dep-a', exception_type: 'SmokeError', source_kind: 'exception', request_id: 'request-1', debug_request_id: 'request-telemetry-1' },
      { event_id: 'event-b', deployment_id: 'dep-b', exception_type: 'SmokeError', source_kind: 'exception', invocation_id: '00000000-0000-4000-8000-000000000099' },
    ],
  };
  const expected = {
    eventIDs: ['event-a', 'event-b'], deployments: ['dep-a', 'dep-b'],
    exceptionType: 'SmokeError', sourceKind: 'exception', requestID: 'request-1',
    invocationID: '00000000-0000-4000-8000-000000000099',
  };
  assert.equal(assertIssueGroup(detail, expected), 'issue-1');
  assert.throws(() => assertIssueGroup({ ...detail, events: detail.events.map(event => ({ ...event, debug_request_id: '' })) }, expected), /request debugger link/);
});

test('runs the staging smoke flow against a fake Gregale API without external writes', async () => {
  const env = validEnv({
    GREGALE_ISSUES_SMOKE_REQUEST_ID: 'request-smoke-1',
    GREGALE_ISSUES_SMOKE_INVOCATION_ID: '00000000-0000-4000-8000-000000000099',
  });
  const deploymentByToken = new Map([
    ['Bearer g_issue_release_a', env.GREGALE_ISSUES_SMOKE_DEPLOYMENT_A],
    ['Bearer g_issue_release_b', env.GREGALE_ISSUES_SMOKE_DEPLOYMENT_B],
  ]);
  const groups = new Map();
  const posts = [];
  const fakeFetch = async (input, init = {}) => {
    const url = new URL(input);
    if (url.pathname.endsWith('/issue-events') && init.method === 'POST') {
      const event = JSON.parse(init.body);
      const key = `${event.exception_type}|${event.source_kind}`;
      let group = groups.get(key);
      if (!group) {
        group = { id: `issue-${groups.size + 1}`, type: event.exception_type, events: [] };
        groups.set(key, group);
      }
      group.events.push({
        ...event,
        deployment_id: deploymentByToken.get(init.headers.Authorization),
        ...(event.request_id ? { debug_request_id: 'request-telemetry-smoke-1' } : {}),
      });
      posts.push(event);
      return new Response(JSON.stringify({ issue_id: group.id, event_id: event.event_id }), { status: 202 });
    }
    if (url.pathname.endsWith('/issues')) {
      const items = [...groups.values()].map(group => ({
        id: group.id, title: `${group.type}: controlled`, state: 'open', event_count: group.events.length,
      }));
      return new Response(JSON.stringify({ items }), { status: 200 });
    }
    const match = url.pathname.match(/\/issues\/(issue-[0-9]+)$/);
    if (match) {
      const group = [...groups.values()].find(item => item.id === match[1]);
      const releases = [...new Set(group.events.map(event => event.deployment_id))].map(deployment_id => ({ deployment_id }));
      return new Response(JSON.stringify({
        issue: { id: group.id, state: 'open', event_count: group.events.length },
        events: group.events,
        releases,
        activity: [],
      }), { status: 200 });
    }
    throw new Error(`unexpected fake API request: ${init.method || 'GET'} ${url.pathname}`);
  };

  const originalLog = console.log;
  console.log = () => {};
  try {
    const result = await runIssuesSmoke(env, fakeFetch);
    assert.equal(result.httpIssueID, 'issue-1');
    assert.equal(result.workerIssueID, 'issue-2');
    assert.equal(posts.length, 4);
    assert.deepEqual(posts.map(event => event.source_kind), ['exception', 'exception', 'worker', 'worker']);
    assert.deepEqual(posts.map(event => event.route), [
      'POST /fail/http', 'POST /fail/http', 'gregale-issues-smoke:worker', 'gregale-issues-smoke:worker',
    ]);
    assert.deepEqual(posts[0].frames[0], {
      file: '/build/issues-smoke/export.js', function: 'generateExport', line: 42, in_app: true,
    });
  } finally {
    console.log = originalLog;
  }
});
