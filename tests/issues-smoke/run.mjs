import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startIssuesSmokeSample } from './sample-app.mjs';

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const slugPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const environmentPattern = /^[a-z0-9][a-z0-9-]{0,62}$/;

function required(env, name) {
  const value = env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function isLoopback(hostname) {
  return hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '[::1]';
}

function validateStagingTarget(apiURL, confirmation) {
  const hostname = apiURL.hostname.toLowerCase();
  if (!confirmation || confirmation.toLowerCase() !== hostname) {
    throw new Error(`set GREGALE_ISSUES_SMOKE_TARGET to the exact staging API hostname (${hostname})`);
  }
  if (hostname === 'api.gregale.dev' || hostname === 'gregale.dev' || hostname === 'www.gregale.dev') {
    throw new Error('the Gregale production API and site cannot be used by the Issues smoke test');
  }
  const stagingHostname = hostname.includes('.') ? hostname.slice(0, hostname.lastIndexOf('.')) : hostname;
  if (!isLoopback(hostname) && !/(^|[.-])(staging|stage|stg|preview|sandbox|development|dev|test)(?=$|[.-])/.test(stagingHostname)) {
    throw new Error('the smoke target hostname must identify staging, preview, sandbox, dev, or test');
  }
  if (apiURL.protocol !== 'https:' && !(apiURL.protocol === 'http:' && isLoopback(hostname))) {
    throw new Error('the Issues smoke target must use HTTPS (HTTP is allowed only on loopback)');
  }
}

export function readSmokeConfig(env = process.env) {
  const rawAPIURL = required(env, 'GREGALE_API_URL');
  let apiURL;
  try {
    apiURL = new URL(rawAPIURL);
  } catch {
    throw new Error('GREGALE_API_URL must be an absolute API origin');
  }
  const hasURLCredentials = apiURL.username || apiURL.password;
  const hasURLSuffix = apiURL.search || apiURL.hash || (apiURL.pathname !== '/' && apiURL.pathname !== '');
  if (!['https:', 'http:'].includes(apiURL.protocol) || hasURLCredentials || hasURLSuffix) {
    throw new Error('GREGALE_API_URL must be an HTTP(S) origin without credentials, path, query, or fragment');
  }
  validateStagingTarget(apiURL, env.GREGALE_ISSUES_SMOKE_TARGET?.trim());

  const app = required(env, 'GREGALE_APP');
  if (!slugPattern.test(app)) throw new Error('GREGALE_APP must be a lowercase app slug');
  const environment = env.GREGALE_ISSUES_SMOKE_ENVIRONMENT?.trim() || 'application';
  if (!environmentPattern.test(environment)) throw new Error('GREGALE_ISSUES_SMOKE_ENVIRONMENT must be a lowercase environment name');

  const readToken = required(env, 'FAAS_TOKEN');
  if (readToken.startsWith('g_issue_')) throw new Error('FAAS_TOKEN must be an API token with issue read access');
  const tokens = [
    required(env, 'GREGALE_ISSUES_SMOKE_TOKEN_A'),
    required(env, 'GREGALE_ISSUES_SMOKE_TOKEN_B'),
  ];
  if (tokens.some(token => !token.startsWith('g_issue_'))) throw new Error('both smoke reporter tokens must be deployment-bound g_issue_ credentials');
  if (tokens[0] === tokens[1]) throw new Error('the two smoke reporter tokens must be distinct');

  const deployments = [
    required(env, 'GREGALE_ISSUES_SMOKE_DEPLOYMENT_A'),
    required(env, 'GREGALE_ISSUES_SMOKE_DEPLOYMENT_B'),
  ];
  if (deployments.some(deployment => !uuidPattern.test(deployment))) throw new Error('both smoke deployment IDs must be UUIDs');
  if (deployments[0].toLowerCase() === deployments[1].toLowerCase()) throw new Error('the two smoke deployment IDs must be different');

  const requestID = env.GREGALE_ISSUES_SMOKE_REQUEST_ID?.trim() || '';
  if (requestID.length > 256) throw new Error('GREGALE_ISSUES_SMOKE_REQUEST_ID exceeds 256 characters');
  const invocationID = env.GREGALE_ISSUES_SMOKE_INVOCATION_ID?.trim() || '';
  if (invocationID && !uuidPattern.test(invocationID)) {
    throw new Error('GREGALE_ISSUES_SMOKE_INVOCATION_ID must be a UUID from an invocation of this app');
  }
  const timeoutMs = Number(env.GREGALE_ISSUES_SMOKE_TIMEOUT_MS || 30000);
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 120000) throw new Error('GREGALE_ISSUES_SMOKE_TIMEOUT_MS must be from 1000 to 120000');

  return Object.freeze({
    apiURL,
    app,
    environment,
    readToken,
    tokens,
    deployments: deployments.map(deployment => deployment.toLowerCase()),
    requestID,
    invocationID: invocationID.toLowerCase(),
    timeoutMs,
  });
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

export function assertIssueGroup(detail, expected) {
  const issue = detail?.issue;
  assert(issue && issue.id, 'issue detail response omitted its issue');
  assert(['open', 'resolved', 'ignored'].includes(issue.state), `smoke issue has an unknown state: ${issue.state}`);
  assert(issue.event_count >= expected.eventIDs.length, `smoke issue contains ${issue.event_count} events; expected at least ${expected.eventIDs.length}`);

  const byID = new Map((detail.events || []).map(event => [event.event_id, event]));
  for (const eventID of expected.eventIDs) assert(byID.has(eventID), `issue ${issue.id} did not contain smoke event ${eventID}`);
  const releases = new Set((detail.releases || []).map(release => release.deployment_id));
  for (const deployment of expected.deployments) assert(releases.has(deployment), `issue ${issue.id} is missing release ${deployment}`);

  for (let i = 0; i < expected.eventIDs.length; i++) {
    const event = byID.get(expected.eventIDs[i]);
    assert(
      event.deployment_id === expected.deployments[i],
      `event ${event.event_id} belongs to ${event.deployment_id}; expected ${expected.deployments[i]}`,
    );
    assert(event.exception_type === expected.exceptionType, `event ${event.event_id} has an unexpected exception type`);
    assert(event.source_kind === expected.sourceKind, `event ${event.event_id} has source ${event.source_kind}; expected ${expected.sourceKind}`);
  }

  if (expected.requestID) {
    const event = byID.get(expected.eventIDs[0]);
    assert(event.request_id === expected.requestID, 'request ID was not retained on the first smoke occurrence');
    assert(event.debug_request_id, 'no unambiguous retained request debugger link was found for GREGALE_ISSUES_SMOKE_REQUEST_ID');
  }
  if (expected.invocationID) {
    const event = byID.get(expected.eventIDs[1]);
    assert(event.invocation_id === expected.invocationID, 'worker invocation ID was not retained on the second smoke occurrence');
  }
  return issue.id;
}

async function requestJSON(fetchImpl, url, token) {
  const response = await fetchImpl(url, {
    headers: { Accept: 'application/json', Authorization: `Bearer ${token}` },
    redirect: 'error',
    signal: AbortSignal.timeout(10000),
  });
  if (!response.ok) {
    let problem = '';
    try {
      const body = await response.json();
      problem = body.detail || body.title || body.code || '';
    } catch { /* Keep transport errors concise and never echo credentials. */ }
    throw new Error(`Gregale API returned HTTP ${response.status}${problem ? `: ${problem}` : ''}`);
  }
  return response.json();
}

async function issueList(config, fetchImpl, cursor = '') {
  const url = new URL(`/v1/apps/${encodeURIComponent(config.app)}/issues`, config.apiURL);
  url.searchParams.set('environment', config.environment);
  if (cursor) url.searchParams.set('cursor', cursor);
  return requestJSON(fetchImpl, url, config.readToken);
}

async function issueDetail(config, fetchImpl, issueID) {
  const detailURL = new URL(`/v1/apps/${encodeURIComponent(config.app)}/issues/${encodeURIComponent(issueID)}`, config.apiURL);
  const merged = { issue: undefined, events: [], releases: [], activity: [] };
  let eventCursor = '';
  let releaseCursor = '';
  let activityCursor = '';
  for (let page = 0; page < 20; page++) {
    const url = new URL(detailURL);
    if (eventCursor) url.searchParams.set('event_cursor', eventCursor);
    if (releaseCursor) url.searchParams.set('release_cursor', releaseCursor);
    if (activityCursor) url.searchParams.set('activity_cursor', activityCursor);
    const current = await requestJSON(fetchImpl, url, config.readToken);
    merged.issue = current.issue;
    merged.events.push(...(current.events || []));
    merged.releases.push(...(current.releases || []));
    merged.activity.push(...(current.activity || []));
    eventCursor = current.next_event_cursor || '';
    releaseCursor = current.next_release_cursor || '';
    activityCursor = current.next_activity_cursor || '';
    if (!eventCursor && !releaseCursor && !activityCursor) return merged;
  }
  throw new Error(`issue ${issueID} exceeded the 20-page smoke-test detail limit`);
}

async function findSmokeDetail(config, fetchImpl, exceptionType, expectedEventIDs) {
  let cursor = '';
  const wanted = new Set(expectedEventIDs);
  for (let page = 0; page < 20; page++) {
    const listing = await issueList(config, fetchImpl, cursor);
    for (const issue of listing.items || []) {
      if (!issue.title?.startsWith(`${exceptionType}:`)) continue;
      const detail = await issueDetail(config, fetchImpl, issue.id);
      if (detail.events?.some(event => wanted.has(event.event_id))) return detail;
    }
    cursor = listing.next_cursor || '';
    if (!cursor) return undefined;
  }
  throw new Error('app issue listing exceeded the 20-page smoke-test limit');
}

async function waitForSmokeGroup(config, fetchImpl, expected) {
  const deadline = Date.now() + config.timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const detail = await findSmokeDetail(config, fetchImpl, expected.exceptionType, expected.eventIDs);
      if (detail) return assertIssueGroup(detail, expected);
    } catch (error) {
      lastError = error;
    }
    await new Promise(resolvePromise => setTimeout(resolvePromise, 1000));
  }
  throw new Error(
    `timed out waiting for ${expected.sourceKind} smoke issue${lastError ? `: ${lastError.message}` : ''}`,
  );
}

