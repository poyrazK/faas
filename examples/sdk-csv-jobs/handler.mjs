import pg from 'pg';

/** Stable input IDs are the application's idempotency keys, across run replay. */
export function csvHandler(databaseURL) {
  const pool=new pg.Pool({connectionString:databaseURL,connectionTimeoutMillis:3000});
  return {close:()=>pool.end(),async handle(input,context){
    context.signal.throwIfAborted();
    if(!/^[^@\s]+@[^@\s]+$/.test(input.email))return {row:input.row,saved:false,rejection:'invalid_email'};
    const attempt=await pool.query('INSERT INTO sdk_csv_attempts VALUES ($1, 1) ON CONFLICT (input_id) DO UPDATE SET calls=sdk_csv_attempts.calls+1 RETURNING calls',[context.inputId]);
    // A durable marker lets the same input recover in a new run/process.
    if(input.fail_once&&attempt.rows[0].calls===1)throw new Error('Injected transient import failure');
    context.signal.throwIfAborted();
    await pool.query('INSERT INTO sdk_csv_rows VALUES ($1,$2,$3) ON CONFLICT (input_id) DO NOTHING',[context.inputId,input.row,input.email]);
    return {row:input.row,saved:true,rejection:'none'};
  }};
}
