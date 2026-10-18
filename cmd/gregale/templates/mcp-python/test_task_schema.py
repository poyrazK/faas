"""Prove schema setup is separate from an unprivileged runtime account."""
import contextlib
import io
import os
import unittest
import uuid
from unittest.mock import patch

from tasks import DurableTaskRuntime, check_task_schema, migrate_task_schema
from task_schema import main


class TaskMigrationBindingTests(unittest.TestCase):
    def test_no_runtime_credential_fallback(self):
        with patch.dict(os.environ, {"MCP_TASK_MIGRATION_DATABASE_URL":"", "DATABASE_URL":"postgres://runtime:never-used@127.0.0.1/runtime"}), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(main(["apply"]),1)


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "set MCP_TASKS_TEST_DATABASE_URL")
class TaskSchemaPostgresTests(unittest.TestCase):
    def test_restricted_runtime_and_version_fence(self):
        import psycopg
        from psycopg import sql
        from psycopg.conninfo import make_conninfo
        from psycopg.rows import dict_row
        from psycopg_pool import ConnectionPool

        url=os.environ["MCP_TASKS_TEST_DATABASE_URL"]
        name="mcp_parity_"+uuid.uuid4().hex
        identifier=sql.Identifier(name)
        with psycopg.connect(url,autocommit=True) as admin:
            admin.execute(sql.SQL("CREATE SCHEMA {}").format(identifier))
            admin.execute(sql.SQL("CREATE ROLE {} LOGIN PASSWORD 'mcp-parity-test-only'").format(identifier))
            try:
                options="-c search_path="+name+",pg_catalog"
                with ConnectionPool(make_conninfo(url,options=options),min_size=1,max_size=1,kwargs={"row_factory":dict_row}) as migration:
                    with self.assertRaises(Exception):check_task_schema(migration)
                    migrate_task_schema(migration)
                    with migration.connection() as connection:
                        connection.execute("INSERT INTO gregale_mcp_tasks (namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,status,expires_at) VALUES ('parity',%s,%s,'migration_probe','1',%s,'completed',clock_timestamp()+interval '1 hour')",(uuid.uuid4(),b"x",b"x"))
                    migrate_task_schema(migration)
                    with migration.connection() as connection:
                        self.assertEqual(connection.execute("SELECT count(*) AS count FROM gregale_mcp_tasks").fetchone()["count"],1)
                    admin.execute(sql.SQL("GRANT USAGE ON SCHEMA {} TO {}").format(identifier,identifier))
                    for table in ["gregale_mcp_tasks","gregale_mcp_task_worker_handlers"]:
                        admin.execute(sql.SQL("GRANT SELECT,INSERT,UPDATE,DELETE ON {}.{} TO {}").format(identifier,sql.Identifier(table),identifier))
                    admin.execute(sql.SQL("GRANT SELECT ON {}.gregale_mcp_task_starter_schema TO {}").format(identifier,identifier))
                    runtime_url=make_conninfo(url,user=name,password="mcp-parity-test-only",options=options)
                    with patch.dict(os.environ,{"DATABASE_URL":runtime_url,"MCP_TASK_OWNER_KEY":"x"*32,"MCP_TASK_NAMESPACE":"parity","MCP_TASK_MIGRATION_DATABASE_URL":"invalid-and-never-used"}):
                        runtime=DurableTaskRuntime({"tasks":{"enabled":True}},role="web")
                        try:
                            with runtime.pool.connection() as connection:
                                self.assertFalse(connection.execute("SELECT has_schema_privilege(current_user,current_schema(),'CREATE') AS permitted").fetchone()["permitted"])
                            with self.assertRaises(psycopg.errors.InsufficientPrivilege):
                                with runtime.pool.connection() as connection:connection.execute("ALTER TABLE gregale_mcp_tasks ADD COLUMN forbidden integer")
                            check_task_schema(runtime.pool)
                            with migration.connection() as connection:connection.execute("UPDATE gregale_mcp_task_starter_schema SET version=2")
                            with self.assertRaises(ValueError):migrate_task_schema(migration)
                            with self.assertRaises(ValueError):check_task_schema(runtime.pool)
                            with migration.connection() as connection:
                                connection.execute("UPDATE gregale_mcp_task_starter_schema SET version=1")
                                connection.execute("ALTER TABLE gregale_mcp_tasks DISABLE TRIGGER gregale_mcp_tasks_notify")
                            with self.assertRaises(ValueError):check_task_schema(runtime.pool)
                        finally:runtime.close()
            finally:
                admin.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(identifier))
                admin.execute(sql.SQL("DROP ROLE {}").format(identifier))
