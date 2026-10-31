import {PostgresPoolBinding} from '@gregale/sdk-node/preview/postgres/runtime';

// Supply pg.Pool from the application's driver dependency. The SDK has none.
export function createPostgresBinding(Pool) {
  return new PostgresPoolBinding('DATABASE_URL', config => {
    const pool = new Pool({...config, max: 10, connectionTimeoutMillis: 3000, query_timeout: 3000});
    // pg emits idle connection failures on the pool. Avoid logging driver
    // errors or connection configuration, which can contain credentials.
    pool.on('error', () => console.error('Postgres idle connection failed'));
    return pool;
  }, {
    validate: async (pool, signal) => {
      signal?.throwIfAborted();
      const connection = await pool.connect();
      try {
        signal?.throwIfAborted();
        await connection.query('SELECT 1');
        signal?.throwIfAborted();
      } finally {
        connection.release();
      }
    },
    retire: pool => pool.end(),
    onRetireError: () => console.error('Postgres pool retirement failed'),
  });
}

export async function withPostgresTransaction(binding, work) {
  const lease = binding.acquire();
  try {
    const connection = await lease.current().connect();
    let destroyConnection = false;
    try {
      await connection.query('BEGIN');
      const result = await work(connection);
      await connection.query('COMMIT');
      return result;
    } catch (error) {
      try {await connection.query('ROLLBACK');} catch {destroyConnection = true;}
      throw error;
    } finally {
      // Return/destroy the driver's connection before releasing its pool lease.
      connection.release(destroyConnection);
    }
  } finally {
    lease.release();
  }
}
