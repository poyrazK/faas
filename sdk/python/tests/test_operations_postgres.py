"""Shared request contract and real PostgreSQL receipt/transaction acceptance."""

import asyncio
import base64
import json
import os
import unittest
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from pathlib import Path
from uuid import uuid4

from faas_sdk import (
    OperationConflictError,
    OperationRequest,
    awith_customer_operation_transaction,
    awith_operation_transaction,
    customer_operation_request_digest,
    customer_operation_request_from_headers,
    customer_operation_receipt_schema,
    operation_receipt_schema,
    operation_request_digest,
    operation_request_from_headers,
    with_customer_operation_transaction,
    with_operation_transaction,
)

FIXTURE = json.loads((Path(__file__).resolve().parents[2] / "operation-tests/request-fixture.json").read_text())
REQUEST = operation_request_from_headers(
    FIXTURE["headers"], FIXTURE["method"], FIXTURE["path"], base64.b64decode(FIXTURE["body_base64"])
)

CUSTOMER_FIXTURE = json.loads(
    (Path(__file__).resolve().parents[2] / "operation-tests/customer-request-fixture.json").read_text()
)
CUSTOMER_REQUEST = customer_operation_request_from_headers(
    CUSTOMER_FIXTURE["headers"],
    CUSTOMER_FIXTURE["method"],
    CUSTOMER_FIXTURE["path"],
    base64.b64decode(CUSTOMER_FIXTURE["body_base64"]),
)


class OperationContractTest(unittest.TestCase):
    def test_request_contract(self):
        self.assertEqual(operation_request_digest(REQUEST).hex(), FIXTURE["digest"])
        self.assertEqual(operation_request_digest(replace(REQUEST, generation=2)), operation_request_digest(REQUEST))
        for change in [
            {"x-gregale-operation-result-version": "2"},
            {"x-gregale-operation-generation": "9223372036854775808"},
            {"x-gregale-operation-generation": "01"},
            {"x-gregale-operation-id": [REQUEST.operation_id, REQUEST.operation_id]},
            {"x-faas-app-id": "00000000-0000-0000-0000-000000000000"},
        ]:
            with self.assertRaises(ValueError):
                operation_request_from_headers(
                    {**FIXTURE["headers"], **change}, REQUEST.method, REQUEST.path, REQUEST.body
                )
        with self.assertRaises(ValueError):
            operation_request_from_headers({}, "POST", "/", b"")
        with self.assertRaises(ValueError):
            operation_request_digest(
                OperationRequest(REQUEST.operation_id, REQUEST.account_id, REQUEST.app_id, 1, "POST", "/", b"")
            )

    def test_customer_receipt_negotiation(self):
        self.assertEqual(customer_operation_request_digest(CUSTOMER_REQUEST).hex(), CUSTOMER_FIXTURE["digest"])
        for change in [
            {"x-gregale-customer-operation-receipt-version": "2"},
            {"x-gregale-customer-operation-receipt-binding": "bad"},
            {"x-gregale-operation-capability": "bad"},
            {"x-gregale-operation-attempt": "01"},
            {"x-gregale-operation-attempt": "2147483648"},
            {"x-faas-platform-tenant-id": ""},
            {"x-faas-invocation-id": ""},
            {"x-gregale-operation-execution-kind": "workflow"},
            {"x-gregale-operation-result-version": "1"},
            {"x-gregale-customer-operation-id": [CUSTOMER_FIXTURE["headers"]["x-gregale-customer-operation-id"]] * 2},
        ]:
            with self.assertRaises(ValueError):
                customer_operation_request_from_headers(
                    {**CUSTOMER_FIXTURE["headers"], **change},
                    CUSTOMER_FIXTURE["method"],
                    CUSTOMER_FIXTURE["path"],
                    CUSTOMER_REQUEST._request.body,
                )
        with self.assertRaises(ValueError):
            with_operation_transaction(None, CUSTOMER_REQUEST._request, lambda _: {})


