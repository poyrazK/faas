"""ADR-20261016: real transaction capture, replay, and failure rollback."""

import asyncio
import base64
import json
import os
import unittest
from dataclasses import replace
from pathlib import Path
from uuid import uuid4

from faas_sdk import (
    DataRepairCaptureError,
    OperationCommitUnknownError,
    acheck_data_repair_capture,
    awith_data_repair_capture,
    awith_operation_transaction,
    awith_repairable_customer_operation_transaction,
    awith_repairable_operation_transaction,
    check_data_repair_capture,
    customer_operation_request_from_headers,
    operation_receipt_schema,
    operation_request_from_headers,
    with_customer_operation_transaction,
    with_data_repair_capture,
    with_operation_transaction,
    with_repairable_customer_operation_transaction,
    with_repairable_operation_transaction,
)


class _CaptureConnection:
    """Real PostgreSQL connection with faults at seal/COMMIT boundaries."""

    def __init__(self, connection, *, seal_fault=False, lose_commit=False):
        self.connection, self.seal_fault, self.lose_commit = connection, seal_fault, lose_commit

    def __getattr__(self, name):
        return getattr(self.connection, name)

    def cursor(self, *args, **kwargs):
        return _CaptureCursor(self, self.connection.cursor(*args, **kwargs))

    def transaction(self, *args, **kwargs):
        return _CaptureTransaction(self, self.connection.transaction(*args, **kwargs))


class _CaptureCursor:
    def __init__(self, connection, cursor):
        self.connection, self.cursor = connection, cursor

    def __getattr__(self, name):
        return getattr(self.cursor, name)

    def __enter__(self):
        self.cursor.__enter__()
        return self

    def __exit__(self, *args):
        return self.cursor.__exit__(*args)

    async def __aenter__(self):
        await self.cursor.__aenter__()
        return self

    async def __aexit__(self, *args):
        return await self.cursor.__aexit__(*args)

    def execute(self, statement, parameters=None):
        if (
            self.connection.seal_fault
            and statement.startswith("SELECT gregale_repair.capture_transaction")
            and parameters[7] == "seal"
        ):
            parameters = (*parameters[:6], bytes(32), parameters[7])
        return self.cursor.execute(statement, parameters)


class _CaptureTransaction:
    def __init__(self, connection, transaction):
        self.connection, self.transaction = connection, transaction

    def __enter__(self):
        return self.transaction.__enter__()

    def __exit__(self, kind, value, traceback):
        result = self.transaction.__exit__(kind, value, traceback)
        self.lost(kind)
        return result

    async def __aenter__(self):
        return await self.transaction.__aenter__()

    async def __aexit__(self, kind, value, traceback):
        result = await self.transaction.__aexit__(kind, value, traceback)
        self.lost(kind)
        return result

    def lost(self, kind):
        if kind is None and self.connection.lose_commit:
            self.connection.lose_commit = False
            raise OSError("lost commit response")


