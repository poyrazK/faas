import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';

const RUNNER = 'gregale-mcp-sdk-interop-v3';
const CHECK = 'official_sdk_interop';
const SDK_PACKAGE = '@modelcontextprotocol/client';
const SDK_VERSION = '2.3.1';
const MODERN_VERSION = '2026-07-28';
const LEGACY_VERSION = '2025-11-25';
const RUNTIMES = {
  node: { capabilities: ['completions', 'prompts', 'resources', 'tools'], completions: true, progress: true },
  go: { capabilities: ['completions', 'prompts', 'resources', 'tools'], completions: true, progress: true },
  python: { capabilities: ['completions', 'prompts', 'resources', 'tools'], completions: true, progress: true },
};

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 1) {
    const key = argv[index];
    if (!['--node-url', '--go-url', '--python-url', '--target'].includes(key) || values[key]) {
      throw new Error('invalid_arguments');
    }
    const value = argv[index + 1];
    if (!value || value.startsWith('--')) throw new Error('invalid_arguments');
    values[key] = value;
    index += 1;
  }
  if (Object.keys(values).length !== 4 || Object.values(values).some(value => !value.trim())) {
    throw new Error('invalid_arguments');
  }
  return values;
}

function validateEndpoint(value) {
  const endpoint = new URL(value);
  if (endpoint.protocol !== 'http:' || endpoint.hostname !== '127.0.0.1'
      || !endpoint.port || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) {
    throw new Error('invalid_endpoint');
  }
  return endpoint;
}

function contentText(result) {
  return (result?.content || []).filter(item => item?.type === 'text').map(item => item.text).join('\n');
}

function resourceText(result, uri) {
  const item = (result?.contents || []).find(content => content?.uri === uri && typeof content.text === 'string');
  assert(item, 'resource content is missing');
  return item.text;
}

