"""Read-only validation of every retained Task payload before writers start."""
import time


def check_task_payload_keys(runtime):
    # v1 has no key ID, so sampling cannot establish that all retained keys are
    # available. A server cursor bounds memory; the total scan has a deadline.
    deadline = time.monotonic() + 10
    fields = ("arguments", "result", "error", "input-state")
    with runtime.pool.connection() as connection:
        with connection.transaction():
            connection.execute("SET TRANSACTION READ ONLY")
            with connection.cursor(name="gregale_task_key_preflight") as cursor:
                cursor.itersize = 8
                cursor.execute("""SELECT task_id::text AS task_id,arguments_encrypted,result_encrypted,error_encrypted,input_state_encrypted
                    FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp()""", (runtime.namespace,))
                for row in cursor:
                    if time.monotonic() >= deadline:
                        raise TimeoutError("MCP Task payload preflight timed out")
                    for field, column in zip(fields, ("arguments_encrypted", "result_encrypted", "error_encrypted", "input_state_encrypted")):
                        if row[column] is not None:
                            runtime._decrypt_payload(row[column], row["task_id"], field)
                if time.monotonic() >= deadline:
                    raise TimeoutError("MCP Task payload preflight timed out")
