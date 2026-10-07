import test from 'node:test';
import assert from 'node:assert/strict';
import { createPromptPolicy, createResourcePolicy, createToolPolicy } from '../tool-policy.js';

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

test('resource policy matches exact URIs and URI templates without widening the allowlist', () => {
  const policy = createResourcePolicy({
    mode: 'external-oauth', scopes: ['mcp:tools'],
    resource_scopes: { 'greeting://welcome': [], 'customer://records/{recordId}': ['records:read'] },
  });
  assert.equal(policy.canAccess('greeting://welcome', { scopes: ['mcp:tools'] }), true);
  assert.equal(policy.canAccess('customer://records/123', { scopes: ['mcp:tools'] }), false);
  assert.deepEqual(policy.requiredScopes('customer://records/123'), ['records:read']);
  assert.equal(policy.canAccess('customer://records/123', { scopes: ['mcp:tools', 'records:read'] }), true);
  assert.equal(policy.canAccess('customer://records/123/extra', { scopes: ['mcp:tools', 'records:read'] }), false);
  assert.equal(policy.canAccess('customer://other/123', { scopes: ['mcp:tools', 'records:read'] }), false);
});

test('resource callback guard rechecks verified context and omitted policies preserve endpoint-only access', async () => {
  const policy = createResourcePolicy({ mode: 'external-oauth', scopes: ['mcp:tools'], resource_scopes: { 'customer://records/{recordId}': ['records:read'] } });
  let reads = 0;
  const guarded = policy.guard('customer://records/{recordId}', async () => { reads++; });
  assert.throws(() => guarded(new URL('customer://records/123'), {}, { http: { authInfo: { scopes: ['mcp:tools'] } } }), /Resource access denied/);
  assert.equal(reads, 0);
  await guarded(new URL('customer://records/123'), {}, { http: { authInfo: { scopes: ['mcp:tools', 'records:read'] } } });
  assert.equal(reads, 1);
  assert.equal(createResourcePolicy({ mode: 'external-oauth', scopes: ['mcp:tools'] }).canAccess('customer://records/123', { scopes: ['mcp:tools'] }), true);
});

test('prompt policy is a closed name allowlist and guards rendering', () => {
  const policy = createPromptPolicy({ mode: 'external-oauth', scopes: ['mcp:tools'], prompt_scopes: { summarize: [], private_report: ['reports:read'] } });
  assert.equal(policy.canAccess('summarize', { scopes: ['mcp:tools'] }), true);
  assert.equal(policy.canAccess('private_report', { scopes: ['mcp:tools'] }), false);
  assert.deepEqual(policy.requiredScopes('private_report'), ['reports:read']);
  assert.equal(policy.canAccess('private_report', { scopes: ['mcp:tools', 'reports:read'] }), true);
  assert.equal(policy.canAccess('private_report', { scopes: ['mcp:tools', 'reports:read', 'other'] }), true);
  assert.equal(policy.canAccess('unlisted', { scopes: ['mcp:tools', 'reports:read'] }), false);
  assert.equal(createPromptPolicy({ mode: 'open', prompt_scopes: { summarize: [] } }).canAccess('summarize'), true);
  let renders = 0;
  const guarded = policy.guard('private_report', async () => { renders++; });
  assert.throws(() => guarded({}, { http: { authInfo: { scopes: ['mcp:tools'] } } }), /Prompt access denied/);
  assert.equal(renders, 0);
  guarded({}, { http: { authInfo: { scopes: ['mcp:tools', 'reports:read'] } } });
  assert.equal(renders, 1);
});

for (const [field, entry] of [
  ['resource_scopes', { 'relative/{id}': [] }],
  ['resource_scopes', { 'customer://records/{id': [] }],
  ['resource_scopes', { 'customer://records/{id}': null }],
  ['prompt_scopes', { 'bad name': [] }],
  ['prompt_scopes', { summarize: null }],
]) test(`${field} rejects invalid configuration`, () => assert.throws(() => (field === 'resource_scopes' ? createResourcePolicy : createPromptPolicy)({ mode: 'open', [field]: entry })));

test('open servers cannot require scoped resources or prompts', () => {
  assert.throws(() => createResourcePolicy({ mode: 'open', resource_scopes: { 'customer://records/{id}': ['records:read'] } }));
  assert.throws(() => createPromptPolicy({ mode: 'open', prompt_scopes: { private_report: ['reports:read'] } }));
});
