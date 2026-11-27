import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { evaluateFlag, evaluateVariant, GregaleFlags, type FlagsBundle } from '../src/flags.js';

// ADR 20261114150000: attribute targeting and kills share decision vectors
// with pkg/flags and the Python and Go SDKs.
type DecisionCase = { name: string; flag: string; type?: string; customer?: string; subject?: string; attributes?: Record<string, string>; kills?: string[]; fallback: boolean | string; value: boolean | string; reason: string; rule_id?: string };
const decisionVectors = () => JSON.parse(readFileSync(new URL('../../../../pkg/flags/testdata/decisions.json', import.meta.url), 'utf8')) as { bundle: FlagsBundle; cases: DecisionCase[] };
const tenant = '00000000-0000-4000-8000-000000000001';

test('cross-language decision vectors', () => {
  const { bundle, cases } = decisionVectors();
  for (const c of cases) {
    const d = c.type === 'variant'
      ? evaluateVariant(bundle, c.flag, c.customer, c.fallback as string, c.subject, c.attributes, c.kills)
      : evaluateFlag(bundle, c.flag, c.customer, c.fallback as boolean, c.subject, c.attributes, c.kills);
    assert.equal(d.value, c.value, c.name);
    assert.equal(d.reason, c.reason, c.name);
    assert.equal(d.rule_id, c.rule_id, c.name);
  }
});

function client(bundle: FlagsBundle, seen: { capabilities: string }): GregaleFlags {
  return new GregaleFlags({
    apiURL: 'https://api.example.com', identityEndpoint: 'http://127.0.0.1/token',
    fetch: async (input, init) => {
      if (String(input).includes('127.0.0.1')) return new Response(JSON.stringify({ access_token: 'token' }));
      seen.capabilities = new Headers(init?.headers).get('X-Faas-Flags-Capabilities') ?? '';
      return new Response(JSON.stringify(bundle));
    },
  });
}

test('gateway attributes and withAttributes feed attribute rules', async () => {
  const seen = { capabilities: '' };
  const flags = client(decisionVectors().bundle, seen);
  await flags.start();
  flags.close();
  assert.match(seen.capabilities, /attribute-targeting-v1/);
  assert.match(seen.capabilities, /kill-v1/);

  const eu = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Client-Country': 'DE', 'X-Faas-Client-Ua-Family': 'chrome' }, () => flags.boolean('eu-checkout', false));
  assert.equal(eu.value, true);
  assert.equal(eu.rule_id, 'eu');

  // ADR 20261119102741 §4: the gateway's cohort header is gregale.cohort.
  assert.match(seen.capabilities, /cohort-targeting-v1/);
  const power = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Flag-Cohorts': 'beta,onboarded' }, () => flags.boolean('power-tools', false));
  assert.equal(power.rule_id, 'power');
  const none = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Flag-Cohorts': '-' }, () => flags.boolean('power-tools', false));
  assert.equal(none.rule_id, 'newcomers');
  const forged = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Flag-Cohorts': 'Not A Key' }, () => flags.boolean('power-tools', false));
  assert.equal(forged.reason, 'default');

  // A malformed gateway value is ignored rather than matched.
  const malformed = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Client-Country': 'de' }, () => flags.boolean('eu-checkout', false));
  assert.equal(malformed.value, false);

  const pro = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Client-Ua-Family': 'chrome' },
    () => flags.withAttributes({ plan: 'pro' }, () => flags.boolean('pro-only', false)));
  assert.equal(pro.value, true);

  // A gateway kill beats a matching rule and is recorded as killed.
  const killed = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Client-Country': 'DE', 'X-Faas-Flag-Kills': 'eu-checkout' }, () => flags.boolean('eu-checkout', false));
  assert.equal(killed.value, false);
  assert.equal(killed.reason, 'killed');
  // A flag killed in the bundle serves the fallback, not its default (true).
  const bundled = await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant }, () => flags.boolean('risky-path', false));
  assert.equal(bundled.value, false);
  assert.equal(bundled.reason, 'killed');

  await flags.runRequest({ 'X-Faas-Platform-Tenant-Id': tenant, 'X-Faas-Client-Ua-Family': 'chrome' }, async () => {
    assert.throws(() => flags.withAttributes({ 'Bad-Name': 'x' }, () => undefined));
    const dropped = await flags.withAttributes({ plan: 'pro\n' }, () => flags.boolean('pro-only', false));
    assert.equal(dropped.value, false);
  });
});
