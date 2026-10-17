import assert from 'node:assert/strict';
import {test} from 'node:test';
import {loadPostgresStarter} from './helpers/postgres-pool-starter.mjs';

test('starter returns connections before releasing leases on success, rollback and connection failure', async t => {
  const {createPostgresBinding, withPostgresTransaction} = await loadPostgresStarter(t);
  for (const mode of ['commit', 'work-error', 'rollback-error', 'connect-error']) {
    const events = [];
    let connects = 0;
    class Pool {
      on() {}
      async connect() {
        if (++connects === 2 && mode === 'connect-error') throw Error('private-connect');
        return {query: async sql => {
          events.push(sql);
          if (sql === 'ROLLBACK' && mode === 'rollback-error') throw Error('private-rollback');
        }, release: destroy => {events.push(destroy ? 'destroy' : 'release');}};
      }
      async end() {events.push('end');}
    }
    const binding = createPostgresBinding(Pool);
    await binding.reload({DATABASE_URL: 'postgres://worker:private-secret@db:5432/app'});
    assert.deepEqual(events, ['SELECT 1', 'release']);
    events.length = 0;
    const work = withPostgresTransaction(binding, async () => {
      binding.invalidate(); // Retirement must wait for connection release.
      assert.ok(!events.includes('end'));
      if (mode !== 'commit') throw Error('private-work');
      return 42;
    });
    if (mode === 'commit') assert.equal(await work, 42); else await assert.rejects(work);
    binding.invalidate();
    await binding.waitForRetirement();
    assert.deepEqual(events, mode === 'connect-error' ? ['end'] :
      ['BEGIN', mode === 'commit' ? 'COMMIT' : 'ROLLBACK', mode === 'rollback-error' ? 'destroy' : 'release', 'end']);
  }
});
