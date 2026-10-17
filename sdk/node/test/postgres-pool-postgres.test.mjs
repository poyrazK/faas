import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {test} from 'node:test';
import pg from 'pg';
import {loadPostgresStarter} from './helpers/postgres-pool-starter.mjs';

const deferred = () => {let resolve; const promise = new Promise(done => {resolve = done;}); return {promise, resolve};};

test('real pg.Pool rotates credentials and drains a transaction through failed adoption and revocation',
  {skip: !process.env.DATABASE_URL, timeout: 20_000}, async t => {
    const {createPostgresBinding, withPostgresTransaction} = await loadPostgresStarter(t);
    const admin = new pg.Pool({connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 3000, query_timeout: 3000});
    const role = `pool_rotation_${randomUUID().replaceAll('-', '')}`;
    const firstPassword = randomUUID().replaceAll('-', ''), nextPassword = randomUUID().replaceAll('-', '');
    const pools = [], entered = deferred(), finish = deferred();
    const ambient = {PGHOST: 'unreachable.invalid', PGPORT: '1', PGUSER: 'wrong', PGPASSWORD: 'wrong', PGDATABASE: 'wrong'};
    const original = Object.fromEntries(Object.keys(ambient).map(key => [key, process.env[key]]));
    Object.assign(process.env, ambient);
    class Pool extends pg.Pool {
      endCalls = 0;
      constructor(config) {super(config); pools.push(this);}
      end() {this.endCalls++; return super.end();}
    }
    const binding = createPostgresBinding(Pool);
    let created = false, transaction;
    t.after(async () => {
      finish.resolve();
      try {
        await transaction?.catch(() => {});
        binding.invalidate();
        await binding.waitForRetirement(AbortSignal.timeout(5000));
        if (created) await admin.query(`DROP ROLE "${role}"`);
      } finally {
        await admin.end();
        for (const [key, value] of Object.entries(original)) {
          if (value === undefined) delete process.env[key]; else process.env[key] = value;
        }
      }
    });
    // All interpolated identifiers/passwords are locally generated hex values.
    await admin.query(`CREATE ROLE "${role}" LOGIN PASSWORD '${firstPassword}'`);
    created = true;
    const connectionURI = password => {
      const url = new URL(process.env.DATABASE_URL);
      url.username = role; url.password = password;
      return url.href;
    };
    await binding.reload({DATABASE_URL: connectionURI(firstPassword)});
    transaction = withPostgresTransaction(binding, async connection => {
      await connection.query('CREATE TEMP TABLE pool_rotation_items (value integer)');
      await connection.query('INSERT INTO pool_rotation_items VALUES (1)');
      entered.resolve(); await finish.promise;
      await connection.query('INSERT INTO pool_rotation_items VALUES (2)');
      const {rows} = await connection.query('SELECT COUNT(*)::integer AS count FROM pool_rotation_items');
      return rows[0].count;
    });
    // Observe connection errors promptly if the transaction fails before entry.
    await Promise.race([entered.promise, transaction.then(() => {throw Error('transaction ended before rotation');})]);
    await admin.query(`ALTER ROLE "${role}" PASSWORD '${nextPassword}'`);
    await binding.reload({DATABASE_URL: connectionURI(nextPassword)});
    assert.equal(pools[0].endCalls, 0);
    const lease = binding.acquire();
    try {assert.equal((await lease.current().query('SELECT current_user')).rows[0].current_user, role);}
    finally {lease.release();}
    // This also proves the test database uses password authentication. Trust
    // authentication cannot qualify a real credential-rotation test.
    await assert.rejects(binding.reload({DATABASE_URL: connectionURI(firstPassword)}));
    assert.throws(() => binding.acquire());
    await binding.reload({DATABASE_URL: connectionURI(nextPassword)});
    await assert.rejects(binding.reload({})); // Revoked/missing binding.
    assert.throws(() => binding.acquire());
    assert.equal(pools[0].endCalls, 0);
    finish.resolve(); assert.equal(await transaction, 2);
    await binding.waitForRetirement(AbortSignal.timeout(5000));
    assert.equal(pools.length, 4);
    for (const pool of pools) assert.equal(pool.endCalls, 1);
  });
