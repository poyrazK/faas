export const MCP_TASK_OWNER_SCHEMA = `CREATE TABLE IF NOT EXISTS gregale_mcp_task_owners (
 namespace text NOT NULL, owner_hash bytea NOT NULL, owner_group bytea NOT NULL,
 PRIMARY KEY(namespace,owner_hash), CHECK(octet_length(owner_hash)>0), CHECK(octet_length(owner_group)>0));
 CREATE INDEX IF NOT EXISTS gregale_mcp_task_owners_group_idx ON gregale_mcp_task_owners(namespace,owner_group)`;

// Caller hashes must be derived on the server, never supplied by the caller.
// Unknown retained owners share one conservative bucket until authentication
// associates their aliases. The execution lock also serializes all group merges.
export async function associateMcpTaskOwners(client, namespace, owners) {
  const result = await client.query(`SELECT COUNT(*)=cardinality($2::bytea[]) AND COUNT(DISTINCT owner_group)=1 AS ready
    FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=ANY($2::bytea[])`, [namespace, owners]);
  if (result.rows[0]?.ready) return;
  await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', ['gregale_mcp_tasks:execution:' + namespace]);
  await client.query(`UPDATE gregale_mcp_task_owners SET owner_group=$3 WHERE namespace=$1 AND owner_group IN
    (SELECT owner_group FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=ANY($2::bytea[]))`, [namespace, owners, owners[0]]);
  await client.query(`INSERT INTO gregale_mcp_task_owners(namespace,owner_hash,owner_group)
    SELECT $1,unnest($2::bytea[]),$3 ON CONFLICT(namespace,owner_hash) DO UPDATE SET owner_group=EXCLUDED.owner_group`, [namespace, owners, owners[0]]);
}

export const mcpTaskOwnerGroup = alias => `COALESCE((SELECT owner_group FROM gregale_mcp_task_owners WHERE namespace=${alias}.namespace AND owner_hash=${alias}.owner_hash),''::bytea)`;

export const MCP_TASK_PRUNE_OWNERS = `WITH unused AS (
 SELECT DISTINCT owners.owner_group FROM gregale_mcp_task_owners AS owners WHERE owners.namespace=$1
 AND NOT EXISTS (SELECT 1 FROM gregale_mcp_tasks AS task JOIN gregale_mcp_task_owners AS known
 ON known.namespace=task.namespace AND known.owner_hash=task.owner_hash
 WHERE task.namespace=$1 AND known.owner_group=owners.owner_group AND task.expires_at>clock_timestamp()) LIMIT 250)
 DELETE FROM gregale_mcp_task_owners AS owners USING unused WHERE owners.namespace=$1 AND owners.owner_group=unused.owner_group`;
