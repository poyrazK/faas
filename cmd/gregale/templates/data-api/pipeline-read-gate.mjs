import pg from 'pg'
import { limits } from './config.mjs'

// Discover worker-installed hooks through the serving login. This adds no
// persistent pool and never reads private pipeline receipts at runtime boot.
export async function configurePipelineReadGate(config) {
  const client = new pg.Client({ connectionString: config.connection, connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
  try {
    await client.connect()
    const result = await client.query(`SELECT p.proname,
      p.prosecdef=(p.proname='check_pipeline_read')
      AND p.proconfig=ARRAY['search_path=pg_catalog']
      AND p.prorettype='void'::regtype AND NOT p.proretset
      AND p.proowner=(SELECT nspowner FROM pg_namespace WHERE nspname='gregale_pipeline')
      AND NOT pg_has_role(current_user,p.proowner,'MEMBER')
      AND has_schema_privilege(current_user,n.oid,'USAGE')
      AND NOT has_schema_privilege(current_user,n.oid,'CREATE')
      AND has_function_privilege(current_user,p.oid,'EXECUTE')
      AND obj_description(p.oid,'pg_proc') LIKE 'gregale.pipeline.read-gate.v1:%' AS valid
      FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
      WHERE n.nspname='gregale_data_api' AND p.proname IN ('check_pipeline_read','check_pipeline_request') AND p.pronargs=0`)
    if (result.rowCount === 0) return false
    if (result.rowCount !== 2 || result.rows.some(row => row.valid !== true)) throw new Error('Pipeline read hooks need worker reconciliation')
    const previous = config.postgrestEnv.PGRST_DB_PRE_REQUEST ?? ''
    if (!['', 'gregale_data_api.check_request', 'gregale_data_api.check_deadline_request'].includes(previous)) throw new Error('Unsupported pipeline pre-request hook')
    config.pipelineReadGate = true
    config.pipelinePreviousHook = previous
    config.postgrestEnv.PGRST_DB_PRE_REQUEST = 'gregale_data_api.check_pipeline_request'
    config.postgrestEnv.PGRST_DB_CONFIG = 'false'
    return true
  } finally { await client.end() }
}
