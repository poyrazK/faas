"""Each SDK writes a receipt; all three replay its exact bytes in a later generation."""
import base64
import json
import os
import subprocess
import tempfile
from itertools import product
from pathlib import Path
from uuid import uuid4

import psycopg
from faas_sdk import (
    operation_receipt_schema,
    operation_request_from_headers,
    with_operation_transaction,
)
from psycopg import sql

ROOT = Path(__file__).resolve().parents[2]


def main():
    dsn = os.environ["DATABASE_URL"]
    database = "operation_cross_" + uuid4().hex
    with psycopg.connect(dsn, autocommit=True) as admin:
        admin.execute(sql.SQL("CREATE DATABASE {} TEMPLATE template0 ENCODING 'UTF8'").format(sql.Identifier(database)))
        try:
            from urllib.parse import urlsplit

            parts = urlsplit(dsn)
            cross_dsn = f"{parts.scheme}://{parts.netloc}/{database}" + ("?" + parts.query if parts.query else "")
            with psycopg.connect(cross_dsn, autocommit=True) as conn, tempfile.TemporaryDirectory(prefix="gregale-operation-cross-") as temp:
                conn.execute(operation_receipt_schema)
                conn.execute("CREATE SCHEMA business; CREATE TABLE business.counter(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business.counter VALUES(1,0)")
                binary = str(Path(temp) / "operation-interop")
                subprocess.run([os.environ.get("GO", "go"), "build", "-p", "1", "-ldflags=-s -w -linkmode=internal", "-o", binary, "./cmd/operation-interop"], cwd=ROOT / "sdk/commit-tests/go", check=True)
                request_file = Path(temp) / "request.json"
                fixture = json.loads((ROOT / "sdk/operation-tests/request-fixture.json").read_text())

                def invoke(language, mode):
                    if language == "python":
                        request = operation_request_from_headers(fixture["headers"], fixture["method"], fixture["path"], base64.b64decode(fixture["body_base64"]))

                        def callback(cursor):
                            if mode != "write":
                                raise AssertionError("cross SDK receipt callback reran")
                            cursor.execute("UPDATE business.counter SET total=total+1 WHERE id=1")
                            return {"result": {"value": 9007199254740993, "label": "π <>&"}, "effects": [{"name": "notify", "webhook_id": "cccbbbaa-3333-4333-8333-cccccccccccc", "type": "order.fulfilled", "payload": {"order_id": 123, "nested": [{"text": 'π \\" },] : [ {\nend'}, None, False]}}]}

                        result = with_operation_transaction(conn, request, callback)
                        return {"body": result.body.decode(), "replayed": result.replayed}
                    request_file.write_text(json.dumps(fixture))
                    command = [binary] if language == "go" else ["node", str(ROOT / "sdk/node/test/fixtures/operation-interop.mjs")]
                    env = {**os.environ, "OPERATION_CROSS_DATABASE_URL": cross_dsn, "OPERATION_REQUEST_FILE": str(request_file), "OPERATION_MODE": mode}
                    result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=20, check=True)
                    return json.loads(result.stdout)

                for index, (scope, writer) in enumerate(product(["customer", "account"], ["python", "node", "go"]), 1):
                    if scope == "account":
                        fixture["headers"].pop("x-faas-platform-tenant-id", None)
                    fixture["headers"]["x-gregale-operation-id"] = str(uuid4())
                    fixture["headers"]["x-gregale-operation-generation"] = "1"
                    saved = invoke(writer, "write")
                    if saved["replayed"] is not False:
                        raise AssertionError(f"{writer} failed to create its receipt")
                    fixture["headers"]["x-gregale-operation-generation"] = "2"
                    for reader in ["python", "node", "go"]:
                        recovered = invoke(reader, "replay")
                        if recovered != {"body": saved["body"], "replayed": True}:
                            raise AssertionError(f"{writer} -> {reader} changed response")
                    counts = conn.execute("SELECT (SELECT total FROM business.counter WHERE id=1),(SELECT count(*) FROM public.gregale_operation_inbox)").fetchone()
                    if counts != (index, index):
                        raise AssertionError(f"duplicate mutation: {counts}")
                print("All 18 cross-SDK receipt replays passed for customer/account scope, including precise numbers and Unicode.")
        finally:
            admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(database)))


if __name__ == "__main__":
    main()
