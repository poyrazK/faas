// test/function-config.test.ts — defineConfig is an identity helper
// (ADR-20261107193000): gregale deploy reads the literal statically.

import test from 'node:test';
import assert from 'node:assert/strict';

import { defineConfig } from '../src/function-config.js';

test('defineConfig returns its argument unchanged', () => {
  const config = { cron: '0 8 * * *', ramMb: 512, queue: ['a', 'b'] };
  assert.equal(defineConfig(config), config);
});