@unittest.skipUnless(
    os.environ.get("DATABASE_URL") and os.environ.get("GREGALE_DATA_REPAIR_SCHEMA"),
    "explicit disposable PostgreSQL and generated repair schema required",
)
class DataRepairPostgresTest(unittest.TestCase):
    def setUp(self):
        import psycopg
        from psycopg import sql

        self.psycopg, self.sql = psycopg, sql
        self.admin = psycopg.connect(os.environ["DATABASE_URL"], autocommit=True)
        self.database = "repair_python_" + uuid4().hex
        self.admin.execute(sql.SQL("CREATE DATABASE {} TEMPLATE template0").format(sql.Identifier(self.database)))
        self.connection = psycopg.connect(os.environ["DATABASE_URL"], dbname=self.database, autocommit=True)
        self.connection.execute(Path(os.environ["GREGALE_DATA_REPAIR_SCHEMA"]).read_text())
        self.connection.execute(operation_receipt_schema)
        self.connection.execute(
            "CREATE TABLE public.gregale_customer_operation_inbox(LIKE public.gregale_operation_inbox INCLUDING ALL);ALTER TABLE public.gregale_customer_operation_inbox ALTER COLUMN platform_tenant_id SET NOT NULL"
        )
        self.connection.execute(
            "CREATE TABLE products(id bigint PRIMARY KEY,category text);SELECT gregale_repair.enroll('products',ARRAY['category']);INSERT INTO products(id,category) VALUES(1,'old'),(2,'old')"
        )
        fixture = json.loads((Path(__file__).resolve().parents[2] / "operation-tests/request-fixture.json").read_text())
        self.request = operation_request_from_headers(
            fixture["headers"], fixture["method"], fixture["path"], base64.b64decode(fixture["body_base64"])
        )

    def tearDown(self):
        self.doCleanups()
        self.connection.close()
        self.admin.execute(self.sql.SQL("DROP DATABASE {} WITH (FORCE)").format(self.sql.Identifier(self.database)))
        self.admin.close()

    def test_capture_replay_and_rollback(self):
        def edit(cursor):
            cursor.execute("UPDATE products SET category='new'")
            return {"result": {"changed": 2}}

        def captured(cursor):
            return with_data_repair_capture(
                cursor, self.request, edit, table="products", environment="production", release="python-v1"
            )

        first = with_operation_transaction(self.connection, self.request, captured)
        replay = with_operation_transaction(
            self.connection, replace(self.request, generation=2), lambda _: self.fail("committed callback reran")
        )
        self.assertFalse(first.replayed)
        self.assertTrue(replay.replayed)
        self.assertEqual(first.body, replay.body)
        failed = replace(self.request, operation_id=str(uuid4()))

        def fail_after_write(cursor):
            cursor.execute("UPDATE products SET category='failed'")
            raise ValueError("validation failed")

        with self.assertRaisesRegex(ValueError, "validation failed"):
            with_operation_transaction(
                self.connection,
                failed,
                lambda cursor: with_data_repair_capture(
                    cursor, failed, fail_after_write, table="products", environment="production", release="python-v1"
                ),
            )
        self.assertEqual(self.connection.execute("SELECT count(*) FROM gregale_repair.operations").fetchone()[0], 1)
        self.assertEqual(
            self.connection.execute(
                "SELECT count(*) FROM products WHERE category='new' AND gregale_row_version=2"
            ).fetchone()[0],
            2,
        )

    def test_async_capture(self):
        async def run():
            conn = await self.psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], dbname=self.database, autocommit=True
            )
            try:

                async def edit(cursor):
                    await cursor.execute("UPDATE products SET category='new'")
                    return {"result": {"changed": 2}}

                async def captured(cursor):
                    return await awith_data_repair_capture(
                        cursor,
                        self.request,
                        edit,
                        table="products",
                        environment="production",
                        release="python-async-v1",
                    )

                result = await awith_operation_transaction(conn, self.request, captured)
                self.assertFalse(result.replayed)
            finally:
                await conn.close()

        asyncio.run(run())
        self.assertEqual(self.connection.execute("SELECT count(*) FROM gregale_repair.changes").fetchone()[0], 2)

    def test_restricted_writer_readiness(self):
        role = "writer_python_" + uuid4().hex
        name = self.sql.Identifier(role)
        self.connection.execute(self.sql.SQL("CREATE ROLE {} NOLOGIN").format(name))
        writer = self.psycopg.connect(os.environ["DATABASE_URL"], dbname=self.database, autocommit=True)
        try:
            grants = (Path(__file__).resolve().parents[2] / "data-repair-tests/writer-grants.sql").read_text()
            self.connection.execute(grants.replace("__WRITER_ROLE__", name.as_string(self.connection)))
            writer.execute(self.sql.SQL("SET ROLE {}").format(name))
            options = dict(
                account_id=self.request.account_id,
                app_id=self.request.app_id,
                platform_tenant_id=self.request.platform_tenant_id,
                environment="production",
                table="public.products",
                release="python-writer-v1",
            )

            def facts():
                return self.connection.execute(
                    "SELECT (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p),"
                    "(SELECT count(*) FROM gregale_repair.operations),(SELECT count(*) FROM gregale_repair.changes),"
                    "(SELECT count(*) FROM gregale_repair.plans),(SELECT count(*) FROM public.gregale_operation_inbox)"
                ).fetchone()

            def check(status, code=None, **overrides):
                report = check_data_repair_capture(writer, **{**options, **overrides})
                self.assertEqual(report["profile"], "capture")
                self.assertEqual(report["status"], status, report)
                self.assertEqual(len(report["findings"]), 10)
                if code:
                    self.assertIn(code, [f["code"] for f in report["findings"]])
                self.assertEqual(int(writer.info.transaction_status), 0)
                return report

            before = facts()
            check("ready")
            check("ready", receipt_protocol="customer")
            self.assertEqual(facts(), before)

            def edit(cursor):
                cursor.execute("UPDATE products SET category='new'")
                return {"result": {"changed": 2}}

            result = with_operation_transaction(
                writer,
                self.request,
                lambda cursor: with_data_repair_capture(
                    cursor,
                    self.request,
                    edit,
                    table=options["table"],
                    environment=options["environment"],
                    release=options["release"],
                ),
            )
            self.assertFalse(result.replayed)
            captured = facts()
            self.connection.execute(
                self.sql.SQL("REVOKE EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) FROM {}").format(name)
            )
            report = check("blocked", "writer_permissions_missing")
            self.assertIn(
                "gregale_repair.finish_operation: EXECUTE",
                next(f["objects"] for f in report["findings"] if f["id"] == "permissions"),
            )
            self.connection.execute(
                self.sql.SQL(
                    "GRANT EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) TO {};ALTER TABLE products DISABLE TRIGGER gregale_repair_capture"
                ).format(name)
            )
            check("blocked", "triggers_invalid")
            self.connection.execute("ALTER TABLE products ENABLE TRIGGER gregale_repair_capture")
            writer.execute("RESET ROLE;SET session_replication_role='replica'")
            writer.execute(self.sql.SQL("SET ROLE {}").format(name))
            check("blocked", "capture_session_invalid")
            writer.execute("RESET ROLE;SET session_replication_role='origin'")
            writer.execute(self.sql.SQL("SET ROLE {}").format(name))
            check("ready")
            self.assertEqual(facts(), captured)
            writer.execute("BEGIN")
            with self.assertRaisesRegex(ValueError, "idle autocommit"):
                check_data_repair_capture(writer, **options)
            self.assertNotEqual(int(writer.info.transaction_status), 0, "readiness rolled back a caller transaction")
            writer.rollback()
        finally:
            writer.close()
            self.connection.execute(self.sql.SQL("DROP OWNED BY {};DROP ROLE {}").format(name, name))

    def test_async_restricted_writer_readiness(self):
        role = "writer_async_python_" + uuid4().hex
        name = self.sql.Identifier(role)
        self.connection.execute(self.sql.SQL("CREATE ROLE {} NOLOGIN").format(name))
        grants = (Path(__file__).resolve().parents[2] / "data-repair-tests/writer-grants.sql").read_text()
        self.connection.execute(grants.replace("__WRITER_ROLE__", name.as_string(self.connection)))

        async def run():
            conn = await self.psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], dbname=self.database, autocommit=True
            )
            try:
                await conn.execute(self.sql.SQL("SET ROLE {}").format(name))
                options = dict(
                    account_id=self.request.account_id,
                    app_id=self.request.app_id,
                    platform_tenant_id=self.request.platform_tenant_id,
                    environment="production",
                    table="public.products",
                    release="python-async-writer-v1",
                )
                report = await acheck_data_repair_capture(conn, **options)
                self.assertEqual(report["status"], "ready", report)
                self.assertEqual(int(conn.info.transaction_status), 0)

                async def edit(cursor):
                    await cursor.execute("UPDATE products SET category='new'")
                    return {"result": {"changed": 2}}

                result = await awith_operation_transaction(
                    conn,
                    self.request,
                    lambda cursor: awith_data_repair_capture(
                        cursor,
                        self.request,
                        edit,
                        table=options["table"],
                        environment=options["environment"],
                        release=options["release"],
                    ),
                )
                self.assertFalse(result.replayed)
                await conn.execute("SET default_transaction_read_only='on'")
                report = await acheck_data_repair_capture(conn, **options)
                self.assertEqual(report["status"], "blocked", report)
                self.assertIn("capture_session_invalid", [f["code"] for f in report["findings"]])
                self.assertEqual(int(conn.info.transaction_status), 0)
            finally:
                await conn.close()

        try:
            asyncio.run(run())
            self.assertEqual(
                self.connection.execute(
                    "SELECT count(*) FROM products WHERE category='new' AND gregale_row_version=2"
                ).fetchone()[0],
                2,
            )
            self.assertEqual(self.connection.execute("SELECT count(*) FROM gregale_repair.plans").fetchone()[0], 0)
        finally:
            self.connection.execute(self.sql.SQL("DROP OWNED BY {};DROP ROLE {}").format(name, name))

    def _repairable_writer(self):
        role = "repairable_python_" + uuid4().hex
        name = self.sql.Identifier(role)
        self.connection.execute(self.sql.SQL("CREATE ROLE {} NOLOGIN").format(name))

        def cleanup():
            self.connection.execute(self.sql.SQL("DROP OWNED BY {};DROP ROLE {}").format(name, name))

        self.addCleanup(cleanup)
        for file in ("writer-grants.sql", "repairable-writer-grants.sql"):
            grants = (Path(__file__).resolve().parents[2] / "data-repair-tests" / file).read_text()
            self.connection.execute(grants.replace("__WRITER_ROLE__", name.as_string(self.connection)))
        writer = self.psycopg.connect(os.environ["DATABASE_URL"], dbname=self.database, autocommit=True)
        self.addCleanup(writer.close)
        writer.execute(self.sql.SQL("SET ROLE {}").format(name))
        return writer, name

    def _repairable_input(self, customer, identity):
        managed = replace(self.request, operation_id=identity)
        if not customer:
            return managed
        return customer_operation_request_from_headers(
            {
                "X-Gregale-Customer-Operation-Id": identity,
                "X-Gregale-Customer-Operation-Receipt-Version": "1",
                "X-Gregale-Customer-Operation-Receipt-Binding": "b" * 64,
                "X-Gregale-Operation-Attempt": "1",
                "X-Gregale-Operation-Capability": "a" * 64,
                "X-Faas-Invocation-Id": str(uuid4()),
                "X-Faas-Tenant-Id": managed.account_id,
                "X-Faas-App-Id": managed.app_id,
                "X-Faas-Platform-Tenant-Id": managed.platform_tenant_id,
            },
            managed.method,
            managed.path,
            managed.body,
        )

    def _repairable_facts(self):
        return self.connection.execute(
            "SELECT (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p),"
            "(SELECT count(*) FROM gregale_repair.operations),(SELECT count(*) FROM gregale_repair.changes),"
            "(SELECT count(*) FROM gregale_repair.capture_receipts),(SELECT count(*) FROM public.gregale_operation_inbox),"
            "(SELECT count(*) FROM public.gregale_customer_operation_inbox)"
        ).fetchone()

    def test_repairable_transactions(self):
        for customer in (False, True):
            with self.subTest(customer=customer):
                writer, name = self._repairable_writer()
                self.connection.execute("UPDATE products SET category='old'")
                config = dict(table="public.products", environment="production", release="repairable-python-v1")

                def run(identity, handler, *, connection=writer, **overrides):
                    request = self._repairable_input(customer, identity)
                    if customer:
                        return with_repairable_customer_operation_transaction(
                            connection, request, handler, **{**config, **overrides}
                        )
                    return with_repairable_operation_transaction(
                        connection, request, lambda c: {"result": handler(c)}, **{**config, **overrides}
                    )

                def edit(cursor):
                    cursor.execute("UPDATE products SET category='new'")
                    return {"changed": 2}

                def never(_cursor):
                    self.fail("committed or unavailable callback reran")

                operation = str(uuid4())
                first = run(operation, edit)
                self.assertFalse(first.replayed)
                self.connection.execute("UPDATE products SET category='human' WHERE id=1")
                before = self._repairable_facts()
                replay = run(operation, never)
                self.assertTrue(replay.replayed)
                self.assertEqual(first.body, replay.body)
                self.assertEqual(self._repairable_facts(), before)
                scope = dict(
                    account_id=self.request.account_id,
                    app_id=self.request.app_id,
                    platform_tenant_id=self.request.platform_tenant_id,
                    environment=config["environment"],
                )
                plan = self.connection.execute(
                    "SELECT gregale_repair.create_plan(%s,%s::jsonb,%s)", (operation, json.dumps(scope), "a" * 64)
                ).fetchone()[0]
                self.assertEqual((plan["candidates"], plan["conflicts"]), (1, 1))
                for override in (dict(release="other"), dict(environment="staging"), dict(table="public.other")):
                    with self.assertRaises(DataRepairCaptureError) as caught:
                        run(operation, never, **override)
                    self.assertEqual(caught.exception.code, "repair_capture_mismatch")

                def failure(cursor):
                    edit(cursor)
                    raise ValueError("business validation failed")

                before = self._repairable_facts()
                with self.assertRaisesRegex(ValueError, "business validation failed"):
                    run(str(uuid4()), failure)
                self.assertEqual(self._repairable_facts(), before)
                with self.assertRaises(DataRepairCaptureError) as caught:
                    run(str(uuid4()), edit, connection=_CaptureConnection(writer, seal_fault=True))
                self.assertEqual(caught.exception.code, "repair_capture_mismatch")
                self.assertEqual(self._repairable_facts(), before)
                incomplete = str(uuid4())

                def finish_early(cursor):
                    result = edit(cursor)
                    cursor.execute("SELECT gregale_repair.finish_operation(%s)", (incomplete,))
                    return result

                with self.assertRaises(DataRepairCaptureError) as caught:
                    run(incomplete, finish_early)
                self.assertEqual(caught.exception.code, "repair_capture_incomplete")
                self.assertEqual(self._repairable_facts(), before)
                zero = str(uuid4())
                run(zero, lambda _: {"changed": 0})
                self.assertEqual(
                    self.connection.execute(
                        "SELECT count(*) FROM gregale_repair.changes WHERE operation_id=%s", (zero,)
                    ).fetchone()[0],
                    0,
                )
                legacy = str(uuid4())
                if customer:
                    with_customer_operation_transaction(
                        writer, self._repairable_input(True, legacy), lambda _: {"old": True}
                    )
                else:
                    with_operation_transaction(
                        writer, self._repairable_input(False, legacy), lambda _: {"result": {"old": True}}
                    )
                before = self._repairable_facts()
                with self.assertRaises(DataRepairCaptureError) as caught:
                    run(legacy, never)
                self.assertEqual(caught.exception.code, "repair_capture_missing")
                self.assertEqual(self._repairable_facts(), before)
                lost = str(uuid4())
                with self.assertRaises(OperationCommitUnknownError):
                    run(lost, edit, connection=_CaptureConnection(writer, lose_commit=True))
                before = self._repairable_facts()
                self.assertTrue(run(lost, never).replayed)
                self.assertEqual(self._repairable_facts(), before)
                self.connection.execute(
                    self.sql.SQL(
                        "REVOKE EXECUTE ON FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text) FROM {}"
                    ).format(name)
                )
                with self.assertRaises(DataRepairCaptureError) as caught:
                    run(str(uuid4()), never)
                self.assertEqual(caught.exception.code, "repair_capture_unavailable")
                self.assertEqual(self._repairable_facts(), before)
                self.connection.execute(
                    self.sql.SQL(
                        "GRANT EXECUTE ON FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text) TO {}"
                    ).format(name)
                )

    def test_async_repairable_transactions(self):
        async def run_protocol(customer):
            _writer, name = self._repairable_writer()
            self.connection.execute("UPDATE products SET category='old'")
            conn = await self.psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], dbname=self.database, autocommit=True
            )
            try:
                await conn.execute(self.sql.SQL("SET ROLE {}").format(name))
                config = dict(table="public.products", environment="production", release="repairable-python-async-v1")

                async def run(identity, handler, connection=conn):
                    request = self._repairable_input(customer, identity)
                    if customer:
                        return await awith_repairable_customer_operation_transaction(
                            connection, request, handler, **config
                        )

                    async def outcome(cursor):
                        return {"result": await handler(cursor)}

                    return await awith_repairable_operation_transaction(connection, request, outcome, **config)

                async def edit(cursor):
                    await cursor.execute("UPDATE products SET category='async-new'")
                    return {"changed": 2}

                async def never(_cursor):
                    self.fail("async callback reran")

                operation = str(uuid4())
                first = await run(operation, edit)
                self.assertEqual(
                    self.connection.execute(
                        "SELECT count(*) FROM gregale_repair.changes WHERE operation_id=%s", (operation,)
                    ).fetchone()[0],
                    2,
                )
                before = self._repairable_facts()
                replay = await run(operation, never)
                self.assertTrue(replay.replayed)
                self.assertEqual(first.body, replay.body)
                self.assertEqual(self._repairable_facts(), before)
                with self.assertRaises(DataRepairCaptureError) as caught:
                    await run(str(uuid4()), edit, _CaptureConnection(conn, seal_fault=True))
                self.assertEqual(caught.exception.code, "repair_capture_mismatch")
                self.assertEqual(self._repairable_facts(), before)
                lost = str(uuid4())
                with self.assertRaises(OperationCommitUnknownError):
                    await run(lost, edit, _CaptureConnection(conn, lose_commit=True))
                before = self._repairable_facts()
                self.assertTrue((await run(lost, never)).replayed)
                self.assertEqual(self._repairable_facts(), before)
                zero = str(uuid4())

                async def no_write(_cursor):
                    return {"changed": 0}

                await run(zero, no_write)
                self.assertEqual(
                    self.connection.execute(
                        "SELECT count(*) FROM gregale_repair.changes WHERE operation_id=%s", (zero,)
                    ).fetchone()[0],
                    0,
                )
            finally:
                await conn.close()

        for customer in (False, True):
            with self.subTest(customer=customer):
                asyncio.run(run_protocol(customer))
