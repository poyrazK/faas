import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { regionalReportArguments } from '../agent/bridge.mjs';
import { REPORT_UUID } from '../agent/report-regional.ts';
import { names } from '../scripts/manifests.mjs';

const base = '/reports';
const steps = new Set(['north', 'south', 'merge']);
const files = new Map([
  [base, ['index.html', 'text/html; charset=utf-8']],
  [`${base}/page.css`, ['page.css', 'text/css; charset=utf-8']],
  [`${base}/page.js`, ['page.js', 'text/javascript; charset=utf-8']],
  [`${base}/sample.json`, ['../sales-report/rows.json', 'application/json']],
]);
const headers = {
  'Cache-Control': 'no-store',
  'Content-Security-Policy': "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
  'Referrer-Policy': 'no-referrer',
  'X-Content-Type-Options': 'nosniff',
};

function json(body, status = 200) {
  return Response.json(body, { status, headers });
}

function reportScope(origin, customerID) {
  return createHash('sha256').update(JSON.stringify(['gregale-report-tab', origin, customerID])).digest('hex');
}

async function readBytes(stream, max) {
  if (!stream) return Buffer.alloc(0);
  const reader = stream.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return Buffer.concat(chunks, size);
      size += value.byteLength;
      if (size > max) {
        await reader.cancel();
        throw new Error('Body too large');
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
}

async function privateDownload(reports, key, fetchArtifact) {
  const signed = await reports.download(key);
  if (!signed) return json({ error: 'Report download is unavailable.' }, 404);
  const url = new URL(signed.url);
  if (url.protocol !== 'https:' || url.username || url.password || signed.method !== 'GET') {
    throw new Error('Invalid signed download');
  }
  const response = await fetchArtifact(url, {
    headers: signed.headers, redirect: 'error', signal: AbortSignal.timeout(15_000),
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new Error('Private download failed');
  }
  const body = await readBytes(response.body, 32768);
  if (!body.length) throw new Error('Private download is empty');
  return new Response(body, { headers: {
    ...headers, 'Content-Type': 'application/octet-stream',
    'Content-Disposition': 'attachment; filename="regional-sales-report.html"',
  } });
}

async function readIntent(request, rerun) {
  const body = JSON.parse((await readBytes(request.body, 16384)).toString('utf8'));
  if (typeof body?.requestID !== 'string' || !REPORT_UUID.test(body.requestID)) throw new Error('Invalid report ID');
  if (rerun) {
    const approvals = body.repeatSteps;
    if (!Array.isArray(approvals) || approvals.length > steps.size ||
        !approvals.every(step => steps.has(step)) || new Set(approvals).size !== approvals.length) {
      throw new Error('Invalid repeat approvals');
    }
    return { requestID: body.requestID, repeatSteps: approvals };
  }
  regionalReportArguments(names('report-agent'), body.orders);
  return { requestID: body.requestID, orders: body.orders };
}

/**
 * Mount at /reports. Resolve the customer from the application's trusted
 * session on every request. Return null for paths outside this handler.
 */
export function createReportPage({ reports, customerForRequest, publicOrigin, fetchArtifact = globalThis.fetch }) {
  const origin = new URL(publicOrigin).origin;
  return async function reportPage(request) {
    const url = new URL(request.url);
    const path = url.pathname;
    if (path !== base && !path.startsWith(`${base}/`)) return null;
    const pathMatch = /^\/reports\/api\/([a-f0-9-]{36})\/(progress|download|rerun)$/.exec(path);
    const submitting = path === `${base}/api`;
    const resource = files.get(path);
    if (!(resource || submitting || (pathMatch && REPORT_UUID.test(pathMatch[1])))) {
      return json({ error: 'Report page not found.' }, 404);
    }
    const action = pathMatch?.[2];
    const method = submitting || action === 'rerun' ? 'POST' : 'GET';
    if (request.method !== method) return new Response(null, { status: 405, headers: { ...headers, Allow: method } });
    if (method === 'POST' && (request.headers.get('origin') !== origin ||
        request.headers.get('content-type')?.split(';')[0].trim().toLowerCase() !== 'application/json')) {
      return json({ error: 'Use the report page to send this request.' }, 403);
    }
    try {
      const customerID = await customerForRequest(request);
      if (typeof customerID !== 'string' || !customerID.trim()) return json({ error: 'Sign in to access reports.' }, 401);
      if (resource) {
        const [file, type] = resource;
        const contents = await readFile(new URL(file, import.meta.url), 'utf8');
        const body = path === base ? contents.replace('%%REPORT_SCOPE%%', reportScope(origin, customerID)) : contents;
        return new Response(body, { headers: { ...headers, 'Content-Type': type } });
      }
      if (method === 'POST') {
        if (request.headers.get('x-report-session') !== reportScope(origin, customerID)) {
          return json({ error: 'The report session changed. Reload the page.' }, 403);
        }
        let intent;
        try { intent = await readIntent(request, !submitting); }
        catch { return json({ error: 'Check the order details or repeat approvals.' }, 400); }
        const report = submitting
          ? await reports.submitRegional({ customerID, requestID: intent.requestID }, intent.orders)
          : await reports.rerun({ customerID, requestID: pathMatch[1] }, intent);
        return report ? json(report, 202) : json({ error: 'Report not found.' }, 404);
      }
      const key = { customerID, requestID: pathMatch[1] };
      if (action === 'download') return await privateDownload(reports, key, fetchArtifact);
      const raw = url.searchParams.get('after') ?? '0';
      const after = Number(raw);
      if (!/^\d+$/.test(raw) || !Number.isSafeInteger(after)) return json({ error: 'Invalid progress cursor.' }, 400);
      const progress = await reports.progress(key, after);
      return progress ? json(progress) : json({ error: 'Report not found.' }, 404);
    } catch {
      return json({ error: 'Report service is unavailable. Try again.' }, 503);
    }
  };
}
