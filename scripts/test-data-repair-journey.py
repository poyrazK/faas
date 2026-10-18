#!/usr/bin/env python3
"""Run the real operator CLI → API → isolated rehearsal → approved repair flow.

Requires a disposable DATABASE_URL, psycopg, a built GREGALE_DATA_REPAIR_CLI,
PostgreSQL 16 pg_dump, and the pre-pulled pinned Docker image. Every database
and executor belongs to this run; production application data is never selected.
"""
from __future__ import annotations

import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path
from urllib.parse import parse_qsl, quote, urlencode, urlsplit, urlunsplit
from uuid import uuid4

import psycopg
from psycopg import sql


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    sys.path.insert(0, str(root / "sdk/python"))
    from faas_sdk import (
        operation_request_from_headers,
        with_repairable_operation_transaction,
    )

    cli = os.environ.get("GREGALE_DATA_REPAIR_CLI", str(root / "bin/gregale"))
    admin = psycopg.connect(os.environ["DATABASE_URL"], autocommit=True)
    database = "repair_cli_" + uuid4().hex
    admin.execute(sql.SQL("CREATE DATABASE {} TEMPLATE template0").format(sql.Identifier(database)))
    conn = None
    server = None
    writer_role = None
    try:
        source = urlsplit(os.environ["DATABASE_URL"])
        if source.scheme not in ("postgres", "postgresql"):
            raise RuntimeError("journey fixture requires a PostgreSQL URI")
        dsn = urlunsplit((source.scheme, source.netloc, "/" + database, source.query, source.fragment))
        conn = psycopg.connect(dsn, autocommit=True)
        env = {**os.environ, "GREGALE_REPAIR_DATABASE_URL": dsn,
               "GREGALE_REPAIR_TOKEN": secrets.token_urlsafe(48), "GREGALE_DATA_REPAIR_ENABLED": "true"}
        scope = {"account_id": str(uuid4()), "app_id": str(uuid4()), "environment": "production"}
        schema = subprocess.run([cli, "data-repair", "schema"], check=True, capture_output=True, text=True, env=env).stdout
        conn.execute(schema)
        conn.execute((root / "sdk/go/operation_schema.sql").read_text())
        conn.execute("CREATE TABLE public.gregale_customer_operation_inbox(LIKE public.gregale_operation_inbox INCLUDING ALL);ALTER TABLE public.gregale_customer_operation_inbox ALTER COLUMN platform_tenant_id SET NOT NULL")
        conn.execute("CREATE TABLE products(id bigint PRIMARY KEY,category text NOT NULL CHECK(category IN ('original','bad','human')))")
        conn.execute("CREATE TABLE repair_policy(id int PRIMARY KEY,enabled boolean NOT NULL)")
        conn.execute("INSERT INTO repair_policy VALUES(1,true)")
        conn.execute("INSERT INTO products SELECT generate_series(1,500),'original'")
        subprocess.run([cli, "data-repair", "enroll", "--table", "public.products", "--fields", "category"], check=True, capture_output=True, env=env)
        writer_role = "capture_writer_cli_" + uuid4().hex
        conn.execute(sql.SQL("CREATE ROLE {} NOLOGIN").format(sql.Identifier(writer_role)))
        for grants_file in ("writer-grants.sql", "repairable-writer-grants.sql"):
            grants = (root / "sdk/data-repair-tests" / grants_file).read_text()
            conn.execute(grants.replace("__WRITER_ROLE__", sql.Identifier(writer_role).as_string(conn)))
        writer_query = parse_qsl(source.query, keep_blank_values=True)
        writer_query.append(("options", "-c role=" + writer_role))
        writer_dsn = urlunsplit((source.scheme, source.netloc, "/" + database, urlencode(writer_query, quote_via=quote), source.fragment))
        env["GREGALE_CAPTURE_WRITER_URL"] = writer_dsn
        operation = str(uuid4())
        capture_request = operation_request_from_headers({
            "X-Gregale-Operation-Result-Version": "1", "X-Gregale-Operation-Id": operation,
            "X-Gregale-Operation-Generation": "1", "X-Faas-Tenant-Id": scope["account_id"], "X-Faas-App-Id": scope["app_id"],
        }, "POST", "/products/import", b'{"category":"bad"}')
        capture_config = dict(table="public.products", environment=scope["environment"], release="bad-import-v1")
        def import_products(cursor):
            cursor.execute("UPDATE products SET category='bad'")
            return {"result": {"imported": 500}}
        with psycopg.connect(writer_dsn, autocommit=True) as writer:
            assert writer.execute("SELECT current_user").fetchone()[0] == writer_role
            capture_result = with_repairable_operation_transaction(writer, capture_request, import_products, **capture_config)
            assert not capture_result.replayed
        conn.execute("UPDATE products SET category='human' WHERE id<=40")
        with tempfile.TemporaryDirectory(prefix="gregale-repair-journey-") as directory:
            scope_file = Path(directory) / "scope.json"
            scope_file.write_text(json.dumps(scope))
            checks_file = Path(directory) / "checks.json"
            checks_file.write_text(json.dumps({"version": 1, "checks": [
                {"name": "Restored product categories", "sql": "SELECT count(*) FILTER(WHERE category='original')=460 FROM public.products"},
                {"name": "Human edits preserved", "sql": "SELECT count(*) FILTER(WHERE category='human')=40 FROM public.products"},
                {"name": "Repair remains enabled", "sql": "SELECT enabled FROM public.repair_policy WHERE id=1 FOR SHARE"},
            ]}))
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                port = listener.getsockname()[1]
            base = f"http://127.0.0.1:{port}"
            server = subprocess.Popen([cli, "data-repair", "serve", "--scope-file", str(scope_file),
                "--actor", "acceptance-operator", "--listen", f"127.0.0.1:{port}",
                "--checks-policy-file", str(checks_file), "--pg-dump", os.environ.get("GREGALE_DATA_REPAIR_PG_DUMP", "pg_dump")],
                env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            deadline = time.monotonic() + 15
            while True:
                if server.poll() is not None:
                    raise RuntimeError("repair executor exited before readiness")
                try:
                    with urllib.request.urlopen(base, timeout=1) as response:
                        if response.status == 200:
                            break
                except OSError:
                    if time.monotonic() >= deadline:
                        raise RuntimeError("repair executor readiness timed out") from None
                    time.sleep(0.05)

            def doctor(expected: str) -> dict:
                result = subprocess.run([cli, "data-repair", "doctor", "--table", "public.products", "--url", base],
                    env=env, capture_output=True, text=True, timeout=20)
                assert result.returncode == (0 if expected == "ready" else 1), result.stderr
                report = json.loads(result.stdout)
                assert report["scope"] == scope and report["table"] == "public.products" and report["status"] == expected
                assert len(report["findings"]) == 9 and "SELECT count" not in result.stdout
                return report

            def writer_doctor(expected: str) -> dict:
                result = subprocess.run([cli, "data-repair", "doctor", "--profile", "capture", "--table", "public.products",
                    "--scope-file", str(scope_file), "--release", "bad-import-v1", "--database-env", "GREGALE_CAPTURE_WRITER_URL"],
                    env=env, capture_output=True, text=True, timeout=20)
                assert result.returncode == (0 if expected == "ready" else 1), result.stderr
                report = json.loads(result.stdout)
                assert report["profile"] == "capture" and report["scope"] == scope and report["status"] == expected
                assert report["receipt_protocol"] == "managed" and len(report["findings"]) == 10
                return report

            # Readiness inspects configuration even though the first business
            # check currently fails. It never evaluates those trusted queries.
            before_doctor = conn.execute("SELECT id,category,gregale_row_identity,gregale_row_version FROM products ORDER BY id").fetchall()
            assert doctor("ready")["policy"]["count"] == 3
            writer_doctor("ready")
            conn.execute("ALTER TABLE products DISABLE TRIGGER gregale_repair_capture")
            blocked = doctor("blocked")
            assert any(f["code"] == "triggers_invalid" for f in blocked["findings"])
            assert any(f["code"] == "triggers_invalid" for f in writer_doctor("blocked")["findings"])
            conn.execute("ALTER TABLE products ENABLE TRIGGER gregale_repair_capture")
            doctor("ready")
            conn.execute(sql.SQL("REVOKE EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) FROM {}").format(sql.Identifier(writer_role)))
            assert any(f["code"] == "writer_permissions_missing" for f in writer_doctor("blocked")["findings"])
            conn.execute(sql.SQL("GRANT EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) TO {}").format(sql.Identifier(writer_role)))
            writer_doctor("ready")
            assert conn.execute("SELECT id,category,gregale_row_identity,gregale_row_version FROM products ORDER BY id").fetchall() == before_doctor
            assert conn.execute("SELECT count(*) FROM gregale_repair.plans").fetchone()[0] == 0
            assert conn.execute("SELECT count(*) FROM public.gregale_operation_inbox").fetchone()[0] == 1
            assert conn.execute("SELECT count(*) FROM gregale_repair.capture_receipts").fetchone()[0] == 1
            assert conn.execute("SELECT count(*) FROM gregale_repair.changes").fetchone()[0] == 500
            print("repair doctor acceptance: healthy and disabled-trigger reports; no checks, plans or business writes")
            print("capture writer acceptance: restricted application role reported separately; missing capture grants and trigger bypass blocked; rows, versions, journals and receipts unchanged")

            def call(verb: str, identity: str, *args: str, succeeds: bool = True,
                     failure_code: str = "repair_check_failed") -> dict:
                result = subprocess.run([cli, "data-repair", verb, identity, "--url", base, *args],
                    env=env, capture_output=True, text=True, timeout=140)
                if not succeeds:
                    assert result.returncode != 0 and failure_code in result.stderr, result.stderr
                    return {}
                if result.returncode:
                    raise RuntimeError(f"repair {verb} failed: {result.stderr}")
                return json.loads(result.stdout)

            def validate(saved: dict, matches: bool) -> dict:
                result = subprocess.run([cli, "data-repair", "validate", saved["id"], "--digest", saved["digest"], "--url", base],
                    env=env, capture_output=True, text=True, timeout=140)
                assert result.returncode == (0 if matches else 1), result.stderr
                report = json.loads(result.stdout)
                assert report["plan_id"] == saved["id"] and report["plan_digest"] == saved["digest"]
                assert report["scope"] == scope and report["matches"] == matches
                return report

            def discover(*args: str) -> dict:
                result = subprocess.run([cli, "data-repair", "sources", "--url", base, *args],
                    env=env, capture_output=True, text=True, timeout=20)
                if result.returncode:
                    raise RuntimeError(f"repair discovery failed: {result.stderr}")
                return json.loads(result.stdout)

            def verify(saved: dict, available: bool = True) -> dict:
                result = subprocess.run([cli, "data-repair", "verify", saved["id"], "--digest", saved["digest"], "--url", base],
                    env=env, capture_output=True, text=True, timeout=20)
                assert result.returncode == (0 if available else 1), result.stderr
                report = json.loads(result.stdout)
                assert report["plan_id"] == saved["id"] and report["plan_digest"] == saved["digest"]
                assert report["scope"] == scope and report["available"] == available
                assert report["repair_operation_id"] == saved["repair_operation_id"] and report["restored"] == saved["candidates"]
                return report

            def history(source_id: str, *args: str) -> dict:
                result = subprocess.run([cli, "data-repair", "history", source_id, "--url", base, *args],
                    env=env, capture_output=True, text=True, timeout=20)
                if result.returncode:
                    raise RuntimeError(f"repair history failed: {result.stderr}")
                page = json.loads(result.stdout)
                assert page["source_operation_id"] == source_id and page["scope"] == scope
                assert '"category"' not in json.dumps(page) and '"before"' not in json.dumps(page)
                return page

            inventory = discover("--release", "bad-import-v1", "--table", "public.products")
            assert inventory["scope"] == scope and len(inventory["items"]) == 1
            found = inventory["items"][0]
            assert found["id"] == operation and found["change_rows"] == 500 and found["can_prepare"]
            assert '"category"' not in json.dumps(inventory)
            plan = call("plan", found["id"])
            assert len(plan["checks"]) == 3 and "SELECT" not in json.dumps(plan)
            assert (plan["candidates"], plan["conflicts"], plan["unavailable"]) == (460, 40, 0)
            conflicts = [item for item in plan["items"] if item["status"] == "conflict"]
            assert len(conflicts) == 40 and all(item["reason"] == "record_version_changed" for item in conflicts)
            assert all(item["observed_values"] == {"category": "human"} and item["observed_version"] == "3" for item in conflicts)
            assert all(item["observed_values_json"] == '{"category": "human"}' for item in conflicts)
            freshness = validate(plan, True)
            assert (freshness["checked"], freshness["unchanged"], freshness["excluded"]) == (460, 460, 40)
            assert call("get", plan["id"])["rehearsal"] is None
            conn.execute("UPDATE repair_policy SET enabled=false")
            call("rehearse", plan["id"], "--digest", plan["digest"], succeeds=False)
            failed = call("get", plan["id"])
            assert failed["rehearsed_at"] is None and failed["rehearsal"] is None
            assert failed["check_failure"]["stage"] == "rehearsal"
            assert [item["status"] for item in failed["check_failure"]["evidence"]["check_results"]["results"]] == ["passed", "passed", "failed"]
            conn.execute("UPDATE repair_policy SET enabled=true")
            plan = call("rehearse", plan["id"], "--digest", plan["digest"])
            assert plan["rehearsal"]["passed"] is True
            assert all(item["status"] == "passed" for item in plan["rehearsal"]["check_results"]["results"])
            assert "check_failure" not in plan
            assert conn.execute("SELECT count(*) FROM products WHERE category='bad'").fetchone()[0] == 460
            plan = call("approve", plan["id"], "--digest", plan["digest"], "--yes")
            assert plan["approved_by"] == "acceptance-operator"
            repair_id = str(uuid4())
            conn.execute("UPDATE repair_policy SET enabled=false")
            call("apply", plan["id"], "--digest", plan["digest"], "--repair-operation", repair_id, "--yes", succeeds=False)
            failed = call("get", plan["id"])
            assert failed["applied_at"] is None and failed["check_failure"]["stage"] == "apply"
            assert dict(conn.execute("SELECT category,count(*) FROM products GROUP BY category").fetchall()) == {"bad": 460, "human": 40}
            assert conn.execute("SELECT count(*) FROM gregale_repair.operations WHERE id=%s", (repair_id,)).fetchone()[0] == 0
            conn.execute("UPDATE repair_policy SET enabled=true")
            applied = call("apply", plan["id"], "--digest", plan["digest"], "--repair-operation", repair_id, "--yes")
            assert applied["repair_operation_id"] == repair_id and applied["applied_at"]
            assert all(item["status"] == "passed" for item in applied["apply_checks"]["results"])
            replay = call("apply", plan["id"], "--digest", plan["digest"], "--repair-operation", repair_id, "--yes")
            assert replay["repair_operation_id"] == repair_id
            counts = dict(conn.execute("SELECT category,count(*) FROM products GROUP BY category").fetchall())
            assert counts == {"original": 460, "human": 40}, counts
            assert conn.execute("SELECT count(*) FROM gregale_repair.changes WHERE operation_id=%s", (repair_id,)).fetchone()[0] == 460
            found = discover("--release", "bad-import-v1")["items"][0]
            assert found["applied_repairs"] == 1 and found["latest_repair"]["operation_id"] == repair_id
            print("repair CLI acceptance: discovered import; named checks rejected bad rehearsal and rolled back live failure; 460 restored, 40 preserved; conflict observations, approval, and idempotent receipt verified")

            # Only this disposable fixture is backdated. Production has no
            # force/short-retention switch and retains every receipt identity.
            with conn.transaction():
                conn.execute("ALTER TABLE gregale_repair.operations DISABLE TRIGGER gregale_repair_protect_operation")
                conn.execute("UPDATE gregale_repair.operations SET created_at=clock_timestamp()-interval '10 days'")
                conn.execute("ALTER TABLE gregale_repair.operations ENABLE TRIGGER gregale_repair_protect_operation")
                conn.execute("UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '10 days',expires_at=clock_timestamp()-interval '9 days',applied_at=clock_timestamp()-interval '8 days'")

            def retention(verb: str, *args: str, succeeds: bool = True) -> dict:
                result = subprocess.run([cli, "data-repair", "retention", verb, *args, "--scope-file", str(scope_file)],
                    env=env, capture_output=True, text=True, timeout=20)
                if not succeeds:
                    assert result.returncode != 0, "unconfirmed payload purge succeeded"
                    return {}
                if result.returncode:
                    raise RuntimeError(f"repair retention {verb} failed: {result.stderr}")
                return json.loads(result.stdout)

            batch = retention("plan")
            assert [(entry["kind"], entry["id"]) for entry in batch["entries"]] == [("plan", plan["id"])]
            retention("purge", batch["id"], "--digest", batch["digest"], "--actor", "retention-operator", succeeds=False)
            assert conn.execute("SELECT count(*) FROM gregale_repair.changes").fetchone()[0] == 960
            receipt = retention("purge", batch["id"], "--digest", batch["digest"], "--actor", "retention-operator", "--yes")
            assert receipt["applied_by"] == "retention-operator" and receipt["applied_role"]
            assert retention("get", batch["id"]) == receipt
            assert retention("purge", batch["id"], "--digest", batch["digest"], "--actor", "retry-operator", "--yes") == receipt
            compacted = call("get", plan["id"])
            assert compacted["payload_purged_at"] and compacted["items"] == []
            assert (compacted["candidates"], compacted["conflicts"]) == (460, 40)
            journals = retention("plan")
            assert {entry["id"] for entry in journals["entries"]} == {operation, repair_id}
            assert all(entry["kind"] == "operation" for entry in journals["entries"])
            retention("purge", journals["id"], "--digest", journals["digest"], "--actor", "retention-operator", "--yes")
            assert conn.execute("SELECT count(*) FROM gregale_repair.changes").fetchone()[0] == 0
            found = discover("--release", "bad-import-v1")["items"][0]
            assert found["payload_purged_at"] and found["change_rows"] is None and not found["can_prepare"]
            assert found["latest_repair"]["operation_id"] == repair_id
            replay = call("apply", plan["id"], "--digest", plan["digest"], "--repair-operation", repair_id, "--yes")
            assert replay["repair_operation_id"] == repair_id and replay["payload_purged_at"]
            assert all(item["status"] == "passed" for item in replay["apply_checks"]["results"])
            assert dict(conn.execute("SELECT category,count(*) FROM products GROUP BY category").fetchall()) == counts
            result = subprocess.run([cli, "data-repair", "plan", operation, "--url", base], env=env, capture_output=True, text=True, timeout=20)
            assert result.returncode != 0 and "410" in result.stderr
            assert retention("plan")["entries"] == []
            print("repair retention acceptance: reviewed plan and 960 journal rows compacted; 460/40 result and original receipt replay preserved")
            compacted_report = validate(compacted, False)
            assert set(compacted_report["issues"]) == {"plan_applied", "plan_expired", "payload_purged"}
            assert compacted_report["checked"] == 0 and compacted_report["rows"] == []

            # A separate recorded import exercises drift after approval. The
            # original approved candidate set must never silently shrink.
            later_operation = str(uuid4())
            with conn.transaction():
                conn.execute("SELECT gregale_repair.begin_operation(%s,%s::jsonb,'bad-import-v2','public.products')", (later_operation, json.dumps(scope)))
                conn.execute("UPDATE products SET category='bad'")
                conn.execute("SELECT gregale_repair.finish_operation(%s)", (later_operation,))
            conn.execute("UPDATE products SET category='human' WHERE id<=40")
            later = call("plan", later_operation)
            later = call("rehearse", later["id"], "--digest", later["digest"])
            later = call("approve", later["id"], "--digest", later["digest"], "--yes")
            assert validate(later, True)["unchanged"] == 460
            conn.execute("UPDATE products SET category='human' WHERE id=41")
            drift = validate(later, False)
            assert (drift["checked"], drift["unchanged"], drift["changed"], drift["excluded"]) == (460, 459, 1, 40)
            assert len(drift["rows"]) == 1 and drift["rows"][0]["key_json"] == '{"id": 41}'
            assert drift["rows"][0]["reason"] == "record_version_changed"
            assert call("get", later["id"]) == later
            stale_repair_id = str(uuid4())
            call("apply", later["id"], "--digest", later["digest"], "--repair-operation", stale_repair_id,
                 "--yes", succeeds=False, failure_code="repair_conflict")
            assert dict(conn.execute("SELECT category,count(*) FROM products GROUP BY category").fetchall()) == {"bad": 459, "human": 41}
            assert conn.execute("SELECT count(*) FROM gregale_repair.operations WHERE id=%s", (stale_repair_id,)).fetchone()[0] == 0
            assert call("get", later["id"]) == later
            print("repair validation acceptance: one later edit identified among 460 approved candidates; plan and approval unchanged; stale apply rolled back every inverse write and receipt")

            # Resolve the original attempt at the source before replacement.
            # Closure is durable metadata, never a rewritten frozen selection.
            resolved = call("resolve", later["id"], "--digest", later["digest"],
                            "--repair-operation", stale_repair_id, "--yes")
            closure = resolved["apply_resolution"]
            assert closure["outcome"] == "not_committed" and closure["plan_digest"] == later["digest"]
            assert closure["repair_operation_id"] == stale_repair_id and closure["resolved_at"]
            original = dict(resolved)
            original.pop("apply_resolution")
            assert original == later
            assert call("resolve", later["id"], "--digest", later["digest"],
                        "--repair-operation", stale_repair_id, "--yes") == resolved
            call("apply", later["id"], "--digest", later["digest"],
                 "--repair-operation", stale_repair_id, "--yes", succeeds=False, failure_code="repair_conflict")
            call("apply", later["id"], "--digest", later["digest"],
                 "--repair-operation", str(uuid4()), "--yes", succeeds=False, failure_code="repair_conflict")
            assert validate(resolved, False)["issues"] == ["apply_closed"]
            later = resolved
            print("repair resolution acceptance: durable source closure recovered by the original identity; old and delayed apply identities fenced; frozen plan and approval preserved")

            # Age only private fixture metadata. A closed parent still needs
            # its values until a replacement is durably saved and old enough.
            with conn.transaction():
                conn.execute("ALTER TABLE gregale_repair.operations DISABLE TRIGGER gregale_repair_protect_operation")
                conn.execute("UPDATE gregale_repair.operations SET created_at=clock_timestamp()-interval '10 days' WHERE id=%s", (later_operation,))
                conn.execute("ALTER TABLE gregale_repair.operations ENABLE TRIGGER gregale_repair_protect_operation")
                conn.execute("UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '10 days' WHERE id=%s", (later["id"],))
            closed_hold = retention("plan")
            assert closed_hold["entries"] == [] and closed_hold["held_approved_plans"] == 1
            assert {"kind": "plan", "reason": "closed_without_replacement", "count": 1} in closed_hold["holds"]

            # The operator updates its declared policy for the explicitly
            # narrowed correction. Existing approval/evidence never transfers.
            server.terminate()
            server.wait(timeout=15)
            checks_file.write_text(json.dumps({"version": 1, "checks": [
                {"name": "Restored product categories", "sql": "SELECT count(*) FILTER(WHERE category='original')=459 FROM public.products"},
                {"name": "Human edits preserved", "sql": "SELECT count(*) FILTER(WHERE category='human')=41 FROM public.products"},
                {"name": "Repair remains enabled", "sql": "SELECT enabled FROM public.repair_policy WHERE id=1 FOR SHARE"},
            ]}))
            server = subprocess.Popen(server.args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            deadline = time.monotonic() + 15
            while True:
                if server.poll() is not None:
                    raise RuntimeError("repair executor exited before replacement readiness")
                try:
                    with urllib.request.urlopen(base, timeout=1) as response:
                        if response.status == 200:
                            break
                except OSError:
                    if time.monotonic() >= deadline:
                        raise RuntimeError("replacement executor readiness timed out") from None
                    time.sleep(0.05)
            replacement_id = str(uuid4())
            replacement = call("replace", later["id"], "--digest", later["digest"], "--repair-operation", replacement_id, "--yes")
            assert (replacement["candidates"], replacement["conflicts"], replacement["unavailable"]) == (459, 41, 0)
            assert replacement["checks_sha256"] != later["checks_sha256"]
            assert replacement["replacement"] == {
                "parent_plan_id": later["id"], "parent_plan_digest": later["digest"],
                "previous_candidates": 460, "retained_candidates": 459, "removed_candidates": 1, "previous_excluded": 40,
            }
            assert not replacement["approved_at"] and not replacement["rehearsed_at"] and not replacement["rehearsal"]
            changed = [item for item in replacement["items"] if item["previous_status"] != item["status"]]
            assert len(changed) == 1 and changed[0]["key_json"] == '{"id": 41}'
            assert changed[0]["previous_status"] == "candidate" and changed[0]["status"] == "conflict"
            parent = call("get", later["id"])
            assert parent.pop("replacement_plan_id") == replacement_id
            assert parent == later
            assert call("replace", later["id"], "--digest", later["digest"], "--repair-operation", replacement_id, "--yes") == replacement
            assert validate(later, False)["issues"] == ["plan_replaced", "apply_closed", "checks_changed"]
            call("apply", later["id"], "--digest", later["digest"], "--repair-operation", stale_repair_id,
                 "--yes", succeeds=False, failure_code="repair_conflict")

            young_fence = retention("plan")
            assert young_fence["entries"] == [] and young_fence["held_approved_plans"] == 1
            assert {"kind": "plan", "reason": "replacement_retention_minimum", "count": 1} in young_fence["holds"]
            # Keep the original fixed expiry; production never refreshes an
            # expired replacement when compacting or recovering its parent.
            conn.execute("UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '8 days' WHERE id=%s", (replacement_id,))
            superseded_batch = retention("plan")
            assert len(superseded_batch["entries"]) == 1 and superseded_batch["held_approved_plans"] == 0
            entry = superseded_batch["entries"][0]
            assert (entry["kind"], entry["id"], entry["reason"]) == ("plan", later["id"], "superseded_plan")
            assert entry["replacement_plan_id"] == replacement_id and entry["replacement_plan_digest"] == replacement["digest"] and entry["superseded_at"]
            superseded_receipt = retention("purge", superseded_batch["id"], "--digest", superseded_batch["digest"],
                                           "--actor", "retention-operator", "--yes")
            assert retention("get", superseded_batch["id"]) == superseded_receipt
            compacted_parent = call("get", later["id"])
            expected_parent = dict(later)
            expected_parent.update(items=[], replacement_plan_id=replacement_id, payload_purged_at=compacted_parent["payload_purged_at"])
            assert compacted_parent["payload_purged_at"] and compacted_parent == expected_parent
            assert call("resolve", later["id"], "--digest", later["digest"],
                        "--repair-operation", stale_repair_id, "--yes") == compacted_parent
            assert call("replace", later["id"], "--digest", later["digest"], "--repair-operation", replacement_id, "--yes") == replacement
            pinned = retention("plan")
            assert pinned["entries"] == []
            assert {"kind": "operation", "reason": "plan_payload_required", "count": 1} in pinned["holds"]
            assert conn.execute("SELECT count(*) FROM gregale_repair.changes WHERE operation_id=%s", (later_operation,)).fetchone()[0] == 500
            for delayed_id in (stale_repair_id, str(uuid4())):
                call("apply", later["id"], "--digest", later["digest"], "--repair-operation", delayed_id,
                     "--yes", succeeds=False, failure_code="repair_conflict")
            print("repair superseded retention acceptance: reviewed parent payload compacted; original closure, approval and replacement recovered; active child journal pinned and delayed parent applies fenced")

            call("approve", replacement_id, "--digest", replacement["digest"], "--yes", succeeds=False, failure_code="repair_conflict")
            replacement = call("rehearse", replacement_id, "--digest", replacement["digest"])
            replacement = call("approve", replacement_id, "--digest", replacement["digest"], "--yes")
            replacement = call("apply", replacement_id, "--digest", replacement["digest"], "--repair-operation", replacement_id, "--yes")
            assert replacement["repair_operation_id"] == replacement_id and replacement["applied_at"]
            assert all(item["status"] == "passed" for item in replacement["apply_checks"]["results"])
            assert dict(conn.execute("SELECT category,count(*) FROM products GROUP BY category").fetchall()) == {"original": 459, "human": 41}
            assert conn.execute("SELECT count(*) FROM gregale_repair.changes WHERE operation_id=%s", (replacement_id,)).fetchone()[0] == 459
            assert call("replace", later["id"], "--digest", later["digest"], "--repair-operation", replacement_id, "--yes") == replacement
            print("repair replacement acceptance: 459 restored and 41 later edits preserved; linked comparison, parent fence, new named policy, fresh rehearsal/approval and stable replacement receipt verified")

            observations = verify(replacement)
            assert (observations["restored"], observations["excluded"], observations["unchanged"]) == (459, 41, 459)
            conn.execute("UPDATE products SET category='human' WHERE id=42")
            observations = verify(replacement)
            assert (observations["restored"], observations["excluded"], observations["checked"], observations["unchanged"], observations["edited"], observations["missing"]) == (459, 41, 459, 458, 1, 0)
            assert len(observations["rows"]) == 1 and observations["rows"][0]["key_json"] == '{"id": 42}'
            assert call("get", replacement_id) == replacement
            assert call("apply", replacement_id, "--digest", replacement["digest"], "--repair-operation", replacement_id, "--yes") == replacement
            assert dict(conn.execute("SELECT category,count(*) FROM products GROUP BY category").fetchall()) == {"original": 458, "human": 42}
            # Exercise both retention stages while preserving receipt recovery.
            with conn.transaction():
                conn.execute("ALTER TABLE gregale_repair.operations DISABLE TRIGGER gregale_repair_protect_operation")
                conn.execute("UPDATE gregale_repair.operations SET created_at=clock_timestamp()-interval '10 days' WHERE id=%s", (replacement_id,))
                conn.execute("ALTER TABLE gregale_repair.operations ENABLE TRIGGER gregale_repair_protect_operation")
                conn.execute("UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '10 days',expires_at=clock_timestamp()-interval '9 days',applied_at=clock_timestamp()-interval '8 days' WHERE id=%s", (replacement_id,))
            replacement = call("get", replacement_id)
            plan_batch = retention("plan")
            assert [(entry["kind"], entry["id"]) for entry in plan_batch["entries"]] == [("plan", replacement_id)]
            retention("purge", plan_batch["id"], "--digest", plan_batch["digest"], "--actor", "retention-operator", "--yes")
            assert verify(replacement)["edited"] == 1
            journal_batch = retention("plan")
            assert {entry["id"] for entry in journal_batch["entries"]} == {later_operation, replacement_id}
            retention("purge", journal_batch["id"], "--digest", journal_batch["digest"], "--actor", "retention-operator", "--yes")
            unavailable = verify(replacement, False)
            assert unavailable["issues"] == ["journal_purged"] and unavailable["checked"] == 0 and unavailable["rows"] == []
            receipt = call("apply", replacement_id, "--digest", replacement["digest"], "--repair-operation", replacement_id, "--yes")
            assert receipt["payload_purged_at"] and receipt["repair_operation_id"] == replacement_id
            print("repair verification acceptance: 459 restored at commit, 458 unchanged and 1 later edit observed; 41 original exclusions and receipt preserved; journal purge explicitly unavailable")

            # Discard known plan identities: reconstruct navigation using only
            # the original import ID after both payload-retention stages.
            before_history = conn.execute("SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p").fetchone()[0]
            recovered_page = history(later_operation, "--limit", "1")
            recovered = list(recovered_page["items"])
            while recovered_page.get("next_cursor"):
                recovered_page = history(later_operation, "--limit", "1", "--cursor", recovered_page["next_cursor"])
                recovered.extend(recovered_page["items"])
            assert len(recovered) == 2
            original = next(item for item in recovered if item.get("replacement_plan_id"))
            repaired = next(item for item in recovered if item.get("applied_at"))
            assert original["payload_purged_at"] and original["apply_resolution"]["outcome"] == "not_committed"
            assert original["approved_at"] and original["replacement_plan_id"] == repaired["id"]
            assert repaired["replacement"]["parent_plan_id"] == original["id"] and repaired["replacement"]["parent_plan_digest"] == original["digest"]
            assert (original["candidates"], original["conflicts"], repaired["candidates"], repaired["conflicts"]) == (460, 40, 459, 41)
            reopened = call("get", repaired["id"])
            assert reopened["digest"] == repaired["digest"] and reopened["payload_purged_at"]
            unavailable = verify(reopened, False)
            assert unavailable["issues"] == ["journal_purged"] and (unavailable["restored"], unavailable["excluded"]) == (459, 41)
            assert conn.execute("SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p").fetchone()[0] == before_history
            print("repair history acceptance: source ID recovered the approved 460-candidate parent, durable closure, 459/41 replacement and committed receipt after payload compaction; no apply or business writes")
            assert conn.execute("SELECT payload_purged_at IS NOT NULL FROM gregale_repair.operations WHERE id=%s", (operation,)).fetchone()[0]
            def no_reimport(_cursor):
                raise AssertionError("a repaired and purged import must not execute again")
            with psycopg.connect(writer_dsn, autocommit=True) as writer:
                replay = with_repairable_operation_transaction(writer, capture_request, no_reimport, **capture_config)
                assert replay.replayed and replay.body == capture_result.body
            assert conn.execute("SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p").fetchone()[0] == before_history
            assert conn.execute("SELECT count(*) FROM gregale_repair.capture_receipts").fetchone()[0] == 1
            print("repairable transaction acceptance: restricted writer captured 500 products; immutable binding survived isolated repair and payload purge; original import replayed without repeating writes")
    finally:
        if server is not None:
            server.terminate()
            try:
                server.wait(timeout=15)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=5)
        if conn is not None:
            if writer_role is not None:
                conn.execute(sql.SQL("DROP OWNED BY {};DROP ROLE {}").format(sql.Identifier(writer_role), sql.Identifier(writer_role)))
            conn.close()
        try:
            admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(database)))
        finally:
            admin.close()


if __name__ == "__main__":
    main()
