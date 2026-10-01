"""Real customer-transaction acceptance; no fake API or outbox relay."""
import os
from pathlib import Path
import unittest
from uuid import uuid4

import psycopg
from psycopg import sql
from faas_sdk import insert_commit_event


@unittest.skipUnless(os.environ.get("DATABASE_URL"), "DATABASE_URL required")
class CommitPostgresTest(unittest.TestCase):
    def test_business_transaction_and_public_schema(self):
        dsn = os.environ["DATABASE_URL"]
        database = "commit_sdk_python_" + uuid4().hex
        admin = psycopg.connect(dsn, autocommit=True, connect_timeout=5)
        writer = observer = None
        created = False
        try:
            admin.execute(sql.SQL("CREATE DATABASE {} TEMPLATE template0").format(sql.Identifier(database)))
            created = True
            writer = psycopg.connect(dsn, dbname=database, connect_timeout=5)
            observer = psycopg.connect(dsn, dbname=database, autocommit=True, connect_timeout=5)
            ddl = (Path(__file__).resolve().parents[3] / "pkg/commit/schema.sql").read_text()
            writer.execute(ddl)
            writer.execute("CREATE SCHEMA business; CREATE TABLE business.orders(id integer PRIMARY KEY); CREATE TABLE business.gregale_outbox(LIKE public.gregale_outbox INCLUDING ALL)")
            writer.execute("SET search_path=business,public")
            writer.commit()

            def counts():
                return observer.execute("SELECT (SELECT count(*) FROM business.orders),(SELECT count(*) FROM public.gregale_outbox)").fetchone()

            writer.execute("INSERT INTO orders VALUES(1)")
            with writer.cursor() as cursor:
                identity = insert_commit_event(cursor, "order.created", {"order_id": 1})
            self.assertEqual(counts(), (0, 0))
            writer.commit()
            self.assertEqual(counts(), (1, 1))
            self.assertEqual(observer.execute("SELECT event_id::text,event_type,payload FROM public.gregale_outbox").fetchone(), (identity, "order.created", {"order_id": 1}))
            writer.execute("INSERT INTO orders VALUES(2)")
            with writer.cursor() as cursor:
                insert_commit_event(cursor, "order.created", {"order_id": 2})
            writer.rollback()
            self.assertEqual(counts(), (1, 1))
            writer.execute("INSERT INTO orders VALUES(3)")
            with writer.cursor() as cursor, self.assertRaises(psycopg.errors.UniqueViolation):
                insert_commit_event(cursor, "order.changed", {"order_id": 3}, event_id=identity)
            writer.rollback()
            self.assertEqual(counts(), (1, 1))
            writer.execute("INSERT INTO orders VALUES(4)")
            with writer.cursor() as cursor, self.assertRaises(ValueError):
                insert_commit_event(cursor, "order.created", float("nan"))
            writer.rollback()
            self.assertEqual(counts(), (1, 1))
            self.assertEqual(observer.execute("SELECT count(*) FROM business.gregale_outbox").fetchone(), (0,))
        finally:
            if writer is not None:
                writer.close()
            if observer is not None:
                observer.close()
            try:
                if created:
                    admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(database)))
            finally:
                admin.close()


if __name__ == "__main__":
    unittest.main()
