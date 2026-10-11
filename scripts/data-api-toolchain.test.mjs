import assert from 'node:assert/strict'
import test from 'node:test'
import { pinnedGoToolchain } from './data-api-toolchain.mjs'

test('legacy modules pin the compiler through the go directive', () => {
  assert.equal(pinnedGoToolchain('module example\n\ngo 1.26.9\n'), '1.26.9')
})

test('the explicit compiler pin takes precedence over the language target', () => {
  assert.equal(pinnedGoToolchain('module example\n\ngo 1.26.0\n\ntoolchain go1.26.9\n'), '1.26.9')
  assert.equal(pinnedGoToolchain('go\t1.26.0 // language\n toolchain\tgo1.26.9 // compiler\n'), '1.26.9')
})

test('unpinned or malformed toolchains cannot fall back to the language target', () => {
  for (const selector of ['default', 'go1.26', 'go1.26.9rc1', 'go1.26.9-custom']) {
    assert.throws(() => pinnedGoToolchain(`go 1.26.0\ntoolchain ${selector}\n`), /exact Go release/)
  }
  assert.throws(() => pinnedGoToolchain('module example\ngo 1.26\n'), /exact Go release/)
})
