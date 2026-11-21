import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { GregaleFlags, GREGALE_FLAG_CONTEXT_HEADER, type FlagsBundle } from '../src/flags.js';

// ADR 20261119173702 §1: a rule_match on a subject experiment queues one
// exposure per flag, customer, subject and variant an hour; flush sends it
// beside the events.
const vectors = JSON.parse(readFileSync(new URL('../../../../pkg/flags/testdata/decisions.json', import.meta.url), 'utf8')) as { bundle: FlagsBundle };
const customer = '00000000-0000-4000-8000-000000000101';

test('subject experiments report deduplicated exposures', async () => {
  const sent: Array<{ events: unknown[]; exposures?: Array<Record<string, unknown>> }> = [];
  let now = Date.UTC(2026, 10, 19, 12);
  const flags = new GregaleFlags({
    apiURL: 'https://api.example.com', identityEndpoint: 'http://127.0.0.1/token', now: () => now,
    fetch: async (input, init) => {
      const url = String(input);
      if (url.includes('127.0.0.1')) return new Response(JSON.stringify({ access_token: 'token' }));
      if (url.endsWith('/v1/runtime/flags')) return new Response(JSON.stringify(vectors.bundle));
      sent.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ accepted: 0 }));
    },
  });
  await flags.start();
  flags.close();
  const serve = () => flags.runRequest({ [GREGALE_FLAG_CONTEXT_HEADER]: customer }, () => flags.withSubject('user-7', () => flags.variant('subject-checkout', 'control').value));
  assert.equal(await serve(), 'new');
  assert.equal(await serve(), 'new');
  now += 61 * 60 * 1000;
  await serve();
  await flags.runRequest({ [GREGALE_FLAG_CONTEXT_HEADER]: customer }, () => flags.variant('layered-checkout', 'control'));
  await flags.flush();
  const exposures = sent.flatMap(b => b.exposures ?? []);
  assert.equal(exposures.length, 2, 'one exposure an hour, none for customer experiments');
  assert.deepEqual(exposures[0], { flag: 'subject-checkout', rule_id: 'by-user', variant: 'new', customer_id: customer, subject: 'user-7', at: '2026-11-19T12:00:00.000Z' });
});
