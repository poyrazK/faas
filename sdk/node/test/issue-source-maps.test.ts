import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createIssueReporter } from '../src/issues.js';
import { registerIssueSourceMaps } from '../src/issue-source-maps.js';

// ADR-1113. Fixed VLQ fixtures exercise mapped/unmapped ranges and coordinates.
const fixture = {
  version: 3, sourceRoot: '../', sources: ['src/checkout.ts'], names: ['charge', 'refund'],
  mappings: 'AASAA,K,KAACC', sourcesContent: ['PRIVATE_SOURCE_CONTENT'],
};
const registration = { generatedFile: '/app/dist/chunk-a.js', payload: JSON.stringify(fixture) };
const frame = { file: '/app/dist/chunk-a.js', function: 'a', line: 1, in_app: true };

test('server symbolication sends immutable generated coordinates and rejects conflicting local mapping', async () => {
  const events: Array<Record<string, unknown>> = [];
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    groupingStrategy:'cause-chain-v1',symbolicationStrategy:'deployment-source-maps-v1',
    fetch:(async(_url,options)=>{events.push(JSON.parse(String(options?.body)));return new Response('{}',{status:events.length===1?503:202});}) as typeof fetch});
  const cause = new Error('payment failed');cause.stack='Error: payment failed\n    at a (/app/dist/chunk-a.js:1:11)';
  reporter.captureException(new Error('wrapper',{cause}));
  assert.equal(await reporter.flush(),false);assert.equal(await reporter.flush(),true);
  assert.deepEqual(events[0],events[1]);
  assert.equal(events[0]?.symbolication_strategy,'deployment-source-maps-v1');
  const reportedCause=(events[0]?.causes as Array<{frames:Array<{column?:number}>}>)[0]!;
  assert.equal(reportedCause.frames[0]?.column,11);
  await reporter.close();
  assert.throws(()=>createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    symbolicationStrategy:'deployment-source-maps-v1'}),/Server symbolication requires/);
  assert.throws(()=>createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    groupingStrategy:'failure-site-v1',symbolicationStrategy:'deployment-source-maps-v1',sourceMaps:[registration]}),/without local source maps/);
});

test('source maps use one-based stack coordinates and preserve unmapped ranges', () => {
  const maps = registerIssueSourceMaps([registration]);
  assert.deepEqual(maps.resolve(frame, 1), { file: '/app/src/checkout.ts', function: 'charge', line: 10, in_app: true });
  assert.equal(maps.resolve(frame, 6), undefined);
  assert.equal(maps.resolve(frame, 10), undefined);
  assert.equal(maps.resolve(frame, 11)?.function, 'refund');
  assert.equal(maps.resolve({ ...frame, line: 2 }, 1), undefined);
  assert.equal(maps.resolve(frame, 0), undefined);
  assert.equal(maps.resolve({ ...frame, line: Number.NaN }, 1), undefined);
  assert.equal(maps.resolve({ ...frame, file: '/other/dist/chunk-a.js' }, 1), undefined);
  assert.equal(maps.resolve({ ...frame, file: 'file:///app/dist/chunk-a.js' }, 1)?.function, 'charge');
});

test('unnamed mappings become anonymous and dependency sources remain outside the app', () => {
  const anonymous = registerIssueSourceMaps([{ ...registration, payload: JSON.stringify({ ...fixture, mappings: 'AASAA,KAAA' }) }]);
  assert.equal(anonymous.resolve(frame, 6)?.function, '<anonymous>');
  const dependency = registerIssueSourceMaps([{ ...registration, payload: JSON.stringify({
    ...fixture, sources: ['node_modules/parser/index.ts'],
  }) }]);
  assert.equal(dependency.resolve(frame, 1)?.in_app, false);
  const virtual = registerIssueSourceMaps([{ ...registration, payload: JSON.stringify({
    ...fixture, sourceRoot: 'webpack://checkout', sources: ['./src/checkout.ts'],
  }) }]);
  assert.equal(virtual.resolve(frame, 1)?.file, 'webpack://checkout/src/checkout.ts');
});