async function triggerFailure(sample, path, headers = {}) {
  const response = await fetch(new URL(path, sample.url), { method: 'POST', headers });
  let body;
  try { body = await response.json(); } catch { body = {}; }
  if (response.status !== 500 || !body.event_id) {
    throw new Error(`sample app ${path} returned HTTP ${response.status} without a reported event`);
  }
  return body.event_id;
}

export async function runIssuesSmoke(env = process.env, fetchImpl = globalThis.fetch) {
  const config = readSmokeConfig(env);
  const [tokenA, tokenB] = config.tokens;
  const [deploymentA, deploymentB] = config.deployments;
  const httpType = 'GregaleIssuesSmokeHTTPFailure';
  const workerType = 'GregaleIssuesSmokeWorkerFailure';

  console.log(`Sending Gregale Issues smoke events to ${config.apiURL.origin} for app ${config.app}.`);
  let sampleA;
  let sampleB;
  let httpIssueID;
  let workerIssueID;
  try {
    sampleA = await startIssuesSmokeSample({ baseURL: config.apiURL.origin, app: config.app, token: tokenA, fetch: fetchImpl });
    sampleB = await startIssuesSmokeSample({ baseURL: config.apiURL.origin, app: config.app, token: tokenB, fetch: fetchImpl });
    const requestHeaders = config.requestID ? { 'x-gregale-request-id': config.requestID } : {};
    const invocationHeaders = config.invocationID ? { 'x-gregale-invocation-id': config.invocationID } : {};
    const httpEvents = [
      await triggerFailure(sampleA, '/fail/http', requestHeaders),
      await triggerFailure(sampleB, '/fail/http'),
    ];
    const workerEvents = [
      await triggerFailure(sampleA, '/fail/worker'),
      await triggerFailure(sampleB, '/fail/worker', invocationHeaders),
    ];

    for (const [label, sample] of [['deployment A', sampleA], ['deployment B', sampleB]]) {
      const stats = sample.stats();
      if (stats.accepted !== 2 || stats.dropped !== 0 || stats.queued !== 0) {
        throw new Error(
          `${label} sample reporter did not flush both events (accepted ${stats.accepted}, dropped ${stats.dropped}, queued ${stats.queued})`,
        );
      }
    }

    httpIssueID = await waitForSmokeGroup(config, fetchImpl, {
      exceptionType: httpType, sourceKind: 'exception', eventIDs: httpEvents,
      deployments: [deploymentA, deploymentB], requestID: config.requestID, invocationID: '',
    });
    workerIssueID = await waitForSmokeGroup(config, fetchImpl, {
      exceptionType: workerType, sourceKind: 'worker', eventIDs: workerEvents,
      deployments: [deploymentA, deploymentB], requestID: '', invocationID: config.invocationID,
    });
  } finally {
    await Promise.all([sampleA, sampleB].filter(Boolean).map(sample => sample.close()));
  }

  console.log(`PASS: HTTP exception grouped across releases as issue ${httpIssueID}.`);
  console.log(`PASS: worker exception grouped across releases as issue ${workerIssueID}.`);
  if (config.requestID) console.log('PASS: retained request ID resolves to a request debugger link.');
  else console.log('SKIP: request debugger link check; set GREGALE_ISSUES_SMOKE_REQUEST_ID to a retained request ID from deployment A.');
  if (config.invocationID) console.log('PASS: worker occurrence retains the supplied invocation ID for its dashboard link.');
  else console.log('SKIP: invocation link check; set GREGALE_ISSUES_SMOKE_INVOCATION_ID to a real invocation UUID for this app.');
  return { httpIssueID, workerIssueID };
}

const isDirect = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isDirect) {
  runIssuesSmoke().catch(error => {
    console.error(`Gregale Issues smoke failed: ${error.message}`);
    process.exitCode = 1;
  });
}
