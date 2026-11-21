"""Bounded authentication of ambiguous native and explicit v2 payloads."""

from cryptography.exceptions import InvalidTag


def rewrap_candidates(runtime, connection, limit):
    active = runtime.active_owner_key_id
    ambiguous = active in runtime._main_payload_keys and active in getattr(runtime, "_legacy_payload_keys", {})
    columns = ("arguments_encrypted", "result_encrypted", "error_encrypted", "input_state_encrypted")
    if not ambiguous:
        checks = [f"""({column} IS NOT NULL AND CASE
            WHEN octet_length({column})<2 THEN TRUE
            WHEN get_byte({column},0)<>2 THEN TRUE
            WHEN get_byte({column},1)=0 OR get_byte({column},1)>64 THEN TRUE
            WHEN octet_length({column})<2+get_byte({column},1)+28 THEN TRUE
            ELSE substring({column} FROM 3 FOR get_byte({column},1))<>%s::bytea END)""" for column in columns]
        rows = connection.execute("SELECT task_id::text AS task_id," + ",".join(columns) +
            " FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp() AND (" +
            " OR ".join(checks) + ") ORDER BY created_at LIMIT %s FOR UPDATE SKIP LOCKED",
            (runtime.namespace, *(active.encode("ascii"),) * 4, limit)).fetchall()
        return rows, None, False

    through = runtime._rewrap_through
    if through is None:
        row = connection.execute("SELECT task_id::text AS task_id FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp() ORDER BY task_id DESC LIMIT 1", (runtime.namespace,)).fetchone()
        through = row["task_id"] if row else None
    rows = connection.execute("SELECT task_id::text AS task_id," + ",".join(columns) +
        " FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp() "
        "AND (%s::uuid IS NULL OR task_id>%s::uuid) AND task_id<=%s::uuid "
        "ORDER BY task_id LIMIT %s FOR UPDATE SKIP LOCKED",
        (runtime.namespace, runtime._rewrap_after, runtime._rewrap_after, through, limit)).fetchall()
    return rows, through, True


def rewrap_payload(runtime, value, task_id, field, max_bytes):
    if value is None:
        return None, False
    active = runtime.active_owner_key_id
    key_id = active.encode("ascii")
    if len(value) >= 2 + len(key_id) + 28 and value[:2 + len(key_id)] == bytes((2, len(key_id))) + key_id:
        offset = 2 + len(key_id)
        aad = runtime._aad(task_id, field)
        if active in runtime._main_payload_keys:
            aad += b":key:" + key_id
        try:
            plain = runtime._aesgcm(runtime.payload_keys[active]).decrypt(
                value[offset:offset + 12], value[offset + 28:] + value[offset + 12:offset + 28], aad)
        except (InvalidTag, ValueError):
            pass
        else:
            if len(plain) > max_bytes:
                raise ValueError("MCP task payload exceeds its configured size limit")
            return value, False
    plain = runtime._decrypt_payload(value, task_id, field)
    if len(plain) > max_bytes:
        raise ValueError("MCP task payload exceeds its configured size limit")
    return runtime._encrypt_payload(plain, task_id, field), True
