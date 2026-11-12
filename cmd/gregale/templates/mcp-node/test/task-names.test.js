import test from 'node:test';
import assert from 'node:assert/strict';
import { createMcpTaskRuntime, mcpTaskHandlers, registerMcpTaskHandler } from '../tasks.js';

test('registered visible ASCII tool names can start a runtime', async () => {
  const names = ['BuildReport', 'acme:report/build', '9lookup', '_lookup', 'a'.repeat(128)];
  for (const name of names) {
    registerMcpTaskHandler(name, { version: '1', validate() {}, async execute() { return { content: [] }; } });
  }
  let inventory;
  const store = { async initialize({ admissionHandlers }) { inventory = admissionHandlers; }, async claim() {} };
  const runtime = createMcpTaskRuntime({ store, handlers: mcpTaskHandlers, workerEnabled: false });
  try {
    await runtime.start();
    for (const name of names) assert.ok(inventory.some(handler => handler.name === name));
  } finally { await runtime.stop(); }
});

test('invalid tool names fail registration and runtime setup', () => {
  const handler = { version: '1', validate() {}, async execute() { return { content: [] }; } };
  for (const name of ['', 'with space', 'with\nnewline', '\u007f', '\u00e9', 'a'.repeat(129)]) {
    assert.throws(() => registerMcpTaskHandler(name, handler), /tool name/);
    assert.throws(() => createMcpTaskRuntime({ store: { initialize() {}, claim() {} }, handlers: { [name]: handler } }), /registry/);
  }
});