async function exercise(runtime, endpointValue, mode) {
  const profile = RUNTIMES[runtime];
  const endpoint = validateEndpoint(endpointValue);
  const negotiationMode = mode === 'modern' ? { pin: MODERN_VERSION } : 'legacy';
  const client = new Client(
    { name: 'gregale-sdk-qualification', version: '1.0.0' },
    { versionNegotiation: { mode: negotiationMode }, enforceStrictCapabilities: true },
  );
  const transport = new StreamableHTTPClientTransport(endpoint);
  const checks = {
    discovery: mode === 'modern' ? 'failed' : 'not_applicable',
    initialize: 'failed',
    tools_list: 'failed',
    tool_calls: 'failed',
    resources: profile.capabilities.includes('resources') ? 'failed' : 'not_supported',
    prompts: profile.capabilities.includes('prompts') ? 'failed' : 'not_supported',
    completions: profile.completions ? 'failed' : 'not_supported',
    progress: profile.progress ? 'failed' : 'not_supported',
    progress_events: 0,
  };
  let stage = 'initialize';
  try {
    await client.connect(transport);
    const expectedEra = mode === 'modern' ? 'modern' : 'legacy';
    const expectedVersion = mode === 'modern' ? MODERN_VERSION : LEGACY_VERSION;
    assert.equal(client.getProtocolEra(), expectedEra);
    assert.equal(client.getNegotiatedProtocolVersion(), expectedVersion);
    checks.initialize = 'passed';

    if (mode === 'modern') {
      stage = 'discovery';
      const discovery = client.getDiscoverResult();
      assert(discovery?.supportedVersions?.includes(MODERN_VERSION));
      checks.discovery = 'passed';
    }

    stage = 'capabilities';
    const serverCapabilities = client.getServerCapabilities() || {};
    const observedCapabilities = ['completions', 'prompts', 'resources', 'tools'].filter(name => serverCapabilities[name]);
    assert.deepEqual(observedCapabilities, profile.capabilities);

    stage = 'tools_list';
    const { tools } = await client.listTools();
    const toolNames = new Set(tools.map(tool => tool.name));
    assert(toolNames.has('greet') && toolNames.has('add'));
    if (profile.progress) assert(toolNames.has('stream_demo'));
    checks.tools_list = 'passed';

    stage = 'tool_calls';
    const add = await client.callTool({ name: 'add', arguments: { a: 7, b: 5 } });
    assert.equal(add.isError, undefined);
    assert.equal(add.structuredContent?.sum, 12);
    assert(contentText(add).includes('12'));
    const greet = await client.callTool({ name: 'greet', arguments: { name: 'Qualification' } });
    assert.equal(greet.isError, undefined);
    assert(contentText(greet).includes('Hello, Qualification!'));
    checks.tool_calls = 'passed';

    let resourceCount = 0;
    let resourceTemplateCount = 0;
    let promptCount = 0;
    if (profile.capabilities.includes('resources')) {
      stage = 'resources';
      const { resources } = await client.listResources();
      assert(resources.some(resource => resource.uri === 'greeting://welcome'));
      const welcome = await client.readResource({ uri: 'greeting://welcome' });
      assert(resourceText(welcome, 'greeting://welcome').includes('Welcome to the Gregale MCP starter.'));
      const { resourceTemplates } = await client.listResourceTemplates();
      assert(resourceTemplates.some(item => item.uriTemplate === 'customer://records/{recordId}'));
      const record = await client.readResource({ uri: 'customer://records/example-1' });
      assert.equal(JSON.parse(resourceText(record, 'customer://records/example-1')).recordId, 'example-1');
      resourceCount = resources.length;
      resourceTemplateCount = resourceTemplates.length;
      checks.resources = 'passed';

    }

    if (profile.capabilities.includes('prompts')) {
      stage = 'prompts';
      const { prompts } = await client.listPrompts();
      assert(prompts.some(prompt => prompt.name === 'summarize'));
      const summary = await client.getPrompt({
        name: 'summarize',
        arguments: { text: 'sample report', style: 'brief' },
      });
      assert(summary.messages.some(message => message.content?.text?.includes('sample report')));
      promptCount = prompts.length;
      checks.prompts = 'passed';

    }

    if (profile.completions) {
      stage = 'completions';
      const promptCompletion = await client.complete({
        ref: { type: 'ref/prompt', name: 'summarize' },
        argument: { name: 'style', value: 'exec' },
      });
      assert.deepEqual(promptCompletion.completion.values, ['executive']);
      const resourceCompletion = await client.complete({
        ref: { type: 'ref/resource', uri: 'customer://records/{recordId}' },
        argument: { name: 'recordId', value: 'example-2' },
      });
      assert.deepEqual(resourceCompletion.completion.values, ['example-2']);
      checks.completions = 'passed';
    }

    if (profile.progress) {
      stage = 'progress';
      const progressEvents = [];
      const progressToken = `${runtime}-${mode}-qualification`;
      const originalOnMessage = transport.onmessage;
      transport.onmessage = (message, extra) => {
        if (message?.method === 'notifications/progress'
            && message.params?.progressToken === progressToken) {
          progressEvents.push({ progress: message.params.progress, total: message.params.total });
        }
        originalOnMessage?.(message, extra);
      };
      const streamed = await client.callTool({
        name: 'stream_demo',
        arguments: {},
        _meta: { progressToken },
      }, { timeout: 15_000 });
      assert.equal(streamed.isError, undefined);
      assert(contentText(streamed).includes('Streaming complete.'));
      assert.deepEqual(progressEvents.map(event => event.progress), [1, 2, 3]);
      assert(progressEvents.every(event => event.total === 3));
      checks.progress_events = progressEvents.length;
      checks.progress = 'passed';
    }

    return {
      mode,
      protocol_era: expectedEra,
      protocol_version: expectedVersion,
      checks,
      tool_count: tools.length,
      resource_count: resourceCount,
      resource_template_count: resourceTemplateCount,
      prompt_count: promptCount,
      completion_count: profile.completions ? 2 : 0,
    };
  } catch {
    throw { runtime, mode, stage, reason: 'sdk_interoperability_failed' };
  } finally {
    await client.close().catch(() => {});
  }
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const packageDocument = JSON.parse(await readFile(new URL('./node_modules/@modelcontextprotocol/client/package.json', import.meta.url), 'utf8'));
  if (packageDocument.version !== SDK_VERSION) throw new Error('sdk_version_mismatch');

  const runtimes = [];
  const failures = [];
  for (const runtime of ['node', 'go', 'python']) {
    const protocols = [];
    for (const mode of ['modern', 'legacy']) {
      try {
        protocols.push(await exercise(runtime, args[`--${runtime}-url`], mode));
      } catch (failure) {
        failures.push(failure);
      }
    }
    runtimes.push({ runtime, capabilities: RUNTIMES[runtime].capabilities, protocols });
  }

  const receipt = {
    version: 1,
    runner: RUNNER,
    check: CHECK,
    status: failures.length === 0 ? 'passed' : 'failed',
    target: args['--target'].trim(),
    observed_at: new Date().toISOString(),
    sdk: { package: SDK_PACKAGE, version: packageDocument.version },
    runtimes,
    failures,
  };
  process.stdout.write(`${JSON.stringify(receipt, null, 2)}\n`);
  if (failures.length > 0) process.exitCode = 1;
}

main().catch(() => {
  process.stdout.write(`${JSON.stringify({ ok: false, error: 'sdk_runner_failed' })}\n`);
  process.exitCode = 1;
});
