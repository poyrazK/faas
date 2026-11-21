import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { createReportPage } from '../web/report-page.mjs';

const origin = 'https://reports.example.test';
const requestID = randomUUID();
const rerunID = randomUUID();
const orders = JSON.parse(await readFile(new URL('../sales-report/rows.json', import.meta.url), 'utf8')).rows;

function request(path, { customer = 'owner', body, from = origin, scope } = {}) {
  const headers = new Headers();
  if (customer) headers.set('x-test-session', customer);
  if (body) {
    headers.set('origin', from);
    headers.set('content-type', 'application/json');
    if (scope) headers.set('x-report-session', scope);
  }
  return new Request(origin + path, { headers, ...(body && { method: 'POST', body: JSON.stringify(body) }) });
}

test('report page enforces session scope and accepts only same-origin JSON writes', async () => {
  const calls = [];
  const handler = createReportPage({
    publicOrigin: origin, customerForRequest: request => request.headers.get('x-test-session'),
    reports: {
      submitRegional(key, orders) { calls.push(['submit', key, orders]); return { requestID: key.requestID, status: 'queued' }; },
      rerun(key, intent) { calls.push(['rerun', key, intent]); return { requestID: intent.requestID, status: 'queued' }; },
      progress(key, cursor) { calls.push(['progress', key, cursor]); return null; },
    },
  });
  const html = await (await handler(request('/reports'))).text();
  assert.match(html, /Regional sales report/);
  const scope = html.match(/name="report-session" content="([a-f0-9]{64})"/)[1];
  assert.equal(await handler(request('/elsewhere')), null);
  assert.equal((await handler(request('/reports', { customer: null }))).status, 401);
  assert.equal((await handler(request('/reports/api', { body: { requestID, orders }, scope, from: 'https://other.example' }))).status, 403);
  assert.equal((await handler(request('/reports/api', { body: { requestID, orders }, scope, customer: 'other' }))).status, 403);
  assert.equal((await handler(request('/reports/api', { body: { requestID, orders: [] }, scope }))).status, 400);
  assert.deepEqual(calls, []);
  assert.equal((await handler(request('/reports/api', { body: { requestID, orders, customerID: 'foreign' }, scope }))).status, 202);
  const path = `/reports/api/${requestID}`;
  assert.equal((await handler(request(path + '/rerun', { body: { requestID: rerunID, repeatSteps: ['merge', 'merge'] }, scope }))).status, 400);
  const approval = { requestID: rerunID, repeatSteps: ['merge'] };
  assert.equal((await handler(request(path + '/rerun', { body: { ...approval, customerID: 'foreign' }, scope }))).status, 202);
  assert.equal((await handler(request(path + '/progress?after=6', { customer: 'other' }))).status, 404);
  assert.equal((await handler(request(path + '/progress?after=-1'))).status, 400);
  assert.deepEqual(calls, [
    ['submit', { requestID, customerID: 'owner' }, orders],
    ['rerun', { requestID, customerID: 'owner' }, approval],
    ['progress', { requestID, customerID: 'other' }, 6],
  ]);
});

test('report downloads use the signed headers server-side and return a private attachment', async () => {
  const handler = createReportPage({
    publicOrigin: origin, customerForRequest: request => request.headers.get('x-test-session'),
    reports: { download: ({ customerID }) => customerID === 'owner' ? {
      url: 'https://storage.example.test/sales?token=private', method: 'GET', headers: { 'x-signed': 'binding' },
    } : null },
    fetchArtifact: async (url, options) => {
      assert.equal(url.href, 'https://storage.example.test/sales?token=private');
      assert.deepEqual(options.headers, { 'x-signed': 'binding' });
      assert.equal(options.redirect, 'error');
      return new Response('<html>Private report</html>');
    },
  });
  const path = `/reports/api/${requestID}/download`;
  assert.equal((await handler(request(path, { customer: 'other' }))).status, 404);
  const response = await handler(request(path));
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.equal(response.headers.get('content-type'), 'application/octet-stream');
  assert.equal(response.headers.get('location'), null);
  assert.match(response.headers.get('content-disposition'), /^attachment/);
  assert.equal(await response.text(), '<html>Private report</html>');
});
