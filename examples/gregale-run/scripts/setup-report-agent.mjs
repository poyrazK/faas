import { execFile } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseArgs, promisify } from 'node:util';
import { createReportIntake } from '../agent/report-intake.ts';
import { buildExamples, names } from './manifests.mjs';

const run = promisify(execFile);
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const slug = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
const reportBytes = 32768;
const endpointPaths = {
  connect_path: '/realtime/connect',
  message_path: '/realtime/message',
  disconnect_path: '/realtime/disconnect',
};

function origin(value, { loopback = false, configuredRuntime = false } = {}) {
  let url;
  try { url = new URL(value); } catch { throw new Error('Provide a valid Gregale origin'); }
  const local = loopback && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  if ((url.protocol !== 'https:' && !((local || configuredRuntime) && url.protocol === 'http:')) ||
      url.username || url.password || url.pathname !== '/' || url.search || url.hash) {
    throw new Error('Provide a Gregale HTTPS origin');
  }
  return url.origin;
}

function apiClient(apiURL, token) {
  return async function request(method, endpoint, body, { missing = false } = {}) {
    const response = await fetch(new URL(endpoint, apiURL), {
      method, redirect: 'error', signal: AbortSignal.timeout(30_000),
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) {
      await response.body?.cancel();
      if (missing && response.status === 404) return null;
      throw new Error(`${method} ${endpoint} rejected (HTTP ${response.status})`);
    }
    try {
      const content = await response.text();
      return content ? JSON.parse(content) : null;
    } catch {
      throw new Error(`${method} ${endpoint} returned an invalid response`);
    }
  };
}

async function checkRoute(request, appPath, routeName) {
  const [routes, buckets] = await Promise.all([
    request('GET', `${appPath}/upload-routes`),
    request('GET', `${appPath}/buckets`),
  ]);
  const route = routes.items.find(item => item.name === routeName);
  const bucket = buckets.items.find(item => item.id === route?.bucket_id);
  if (!route?.enabled || !Number.isSafeInteger(route.max_bytes) || route.max_bytes < reportBytes ||
      !route.allowed_content_types?.includes('text/html') || !bucket ||
      bucket.state !== 'ready' || bucket.public !== false || bucket.serve_at) {
    throw new Error('Use an enabled upload route allowing text/html and at least 32 KiB on a ready private app bucket');
  }
}

async function checkInbox(request, app, appPath, callbackURL, ready) {
  const query = new URLSearchParams({ app_id: app.id, kind: 'realtime' });
  const [endpoints, triggers] = await Promise.all([
    request('GET', `${appPath}/realtime/endpoints`),
    request('GET', `/v1/triggers?${query}`),
  ]);
  const endpoint = endpoints?.[0];
  const trigger = triggers?.[0];
  if (endpoints?.length > 1 || triggers?.length > 1 ||
      (endpoint && (!UUID.test(endpoint.id) || endpoint.slug !== 'agent' ||
        endpoint.app_id !== app.id || endpoint.callback_url !== callbackURL ||
        !endpoint.enabled || endpoint.auth_mode !== 'none' ||
        endpoint.allowed_origins?.length || endpoint.channel_rules?.length ||
        Object.entries(endpointPaths).some(([key, value]) => endpoint[key] !== value))) ||
      (trigger && (!endpoint || trigger.slug !== 'agent-inbox' || !trigger.enabled || trigger.agent ||
        trigger.delivery !== 'record' || trigger.batch_size_max !== 1 ||
        trigger.config?.endpoint_id !== endpoint.id || trigger.config?.channel !== 'agent-inbox' ||
        trigger.config?.group !== 'slices' || trigger.config?.max_concurrency !== 4))) {
    throw new Error('The app has a conflicting realtime endpoint or trigger. Use a dedicated Report Agent app');
  }
  const groups = endpoint ? await request('GET',
    `${appPath}/realtime/endpoints/${endpoint.id}/channels/agent-inbox/groups`) : [];
  const group = groups?.find(item => item.name === 'slices');
  if (group && (group.lease_seconds !== 120 || group.ordering_metadata_key !== 'thread_id' ||
      group.deduplication_metadata_key !== 'message_id' || group.dead_letter_policy !== 'block' ||
      group.paused || group.draining || (group.trigger_id && group.trigger_id !== trigger?.id))) {
    throw new Error('The inbox has a conflicting slices consumer group');
  }
  if (ready && (!endpoint || !trigger || !group || group.trigger_id !== trigger.id)) {
    throw new Error('The deployment has no active Report Agent inbox trigger binding');
  }
  return endpoint;
}

async function publishTasks(request, tasks) {
  const versions = [];
  for (const task of [tasks.sales, tasks.merge]) {
    task.definition.limits.timeout_ms = 20_000;
    // Catalog reads redact source. Exact publication replay verifies the entire
    // immutable definition, including its endpoint delegation.
    const version = await request('POST', '/v1/run-tasks', task);
    if (version.status !== 'active' || !version.digest ||
        version.name !== task.name || version.version !== task.version ||
        version.agent_endpoint_ids?.length !== 1 || version.agent_endpoint_ids[0] !== task.agent_endpoint_ids[0]) {
      throw new Error(`Task ${task.name}@${task.version} is unavailable for this inbox`);
    }
    versions.push({ name: version.name, version: version.version, digest: version.digest });
  }
  return versions;
}

async function configureApp(request, appPath, values, databaseURL, live) {
  const { rows } = await request('GET', `${appPath}/env-diff`);
  const runtimeAPI = rows.find(row => row.key === 'GREGALE_API_URL' && row.kind === 'env' && row.cells.default?.present);
  if (runtimeAPI) {
    origin(runtimeAPI.cells.default.value, { configuredRuntime: true });
    values.GREGALE_API_URL = runtimeAPI.cells.default.value;
  }
  const managed = new Set([...Object.keys(values), 'DATABASE_URL']);
  for (const row of rows) {
    if (!managed.has(row.key)) continue;
    if (Object.entries(row.cells).some(([scope, cell]) => scope !== 'default' && cell.present) ||
        (row.cells.default?.present && (row.key === 'DATABASE_URL' ? row.kind !== 'secret' :
          row.kind !== 'env' || row.cells.default.value !== values[row.key]))) {
      throw new Error(`Conflicting app configuration for ${row.key}`);
    }
  }
  const present = key => rows.some(row => row.key === key && row.cells.default?.present);
  if (live && [...managed].some(key => !present(key))) {
    throw new Error('The live starter is missing runtime configuration. Restore its bindings and restart it fresh');
  }
  if (!present('DATABASE_URL')) {
    await request('PUT', `${appPath}/secrets/DATABASE_URL`, { value: databaseURL, secret_class: 'ephemeral' });
  }
  for (const [key, value] of Object.entries(values)) {
    if (!present(key)) await request('PUT', `${appPath}/env/${key}`, { value });
  }
}

async function releaseKey(bundle, config) {
  const hash = createHash('sha256').update(JSON.stringify(config));
  const files = (await fs.readdir(bundle, { recursive: true, withFileTypes: true }))
    .filter(file => file.isFile()).map(file => path.join(file.parentPath, file.name)).sort();
  for (const file of files) {
    const contents = await fs.readFile(file);
    hash.update(JSON.stringify([path.relative(bundle, file).split(path.sep).join('/'), contents.length]));
    hash.update(contents);
  }
  return `report-starter-${hash.digest('hex').slice(0, 24)}`;
}

async function verifyReport(intake, requestID) {
  const started = performance.now();
  const key = { customerID: 'gregale-report-starter-verification', requestID };
  const csv = await fs.readFile(new URL('../sales-report/orders.csv', import.meta.url));
  let state = await intake.submit(key, new Blob([csv]));
  while (['staging', 'queued', 'running'].includes(state.status) && performance.now() - started < 180_000) {
    await sleep(1000);
    state = await intake.status(key);
    if (!state) throw new Error('The verification request is missing from the report database');
  }
  if (state.status !== 'succeeded') throw new Error(`Sample report status is ${state.status}. Retry with the same verification UUID`);
  const { orders, units, revenue_cents, currency } = state.summary;
  if (orders !== 4 || units !== 8 || revenue_cents !== 6996 || currency !== 'USD') {
    throw new Error('Sample report totals differ from the fixture');
  }
  const other = { ...key, customerID: 'gregale-report-starter-other-customer' };
  if (await intake.status(other) || await intake.download(other)) throw new Error('Sample report customer isolation check failed');
  const download = await intake.download(key);
  let url;
  try {
    url = new URL(download.url);
    if (url.protocol !== 'https:' || url.username || url.password || download.method !== 'GET') throw new Error();
  } catch {
    throw new Error('The sample report has no valid signed HTTPS download');
  }
  let response;
  try {
    response = await fetch(url, { headers: download.headers, redirect: 'error', signal: AbortSignal.timeout(30_000) });
  } catch {
    throw new Error('Could not connect to the signed report download');
  }
  if (!response.ok) {
    await response.body?.cancel();
    throw new Error(`The signed report download returned HTTP ${response.status}`);
  }
  // Signed object URLs serve active documents as inert attachments.
  const mediaType = response.headers.get('content-type')?.split(';')[0].trim().toLowerCase();
  if (mediaType !== 'text/html' && mediaType !== 'application/octet-stream') {
    await response.body?.cancel();
    throw new Error('The signed report download returned an unexpected content type');
  }
  const chunks = [];
  let bytes = 0;
  try {
    for await (const chunk of response.body) {
      bytes += chunk.length;
      if (bytes > reportBytes) throw new Error();
      chunks.push(chunk);
    }
  } catch {
    throw new Error('Could not read the bounded sample report download');
  }
  const html = Buffer.concat(chunks).toString('utf8');
  if (!html.includes('4 orders · 8 units · 6996 USD cents')) throw new Error('The private HTML does not match the report totals');
  return { request_id: requestID, orders, units, revenue_cents, currency, elapsed_ms: Math.round(performance.now() - started) };
}

export async function setupReportAgent({ app: appSlug, route, prefix, requestID, apiURL, token, databaseURL,
  bundle, db, deploy, progress = () => {} }) {
  const started = performance.now();
  const request = apiClient(apiURL, token);
  const appPath = `/v1/apps/${appSlug}`;
  const [app, capabilities] = await Promise.all([
    request('GET', appPath),
    request('GET', '/v1/executions/capabilities'),
  ]);
  if (app.slug !== appSlug || !UUID.test(app.id) || app.type !== 'function' || app.runtime !== 'node24' || app.project_id) {
    throw new Error('Reserve a dedicated standalone Node 24 function app before running setup');
  }
  if (!capabilities.admission_available || !capabilities.task_pipelines || !capabilities.artifact_deliveries) {
    throw new Error('Enable Runs, task pipelines and private artifact deliveries for this pilot');
  }
  if (requestID && !(capabilities.file_limits?.max_file_bytes > 0)) {
    throw new Error('Enable CSV file inputs to verify a sample report');
  }
  const callbackURL = origin(app.canonical_url || app.url);
  const taskPrefix = prefix ?? `report-${appSlug.slice(0, 12)}-${app.id.slice(0, 8)}`;
  names(taskPrefix);
  await checkRoute(request, appPath, route);
  progress('Checking the dedicated app, inbox and PostgreSQL schema');
  try {
    const { rows } = await db.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS locked', [`report-starter:${app.id}`]);
    if (!rows[0]?.locked) throw new Error('busy');
  } catch {
    throw new Error('Could not lock pilot setup in PostgreSQL. Check database access and allow any other setup to finish');
  }
  try {
    const latestPath = `${appPath}/deployments/latest`;
    const latest = await request('GET', latestPath, undefined, { missing: true });
    if (latest && !/^report-starter-[0-9a-f]{24}$/.test(latest.reason ?? '')) {
      throw new Error('The app already has a deployment outside the Report Agent starter');
    }
    let endpoint = await checkInbox(request, app, appPath, callbackURL, Boolean(latest?.status === 'live'));
    try { await db.query(await fs.readFile(path.join(bundle, 'report-db.sql'), 'utf8')); }
    catch { throw new Error('Could not initialize the Report Agent schema in PostgreSQL'); }
    if (!endpoint) {
      // The Report Agent refuses client connections and authenticates trigger
      // invocations through the gateway. The callback credential is discarded.
      endpoint = await request('POST', `${appPath}/realtime/endpoints`, {
        slug: 'agent', enabled: true, auth_mode: 'none', callback_url: callbackURL,
        callback_auth_token: randomBytes(32).toString('hex'), ...endpointPaths,
      });
      if (!UUID.test(endpoint.id)) throw new Error('The API returned an invalid inbox identity');
    }
    const values = {
      REPORT_APP: appSlug, REPORT_ENDPOINT_ID: endpoint.id,
      REPORT_UPLOAD_ROUTE: route, REPORT_TASK_PREFIX: taskPrefix, GREGALE_API_URL: apiURL,
    };
    await configureApp(request, appPath, values, databaseURL, latest?.status === 'live');
    progress('Publishing pinned sales and merge task versions');
    const { tasks } = await buildExamples({ prefix: taskPrefix, endpointID: endpoint.id });
    const versions = await publishTasks(request, tasks);
    const key = await releaseKey(bundle, { appID: app.id, values, versions });
    if (latest?.reason !== key || latest.status !== 'live') {
      progress('Deploying the function, ordered consumer group and realtime trigger');
      await deploy(key);
    }
    const active = await request('GET', latestPath);
    if (!UUID.test(active.id) || active.reason !== key || active.status !== 'live' || active.traffic_percent !== 100) {
      throw new Error('The Report Agent deployment is not live at full traffic');
    }
    await checkInbox(request, app, appPath, callbackURL, true);
    if (requestID) progress('Submitting the sample CSV and verifying its private report');
    const verification = requestID ? await verifyReport(createReportIntake({
      db, apiURL, token, app: appSlug, endpointID: endpoint.id, uploadRoute: route, taskPrefix,
    }), requestID) : undefined;
    return { app: appSlug, endpoint_id: endpoint.id, upload_route: route, task_prefix: taskPrefix,
      deployment_id: active.id, tasks: versions, verification, elapsed_ms: Math.round(performance.now() - started) };
  } finally {
    await db.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [`report-starter:${app.id}`]).catch(() => {});
  }
}