test('source map registration rejects invalid maps, ambiguous names, and resource amplification', () => {
  const invalidPayloads = [
    'not JSON', 'null', JSON.stringify({ ...fixture, version: 2 }),
    JSON.stringify({ ...fixture, sections: [] }),
    ...['!', 'g', 'ggggggggA', 'AA', 'AAAA,', ',AAAA', 'AAAA,,AAAA', 'ACAAA', 'AAAAE', 'ADAA'].map(
      mappings => JSON.stringify({ ...fixture, mappings }),
    ),
    JSON.stringify({ ...fixture, sourceRoot: 'x'.repeat(513) }),
    JSON.stringify({ ...fixture, sources: Array(8193).fill('src.ts') }),
    JSON.stringify({ ...fixture, sourcesContent: ['x'.repeat(512 << 10)] }),
    JSON.stringify({ ...fixture, mappings: Array(100001).fill('AAAA').join(',') }),
  ];
  for (const payload of invalidPayloads) {
    assert.throws(() => registerIssueSourceMaps([{ ...registration, payload }]), /Invalid issue source map/);
  }
  for (const generatedFile of ['chunk-a.js', 'https://example.com/chunk-a.js', 'file:///app/a.js?version=2']) {
    assert.throws(() => registerIssueSourceMaps([{ ...registration, generatedFile }]), /Invalid issue source map/);
  }
  assert.throws(() => registerIssueSourceMaps([registration, { ...registration, generatedFile: 'file:///app/dist/chunk-a.js' }]), /Invalid issue source map/);
  assert.throws(() => registerIssueSourceMaps(Array(33).fill(registration)), /Invalid issue source map/);
  const padded = JSON.stringify({ ...fixture, ignored: 'x'.repeat(500000) });
  assert.throws(() => registerIssueSourceMaps(Array.from({ length: 9 }, (_, i) => ({
    generatedFile: '/app/chunk-' + i + '.js', payload: padded,
  }))), /Invalid issue source map/);
});

test('source-map capture keeps raw evidence, drops source content, and retries an immutable event', async () => {
  const requests: Array<Record<string, unknown>> = [];
  const sourceMaps = [registration];
  const reporter = createIssueReporter({
    baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test',
    groupingStrategy: 'failure-site-v1', sourceMaps,
    fetch: (async (_url, options) => {
      requests.push(JSON.parse(String(options?.body)));
      return new Response('{}', { status: requests.length === 1 ? 503 : 202 });
    }) as typeof fetch,
  });
  sourceMaps[0] = { ...registration, payload: JSON.stringify({ ...fixture, names: ['changed', 'refund'] }) };
  const error = new Error('payment failed');
  error.stack = 'Error: payment failed\n    at a (/app/dist/chunk-a.js:1:1)\n    at caller (/app/dist/other.js:3:9)';
  reporter.captureException(error);
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.flush(), true);
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(requests[0]?.stack_trace, error.stack);
  assert.deepEqual((requests[0]?.frames as unknown[])[0], { file: '/app/src/checkout.ts', function: 'charge', line: 10, in_app: true });
  assert.equal(JSON.stringify(requests).includes('PRIVATE_SOURCE_CONTENT'), false);
  assert.equal(reporter.stats().mappedFrames, 1);
  assert.equal(reporter.stats().unmappedFrames, 1);
  await reporter.close();
  assert.throws(() => createIssueReporter({
    baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test', sourceMaps,
  }), /require failure-site-v1/);
});

test('source maps resolve the stack from an actual minified module failure', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-issue-maps-'));
  try {
    const bundle = join(directory, 'chunk-a.mjs');
    await writeFile(bundle, 'export function a(){throw new Error("payment failed")}');
    const requests: Array<Record<string, unknown>> = [];
    const reporter = createIssueReporter({
      baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test', groupingStrategy: 'failure-site-v1',
      sourceMaps: [{ generatedFile: bundle, payload: JSON.stringify({
        ...fixture, sourceRoot: './', mappings: 'AASAA',
      }) }],
      fetch: (async (_url, options) => {
        requests.push(JSON.parse(String(options?.body)));
        return new Response('{}', { status: 202 });
      }) as typeof fetch,
    });
    const module = await import(pathToFileURL(bundle).href) as { a(): never };
    try { module.a(); } catch (error) { reporter.captureException(error); }
    assert.equal(await reporter.close(), true);
    assert.deepEqual((requests[0]?.frames as unknown[])[0], {
      file: join(directory, 'src/checkout.ts'), function: 'charge', line: 10, in_app: true,
    });
    assert.ok(String(requests[0]?.stack_trace).includes('chunk-a.mjs'));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('cause-chain reporting maps cause frames without including source content', async () => {
  const received: any[] = [];
  const reporter=createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    groupingStrategy:'cause-chain-v1',sourceMaps:[registration],
    fetch:(async (_url,options)=>{received.push(JSON.parse(String(options?.body)));return new Response('{}',{status:202});}) as typeof fetch});
  const cause=new Error('root');cause.stack='Error: root\n    at a (/app/dist/chunk-a.js:1:1)';
  reporter.captureException(new Error('wrapper',{cause}));
  assert.equal(await reporter.close(),true);
  assert.equal(received[0].causes[0].frames[0].file,'/app/src/checkout.ts');
  assert.equal(received[0].causes[0].frames[0].function,'charge');
  assert.equal(JSON.stringify(received).includes('PRIVATE_SOURCE_CONTENT'),false);
});
