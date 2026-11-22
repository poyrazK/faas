"""Durable, hash-only owner aliases shared by all starter runtimes."""

OWNER_SCHEMA = """CREATE TABLE IF NOT EXISTS gregale_mcp_task_owners (
 namespace text NOT NULL, owner_hash bytea NOT NULL, owner_group bytea NOT NULL,
 PRIMARY KEY(namespace,owner_hash), CHECK(octet_length(owner_hash)>0), CHECK(octet_length(owner_group)>0));
 CREATE INDEX IF NOT EXISTS gregale_mcp_task_owners_group_idx ON gregale_mcp_task_owners(namespace,owner_group)"""


def associate_owners(connection, namespace, owners):
    # Fast path avoids contending with workers on every poll. Only trusted,
    # server-derived hashes enter this registry; raw identity is never stored.
    ready = connection.execute("""SELECT COUNT(*)=cardinality(%s::bytea[]) AND COUNT(DISTINCT owner_group)=1 AS ready
        FROM gregale_mcp_task_owners WHERE namespace=%s AND owner_hash=ANY(%s::bytea[])""",
        (owners, namespace, owners)).fetchone()["ready"]
    if ready:
        return
    connection.execute("SELECT pg_advisory_xact_lock(hashtextextended(%s,0))", ("gregale_mcp_tasks:execution:" + namespace,))
    connection.execute("""UPDATE gregale_mcp_task_owners SET owner_group=%s WHERE namespace=%s AND owner_group IN
        (SELECT owner_group FROM gregale_mcp_task_owners WHERE namespace=%s AND owner_hash=ANY(%s::bytea[]))""",
        (owners[0], namespace, namespace, owners))
    connection.execute("""INSERT INTO gregale_mcp_task_owners(namespace,owner_hash,owner_group)
        SELECT %s,unnest(%s::bytea[]),%s ON CONFLICT(namespace,owner_hash) DO UPDATE SET owner_group=EXCLUDED.owner_group""",
        (namespace, owners, owners[0]))


def cleanup_expired_tasks(connection, namespace):
    connection.execute("SELECT pg_advisory_xact_lock(hashtextextended(%s,0))", ("gregale_mcp_tasks:admission:" + namespace,))
    connection.execute("SELECT pg_advisory_xact_lock(hashtextextended(%s,0))", ("gregale_mcp_tasks:execution:" + namespace,))
    connection.execute("DELETE FROM gregale_mcp_tasks WHERE namespace=%s AND task_id IN (SELECT task_id FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 250)", (namespace, namespace))
    connection.execute("""WITH unused AS (
 SELECT DISTINCT owners.owner_group FROM gregale_mcp_task_owners AS owners WHERE owners.namespace=%s
 AND NOT EXISTS (SELECT 1 FROM gregale_mcp_tasks AS task JOIN gregale_mcp_task_owners AS known
 ON known.namespace=task.namespace AND known.owner_hash=task.owner_hash
 WHERE task.namespace=%s AND known.owner_group=owners.owner_group AND task.expires_at>clock_timestamp()) LIMIT 250)
 DELETE FROM gregale_mcp_task_owners AS owners USING unused WHERE owners.namespace=%s AND owners.owner_group=unused.owner_group""", (namespace, namespace, namespace))
