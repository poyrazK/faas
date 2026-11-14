import test from 'node:test';
import assert from 'node:assert/strict';
import { installOpenAPITools, manifest } from '../openapi-tools.js';

test('selected tools reject undeclared arguments before contacting the API', async () => {
  const tools = new Map();
  let requests = 0;
  const runtime = {
    removeTool() {}, removeResource() {}, removePrompt() {},
    registerTool(name, definition, handler) { tools.set(name, { definition, handler }); },
  };
  installOpenAPITools(runtime, { request: async () => { requests++; throw new Error('No API requests in generated contract tests'); } });
  assert.deepEqual([...tools.keys()].sort(), manifest.operations.map(op => op.id).sort());
  for (const { definition, handler } of tools.values()) {
    assert.ok(definition.inputSchema);
    assert.ok(definition.outputSchema);
    const result = await handler({ undeclared_argument: true });
    assert.equal(result.isError, true);
  }
  assert.equal(requests, 0);
});
