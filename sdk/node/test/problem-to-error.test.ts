import test from 'node:test';
import assert from 'node:assert/strict';

import {
  ErrCapacity,
  ErrNotFound,
  ErrRateLimited,
  ErrUnauthorized,
  isFaasError,
  problemToError,
} from '../src/index.js';

// Hunt 14 H14-91: GET /v1/status/incidents/{id} with a bad id answered
// 400 validation_failed on production-us, and the SDK raised it as
// ErrUnauthorized, so a caller that re-authenticates on that error would do
// so for a validation failure.
test('problems without a canonical code or status are the base FaasError', () => {
  for (const [status, code] of [[400, 'validation_failed'], [409, 'conflict'], [422, 'traffic_policy_too_complex'], [500, 'internal_error']] as const) {
    const err = problemToError({ code, detail: 'x' }, status);
    assert.ok(isFaasError(err), `${status} ${code} is a FaasError`);
    assert.ok(!(err instanceof ErrUnauthorized), `${status} ${code} must not be ErrUnauthorized`);
    assert.equal(err.name, 'FaasError');
    assert.equal(err.problem.code, code);
    assert.equal(err.status, status);
  }
});

test('canonical codes and statuses keep their sentinels', () => {
  assert.ok(problemToError({ code: 'not_found' }, 404) instanceof ErrNotFound);
  assert.ok(problemToError({ code: 'unauthorized' }, 401) instanceof ErrUnauthorized);
  assert.ok(problemToError({ code: 'rate_limited' }, 429) instanceof ErrRateLimited);
  assert.ok(problemToError({ code: 'capacity_unavailable' }, 503) instanceof ErrCapacity);
  assert.ok(problemToError({ code: 'something_new' }, 401) instanceof ErrUnauthorized);
  assert.ok(problemToError({}, 404) instanceof ErrNotFound);
  assert.equal(problemToError({ code: 'not_found' }, 404).name, 'ErrNotFound');
});
