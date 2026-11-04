"""Observe one exclusively leased application writer session, without writes.

The connection must be idle/autocommit and psycopg-compatible. These helpers own
a read-only snapshot and always roll it back. Connection/I/O timeouts remain
driver configuration; the generated probe sets bounded statement/lock timeouts.
"""
import asyncio
import json
from datetime import UTC, datetime
from importlib.resources import files
from typing import Any
from uuid import UUID

from .operations import _ready, _tuple_row


def _probe() -> dict[str, Any]:
    return json.loads(files(__package__).joinpath("data_repair_readiness_probe.json").read_text(encoding="utf-8"))


def _options(p: dict[str, Any], *, account_id: str, app_id: str, environment: str, table: str,
             release: str, platform_tenant_id: str = "", receipt_protocol: str = "managed") -> dict[str, Any]:
    try:
        for value in (account_id, app_id, *([platform_tenant_id] if platform_tenant_id else [])):
            if not isinstance(value, str) or str(UUID(value)) != value or UUID(value).int == 0:
                raise ValueError("invalid UUID")
        for value in (environment, release):
            if not isinstance(value, str) or not value or len(value.encode("utf-8")) > p["max_context_bytes"] or "\0" in value:
                raise ValueError("invalid context")
        if (not isinstance(table, str) or table.strip() != table or "." not in table or "\0" in table
                or len(table.encode("utf-8")) > p["max_table_bytes"] or receipt_protocol not in ("managed", "customer")
                or (receipt_protocol == "customer" and not platform_tenant_id)):
            raise ValueError("invalid table or receipt protocol")
    except (ValueError, TypeError, AttributeError) as error:
        raise ValueError("invalid data repair capture readiness options") from error
    scope = {"account_id": account_id, "app_id": app_id, "environment": environment}
    if platform_tenant_id:
        scope["platform_tenant_id"] = platform_tenant_id
    return {"scope": scope, "table": table, "release": release, "receipt_protocol": receipt_protocol}


def _finding(p: dict[str, Any], identity: str, code: str, objects: list[str] | None = None) -> dict[str, Any]:
    objects = [] if objects is None else objects
    m = p["messages"].get(code)
    if (not m or (m["id"] and m["id"] != identity) or identity not in p["finding_ids"]
            or not isinstance(objects, list) or len(objects) > p["max_objects"]
            or any(not isinstance(o, str) or not o or len(o.encode("utf-8")) > p["max_table_bytes"] or "\0" in o for o in objects)):
        raise ValueError("invalid capture probe frame")
    return {"id": identity, "code": code, "status": m["status"], "summary": m["summary"], "action": m["action"], "objects": objects}


def _report(p: dict[str, Any], config: dict[str, Any]) -> dict[str, Any]:
    return {"version": p["version"], "profile": "capture", **config, "checked_at": datetime.now(UTC).isoformat(),
            "status": "unknown", "findings": [_finding(p, identity, "writer_unavailable" if identity == "connection" else "not_inspected")
                                               for identity in p["finding_ids"]]}


def _merge(p: dict[str, Any], report: dict[str, Any], frame: Any) -> bool:
    if (not isinstance(frame, dict) or len(json.dumps(frame).encode("utf-8")) > p["max_response_bytes"]
            or not isinstance(frame.get("findings"), list) or not isinstance(frame.get("inspect_enrollment"), bool)):
        raise ValueError("invalid capture probe frame")
    seen = set()
    for row in frame["findings"]:
        if row["id"] in seen:
            raise ValueError("invalid capture probe frame")
        seen.add(row["id"])
        objects = row["objects"]
        index = p["finding_ids"].index(row["id"])
        old = report["findings"][index]
        if row["id"] == "permissions" and row["code"] == "writer_permissions_missing" and old["code"] == row["code"]:
            objects = old["objects"] + objects
        report["findings"][index] = _finding(p, row["id"], row["code"], objects)
    return frame["inspect_enrollment"]


def _aggregate(report: dict[str, Any]) -> dict[str, Any]:
    statuses = {f["status"] for f in report["findings"]}
    report["status"] = "blocked" if "blocked" in statuses else "unknown" if "unknown" in statuses else "ready"
    return report


def check_data_repair_capture(connection: Any, **options: Any) -> dict[str, Any]:
    """Inspect a writer connection. Options are trusted application context.

    An active caller transaction is rejected before issuing any SQL. The report
    covers capture/base receipt prerequisites, not optional outboxes, executor
    readiness, business invariants or whether every application command captures.
    """
    p = _probe()
    config = _options(p, **options)
    _ready(connection)
    report = _report(p, config)
    try:
        with connection.cursor(row_factory=_tuple_row) as cursor:
            cursor.execute("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
            cursor.execute(p["setup_sql"])
            cursor.execute(p["python_catalog_sql"], (config["receipt_protocol"],))
            if _merge(p, report, cursor.fetchone()[0]):
                cursor.execute(p["python_enrollment_sql"], (config["table"],))
                _merge(p, report, cursor.fetchone()[0])
    except Exception:
        report["findings"][0] = _finding(p, "connection", "writer_unavailable")
    finally:
        try:
            connection.rollback()
        except Exception:
            try:
                connection.close()
            except Exception:
                pass
            report["findings"][0] = _finding(p, "connection", "writer_unavailable")
    return _aggregate(report)


async def acheck_data_repair_capture(connection: Any, **options: Any) -> dict[str, Any]:
    """Async equivalent. Cancellation rolls back the owned snapshot; a failed
    rollback closes the connection instead of exposing a dirty session."""
    p = _probe()
    config = _options(p, **options)
    _ready(connection)
    report = _report(p, config)
    try:
        async with asyncio.timeout(p["timeout_ms"] / 1000):
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                await cursor.execute("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
                await cursor.execute(p["setup_sql"])
                await cursor.execute(p["python_catalog_sql"], (config["receipt_protocol"],))
                if _merge(p, report, (await cursor.fetchone())[0]):
                    await cursor.execute(p["python_enrollment_sql"], (config["table"],))
                    _merge(p, report, (await cursor.fetchone())[0])
    except Exception:
        report["findings"][0] = _finding(p, "connection", "writer_unavailable")
    finally:
        try:
            async with asyncio.timeout(p["timeout_ms"] / 1000):
                await connection.rollback()
        except asyncio.CancelledError:
            await connection.close()
            raise
        except Exception:
            try:
                await connection.close()
            except Exception:
                pass
            report["findings"][0] = _finding(p, "connection", "writer_unavailable")
    return _aggregate(report)
