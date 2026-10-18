"""Explicit Task schema setup, using only the migration account binding."""

import json
import os
import sys

from tasks import check_task_schema, migrate_task_schema


def main(args=None):
    args = sys.argv[1:] if args is None else args
    if len(args) != 1 or args[0] not in {"apply", "status"}:
        print(json.dumps({"ok": False, "detail": "Usage: python task_schema.py apply|status"}))
        return 1
    database_url = os.environ.get("MCP_TASK_MIGRATION_DATABASE_URL", "")
    if not database_url:
        print(json.dumps({"ok": False, "detail": "Set MCP_TASK_MIGRATION_DATABASE_URL to the schema owner account; no runtime binding fallback is used"}))
        return 1
    try:
        from psycopg.conninfo import conninfo_to_dict
        from psycopg.rows import dict_row
        from psycopg_pool import ConnectionPool

        with ConnectionPool(database_url, min_size=1, max_size=1, open=False,
                            kwargs={"row_factory": dict_row, "connect_timeout": 5,
                                    "options": conninfo_to_dict(database_url).get("options", "") + " -c statement_timeout=60000"}) as pool:
            pool.wait(timeout=10)
            if args[0] == "apply":
                migrate_task_schema(pool)
            check_task_schema(pool)
    except Exception:
        # Driver errors may contain credential-bearing URLs or application data.
        print(json.dumps({"ok": False, "detail": "Task schema operation failed; check schema ownership, PUBLIC CREATE grants, connectivity and starter version"}))
        return 1
    print(json.dumps({"ok": True, "schema_version": 1}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