@unittest.skipUnless(os.environ.get("DATABASE_URL"), "DATABASE_URL required")
class OperationPostgresTest(unittest.TestCase):
    def setUp(self):
        import psycopg
        from psycopg import sql

        self.psycopg = psycopg
        self.sql = sql
        self.admin = psycopg.connect(os.environ["DATABASE_URL"], autocommit=True)
        self.database = "operation_python_" + uuid4().hex
        self.admin.execute(
            sql.SQL("CREATE DATABASE {} TEMPLATE template0 ENCODING 'UTF8'").format(sql.Identifier(self.database))
        )
        self.conn = self.connect()
        self.conn.execute(operation_receipt_schema)
        self.conn.execute(operation_receipt_schema)
        self.conn.execute(customer_operation_receipt_schema)
        self.conn.execute(customer_operation_receipt_schema)
        self.conn.execute(
            "CREATE SCHEMA business; CREATE TABLE business.counter(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business.counter VALUES(1,0); CREATE TABLE business.gregale_operation_inbox(LIKE public.gregale_operation_inbox INCLUDING ALL)"
        )
        self.conn.execute("SET search_path=business,public")

    def connect(self):
        return self.psycopg.connect(os.environ["DATABASE_URL"], dbname=self.database, autocommit=True)

    def tearDown(self):
        self.conn.close()
        try:
            self.admin.execute(self.sql.SQL("DROP DATABASE {} WITH (FORCE)").format(self.sql.Identifier(self.database)))
        finally:
            self.admin.close()

    def counts(self):
        return self.conn.execute(
            "SELECT (SELECT total FROM business.counter WHERE id=1),(SELECT count(*) FROM public.gregale_operation_inbox)"
        ).fetchone()

    def customer_counts(self):
        return self.conn.execute(
            "SELECT (SELECT total FROM business.counter WHERE id=1),(SELECT count(*) FROM public.gregale_customer_operation_inbox)"
        ).fetchone()

    def outcome(self):
        return {
            "result": {"value": 9007199254740993, "label": "π <>&"},
            "effects": [
                {
                    "name": "notify",
                    "webhook_id": "cccbbbaa-3333-4333-8333-cccccccccccc",
                    "type": "order.fulfilled",
                    "payload": {"order_id": 123},
                }
            ],
        }

    def test_rollback_concurrent_replay_scope_and_validation(self):
        def callback(cursor):
            cursor.execute("UPDATE business.counter SET total=total+1 WHERE id=1")
            return self.outcome()

        def abort(cursor):
            callback(cursor)
            raise RuntimeError("abort")

        with self.assertRaisesRegex(RuntimeError, "abort"):
            with_operation_transaction(self.conn, REQUEST, abort)
        self.assertEqual(self.counts(), (0, 0))
        for outcome in [
            {"result": float("nan")},
            {"result": "x" * 1048576},
            {"result": {}, "effects": self.outcome()["effects"] * 2},
            {"result": {}, "effects": [{**self.outcome()["effects"][0], "payload": "x" * 65536}]},
        ]:

            def invalid(cursor):
                callback(cursor)
                return outcome

            with self.assertRaises(ValueError):
                with_operation_transaction(self.conn, REQUEST, invalid)
            self.assertEqual(self.counts(), (0, 0))

        def attempt(_):
            with self.connect() as conn:
                return with_operation_transaction(conn, REQUEST, callback)

        with ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(attempt, range(8)))
        self.assertEqual(sum(not result.replayed for result in results), 1)
        self.assertTrue(all(result.body == results[0].body for result in results))
        self.assertEqual(self.counts(), (1, 1))

        def must_not_run(_):
            raise AssertionError("committed callback reran")

        later = with_operation_transaction(self.conn, replace(REQUEST, generation=2), must_not_run)
        self.assertTrue(later.replayed)
        self.assertEqual(later.body, results[0].body)
        for change in [
            {"body": b"{}"},
            {"path": "/other"},
            {"method": "PUT"},
            {"platform_tenant_id": ""},
            {"platform_tenant_id": str(uuid4())},
            {"account_id": str(uuid4())},
            {"app_id": str(uuid4())},
        ]:
            with self.assertRaises(OperationConflictError):
                with_operation_transaction(self.conn, replace(REQUEST, **change), callback)
        self.assertEqual(self.counts(), (1, 1))
        self.assertEqual(self.conn.execute("SELECT count(*) FROM business.gregale_operation_inbox").fetchone(), (0,))
        with self.conn.transaction(), self.assertRaises(ValueError):
            with_operation_transaction(self.conn, REQUEST, callback)
        with self.connect() as conn:
            conn.autocommit = False
            with self.assertRaises(ValueError):
                with_operation_transaction(conn, REQUEST, callback)

    def test_async_transaction_replay_and_rollback(self):
        async def run():
            connection = await self.psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], dbname=self.database, autocommit=True
            )
            async with connection:

                async def callback(cursor):
                    await cursor.execute("UPDATE business.counter SET total=total+1 WHERE id=1")
                    return self.outcome()

                async def abort(cursor):
                    await callback(cursor)
                    raise RuntimeError("abort")

                with self.assertRaisesRegex(RuntimeError, "abort"):
                    await awith_operation_transaction(connection, REQUEST, abort)
                first = await awith_operation_transaction(connection, REQUEST, callback)

                async def must_not_run(_):
                    raise AssertionError("async committed callback reran")

                later = await awith_operation_transaction(connection, replace(REQUEST, generation=2), must_not_run)
                self.assertFalse(first.replayed)
                self.assertTrue(later.replayed)
                self.assertEqual(first.body, later.body)

        asyncio.run(run())
        self.assertEqual(self.counts(), (1, 1))

    def test_connection_row_factory_is_preserved_for_business_queries(self):
        from psycopg.rows import dict_row

        self.conn.row_factory = dict_row

        def callback(cursor):
            cursor.execute("UPDATE business.counter SET total=total+1 WHERE id=1 RETURNING total")
            self.assertEqual(cursor.fetchone()["total"], 1)
            return self.outcome()

        first = with_operation_transaction(self.conn, REQUEST, callback)
        replay = with_operation_transaction(self.conn, replace(REQUEST, generation=2), callback)
        self.assertFalse(first.replayed)
        self.assertTrue(replay.replayed)
        self.assertEqual(first.body, replay.body)

    def test_customer_transaction_concurrency_replay_and_binding(self):
        def callback(cursor):
            cursor.execute("UPDATE business.counter SET total=total+1 WHERE id=1")
            return {"file": "ready.csv", "value": 9007199254740993, "label": "π <>&"}

        def abort(cursor):
            callback(cursor)
            raise RuntimeError("abort")

        with self.assertRaisesRegex(RuntimeError, "abort"):
            with_customer_operation_transaction(self.conn, CUSTOMER_REQUEST, abort)
        self.assertEqual(self.customer_counts(), (0, 0))

        def attempt(_):
            with self.connect() as connection:
                return with_customer_operation_transaction(connection, CUSTOMER_REQUEST, callback)

        with ThreadPoolExecutor(max_workers=8) as executor:
            results = list(executor.map(attempt, range(8)))
        self.assertEqual(sum(not result.replayed for result in results), 1)
        self.assertTrue(all(result.body == results[0].body for result in results))
        self.assertEqual(json.loads(results[0].body)["value"], 9007199254740993)
        self.assertNotIn("gregale_operation_result", json.loads(results[0].body))
        for change in [
            {"x-gregale-customer-operation-receipt-binding": "d" * 64},
            {"x-faas-platform-tenant-id": str(uuid4())},
            {"x-faas-app-id": str(uuid4())},
            {"x-faas-tenant-id": str(uuid4())},
        ]:
            changed = customer_operation_request_from_headers(
                {**CUSTOMER_FIXTURE["headers"], **change},
                CUSTOMER_FIXTURE["method"],
                CUSTOMER_FIXTURE["path"],
                CUSTOMER_REQUEST._request.body,
            )
            with self.assertRaises(OperationConflictError):
                with_customer_operation_transaction(self.conn, changed, callback)
        changed = customer_operation_request_from_headers(
            CUSTOMER_FIXTURE["headers"], CUSTOMER_FIXTURE["method"], CUSTOMER_FIXTURE["path"], b"{}"
        )
        with self.assertRaises(OperationConflictError):
            with_customer_operation_transaction(self.conn, changed, callback)
        managed = operation_request_from_headers(
            {
                **FIXTURE["headers"],
                "x-gregale-operation-id": CUSTOMER_FIXTURE["headers"]["x-gregale-customer-operation-id"],
            },
            CUSTOMER_FIXTURE["method"],
            CUSTOMER_FIXTURE["path"],
            CUSTOMER_REQUEST._request.body,
        )
        managed_result = with_operation_transaction(self.conn, managed, lambda _: {"result": {"scope": "managed"}})
        self.assertFalse(managed_result.replayed)
        self.assertIn("gregale_operation_result", json.loads(managed_result.body))
        self.assertEqual(self.customer_counts(), (1, 1))
        self.assertEqual(self.counts(), (1, 1))

    def test_customer_async_rollback_and_replay(self):
        async def run():
            async with await self.psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], dbname=self.database, autocommit=True
            ) as connection:

                async def callback(cursor):
                    await cursor.execute("UPDATE business.counter SET total=total+1 WHERE id=1")
                    return {"file": "ready.csv"}

                async def abort(cursor):
                    await callback(cursor)
                    raise RuntimeError("abort")

                with self.assertRaisesRegex(RuntimeError, "abort"):
                    await awith_customer_operation_transaction(connection, CUSTOMER_REQUEST, abort)
                self.assertEqual(self.customer_counts(), (0, 0))
                saved = await awith_customer_operation_transaction(connection, CUSTOMER_REQUEST, callback)
                headers = {
                    **CUSTOMER_FIXTURE["headers"],
                    "x-gregale-operation-attempt": "2",
                    "x-faas-invocation-id": str(uuid4()),
                    "x-gregale-operation-capability": "c" * 64,
                }
                later = customer_operation_request_from_headers(
                    headers, CUSTOMER_FIXTURE["method"], CUSTOMER_FIXTURE["path"], CUSTOMER_REQUEST._request.body
                )

                async def must_not_run(_):
                    raise AssertionError("committed customer callback reran")

                recovered = await awith_customer_operation_transaction(connection, later, must_not_run)
                self.assertFalse(saved.replayed)
                self.assertTrue(recovered.replayed)
                self.assertEqual(saved.body, recovered.body)
                self.assertEqual(json.loads(recovered.body), {"file": "ready.csv"})

        asyncio.run(run())
        self.assertEqual(self.customer_counts(), (1, 1))


if __name__ == "__main__":
    unittest.main()