async function main() {
  const started = performance.now();
  const { values } = parseArgs({ options: {
    app: { type: 'string' }, route: { type: 'string', default: 'reports' },
    'task-prefix': { type: 'string' }, verify: { type: 'string' }, help: { type: 'boolean', short: 'h' },
  } });
  if (values.help) {
    console.log('Usage: npm run setup:report -- --app SLUG [--route reports] [--task-prefix PREFIX] [--verify UUID]\nSet GREGALE_API_URL, GREGALE_TOKEN and DATABASE_URL in the environment.');
    return;
  }
  if (!slug.test(values.app ?? '') || !slug.test(values.route) || (values.verify !== undefined && !UUID.test(values.verify))) {
    throw new Error('Supply a DNS-safe app and route and a lowercase UUID for --verify');
  }
  if (values['task-prefix'] !== undefined) names(values['task-prefix']);
  const { GREGALE_TOKEN: token, GREGALE_API_URL: url, DATABASE_URL: databaseURL, FAAS_TOKEN, FAAS_API_KEY, ...childEnv } = process.env;
  if (!url || !token || !/^postgres(?:ql)?:\/\//.test(databaseURL ?? '')) {
    throw new Error('Set GREGALE_API_URL, GREGALE_TOKEN and a PostgreSQL DATABASE_URL');
  }
  const apiURL = origin(url, { loopback: true });
  const work = await fs.mkdtemp(path.join(os.tmpdir(), 'gregale-report-starter-'));
  let db;
  try {
    console.error('Preparing the Report Agent bundle and PostgreSQL driver');
    const bundle = path.join(work, 'bundle');
    await run(process.execPath, [fileURLToPath(new URL('./hosted-pilot.mjs', import.meta.url)), bundle, values.app],
      { env: childEnv }).catch(() => { throw new Error('Could not generate the hosted Report Agent bundle'); });
    for (const file of ['package.json', 'package-lock.json']) await fs.copyFile(path.join(bundle, file), path.join(work, file));
    await run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: work, env: childEnv, timeout: 120_000 })
      .catch(() => { throw new Error('Could not install the starter’s locked PostgreSQL driver'); });
    const { default: pg } = await import(pathToFileURL(path.join(work, 'node_modules/pg/lib/index.js')));
    db = new pg.Client({ connectionString: databaseURL, connectionTimeoutMillis: 5000, query_timeout: 30_000 });
    await db.connect().catch(() => { throw new Error('Could not connect to the dedicated pilot database'); });
    const deploy = async key => {
      const env = { ...childEnv, FAAS_API: apiURL, FAAS_TOKEN: token, FAAS_REALTIME_PREVIEW_ACCOUNTS: '1' };
      try {
        await run('gregale', ['deploy', '--name', values.app, '--function', '--runtime', 'node24', '--handler', 'handler.handler',
          '--wait', '--timeout', '12m', '--json', '--reason', key, '--idempotency-key', key],
        { cwd: bundle, env, timeout: 780_000 });
      } catch (error) {
        const problem = error.stdout?.split('\n').reverse().find(line => /"exit_code":\d+/.test(line));
        const code = problem?.match(/"code":"([a-z0-9_]+)"/)?.[1];
        throw new Error(`Deploy did not finish for ${key}${code ? ` (${code})` : ''}. Inspect the app’s deployment status and retry setup with the same inputs`);
      }
    };
    const result = await setupReportAgent({ app: values.app, route: values.route, prefix: values['task-prefix'],
      requestID: values.verify, apiURL, token, databaseURL, bundle, db, deploy, progress: step => console.error(step) });
    console.log(JSON.stringify({ ...result, elapsed_ms: Math.round(performance.now() - started) }, null, 2));
  } finally {
    if (db) await db.end().catch(() => {});
    await fs.rm(work, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main().catch(error => {
    // API errors use status-only messages. Mask any unexpected exception that
    // could carry a PostgreSQL connection URL, a bearer token, or a signed URL.
    const message = String(error?.message ?? '');
    const secrets = [process.env.DATABASE_URL, process.env.GREGALE_TOKEN].filter(Boolean);
    console.error(secrets.some(value => message.includes(value)) ? 'Report Agent setup failed' : message);
    process.exitCode = 1;
  });
}
