import test from 'node:test';
import assert from 'node:assert/strict';
import { createToolPolicy } from '../tool-policy.js';

const auth = { mode: 'external-oauth', scopes: ['mcp:tools'], tool_scopes: { greet: [], add: ['math:read', 'math:write'] } };
test('configured policy denies missing tools and requires every scope', () => {
  const policy = createToolPolicy(auth);
  assert.equal(policy.canAccess('greet', { scopes: ['mcp:tools'] }), true);
  assert.equal(policy.canAccess('add', { scopes: ['mcp:tools', 'math:read'] }), false);
  assert.equal(policy.canAccess('add', { scopes: ['mcp:tools', 'math:read', 'math:write'] }), true);
  assert.equal(policy.canAccess('add', { scopes: ['math:read', 'math:write'] }), false);
  for (const name of ['stream_demo', 'toString', '__proto__']) assert.equal(policy.canAccess(name, { scopes: ['mcp:tools', 'math:read', 'math:write'] }), false);
  assert.equal(createToolPolicy({ mode: 'open', tool_scopes: {} }).canAccess('greet'), false);
});
test('execution guard independently rejects missing or insufficient verified context', async () => {
  let executions = 0;
  const guarded = createToolPolicy(auth).guard('add', () => { executions++; });
  for (const ctx of [{}, { http: {} }, { http: { authInfo: { scopes: ['mcp:tools'] } } }]) {
    assert.throws(() => guarded({}, ctx), /Tool access denied/);
    assert.equal(executions, 0);
  }
  await guarded({}, { http: { authInfo: { scopes: ['mcp:tools', 'math:read', 'math:write'] } } });
  assert.equal(executions, 1);
});
test('omitted policy retains endpoint-only compatibility', () => {
  assert.equal(createToolPolicy({ mode: 'open' }).canAccess('existing'), true);
  const policy = createToolPolicy({ mode: 'external-oauth', scopes: ['mcp:tools'] });
  assert.equal(policy.canAccess('existing', { scopes: ['mcp:tools'] }), true);
  assert.equal(policy.canAccess('existing'), false);
});
for (const [name, tool_scopes] of [
  ['null policy', null], ['array policy', []], ['null scopes', { greet: null }],
  ['string scopes', { greet: 'read' }], ['empty name', { '': [] }], ['control name', { 'bad\0name': [] }],
  ['empty scope', { greet: [''] }], ['space in scope', { greet: ['two words'] }],
  ['quote in scope', { greet: ['quote"'] }], ['backslash in scope', { greet: ['slash\\'] }],
  ['nonascii scope', { greet: ['nonascii:é'] }],
]) test(name, () => assert.throws(() => createToolPolicy({ ...auth, tool_scopes })));
test('public mode cannot grant scoped tools', () => assert.throws(() => createToolPolicy({ mode: 'open', tool_scopes: { greet: ['read'] } })));
