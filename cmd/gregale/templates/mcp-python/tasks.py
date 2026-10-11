"""Durable MCP Task storage compatible with the Gregale Node starter."""

from __future__ import annotations

import hashlib
import hmac
import json
import os
import re
import threading
import time
import uuid
from datetime import UTC, datetime
from typing import Any

from cryptography.hazmat.primitives.ciphers.aead import AESGCM


TASK_EXTENSION_ID = "io.modelcontextprotocol/tasks"
TASK_TABLE = "gregale_mcp_tasks"
ENV_NAME = re.compile(r"^[A-Z_][A-Z0-9_]*$")
UUID_PATTERN = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$", re.I)
MAX_ATTEMPTS = 3
LEASE_SECONDS = 30
DEFAULT_MAX_OUTSTANDING = 1000
DEFAULT_MAX_PER_OWNER = 100


class TaskCapacityError(RuntimeError):
    """The namespace or owner has reached its outstanding-task limit."""


def task_settings(config: dict[str, Any]) -> dict[str, Any] | None:
    settings = config.get("tasks")
    if settings is None:
        return None
    if not isinstance(settings, dict):
        raise RuntimeError("tasks must be an object when configured")
    if settings.get("enabled", False) is False:
        return None
    if settings.get("enabled") is not True:
        raise RuntimeError("tasks.enabled must be true or false")
    for field in ("database_url_env", "owner_key_env", "namespace_env"):
        value = settings.get(field)
        if not isinstance(value, str) or not ENV_NAME.fullmatch(value):
            raise RuntimeError(f"tasks.{field} must name an uppercase environment variable")
    ttl = settings.get("ttl_seconds", 86400)
    poll = settings.get("poll_interval_ms", 2000)
    if not isinstance(ttl, int) or isinstance(ttl, bool) or not 60 <= ttl <= 30 * 24 * 60 * 60:
        raise RuntimeError("tasks.ttl_seconds must be between 60 and 2592000")
    if not isinstance(poll, int) or isinstance(poll, bool) or not 500 <= poll <= 30000:
        raise RuntimeError("tasks.poll_interval_ms must be between 500 and 30000")
    max_outstanding = settings.get("max_outstanding", DEFAULT_MAX_OUTSTANDING)
    max_per_owner = settings.get("max_outstanding_per_owner", DEFAULT_MAX_PER_OWNER)
    if any(not isinstance(value, int) or isinstance(value, bool) or value < 1 for value in (max_outstanding, max_per_owner)) or max_per_owner > max_outstanding:
        raise RuntimeError("tasks admission limits must be positive and the owner limit cannot exceed the namespace limit")
    return {**settings, "ttl_seconds": ttl, "poll_interval_ms": poll,
            "max_outstanding": max_outstanding, "max_outstanding_per_owner": max_per_owner}


class MCPTaskManager:
    def __init__(self, config: dict[str, Any]):
        settings = task_settings(config)
        if settings is None:
            raise ValueError("MCP Tasks are disabled")
        database_url = os.environ.get(settings["database_url_env"], "").strip()
        self.owner_key = os.environ.get(settings["owner_key_env"], "").encode()
        self.namespace = os.environ.get(settings["namespace_env"], "").strip()
        if not database_url:
            raise RuntimeError(f"environment variable {settings['database_url_env']} is required")
        if len(self.owner_key) < 32:
            raise RuntimeError(f"environment variable {settings['owner_key_env']} must contain at least 32 bytes")
        if not self.namespace or len(self.namespace) > 255:
            raise RuntimeError(f"environment variable {settings['namespace_env']} must contain a stable task namespace of at most 255 characters")
        self.resource = config["auth"].get("resource", "")
        self.auth_mode = config["auth"]["mode"]
        self.ttl_seconds = settings["ttl_seconds"]
        self.poll_interval_ms = settings["poll_interval_ms"]
        self.max_outstanding = settings["max_outstanding"]
        self.max_per_owner = settings["max_outstanding_per_owner"]
        self.payload_key = hmac.new(self.owner_key, b"gregale-mcp-task-payload:v1", hashlib.sha256).digest()
        try:
            from psycopg_pool import ConnectionPool
        except ImportError as exc:
            raise RuntimeError("install the psycopg[binary,pool] requirement to enable MCP Tasks") from exc
        self.pool = ConnectionPool(database_url, min_size=1, max_size=4, open=False, timeout=5)
        try:
            self.pool.open(wait=True)
            with self.pool.connection() as connection:
                table_exists = connection.execute(
                    "SELECT to_regclass('gregale_mcp_tasks') IS NOT NULL"
                ).fetchone()[0]
                if not table_exists:
                    connection.execute(
                        """CREATE TABLE IF NOT EXISTS gregale_mcp_tasks (
                        namespace text NOT NULL,
                        task_id uuid NOT NULL,
                        owner_hash bytea NOT NULL,
                        tool_name text NOT NULL,
                        handler_version text NOT NULL,
                        arguments_encrypted bytea NOT NULL,
                        status text NOT NULL CHECK (status IN ('queued', 'running', 'input_required', 'completed', 'cancelled', 'failed')),
                        result_encrypted bytea,
                        error_encrypted bytea,
                        input_state_encrypted bytea,
                        input_methods text[] NOT NULL DEFAULT '{}',
                        created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
                        updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
                        expires_at timestamptz NOT NULL,
                        attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
                        resume_pending boolean NOT NULL DEFAULT false,
                        next_attempt_at timestamptz,
                        lease_token uuid,
                        lease_expires_at timestamptz,
                        cancel_requested_at timestamptz,
                        PRIMARY KEY (namespace, task_id),
                        CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
                        CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
                        )"""
                    )
                    connection.execute(
                        "CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_queue_idx ON gregale_mcp_tasks (namespace, created_at, task_id) WHERE status IN ('queued', 'running')"
                    )
                connection.execute(
                    """SELECT namespace, task_id, owner_hash, tool_name, handler_version,
                        arguments_encrypted, result_encrypted, error_encrypted, status,
                        created_at, updated_at, expires_at, attempt_count, lease_token,
                        lease_expires_at, cancel_requested_at
                    FROM gregale_mcp_tasks LIMIT 0"""
                )
        except Exception as exc:
            self.pool.close()
            raise RuntimeError("could not initialize MCP Tasks PostgreSQL storage") from exc
        self._stop = threading.Event()
        self._worker = threading.Thread(target=self._run, name="gregale-mcp-tasks", daemon=True)
        self._worker.start()

    def _owner_hash(self, principal) -> bytes:
        if self.auth_mode == "open":
            identity = "open"
        else:
            if principal is None or not getattr(principal, "subject", ""):
                raise ValueError("authenticated MCP task owner is unavailable")
            identity = json.dumps(
                [self.resource, principal.subject, principal.client_id],
                ensure_ascii=False,
                separators=(",", ":"),
            )
        key = hmac.new(self.owner_key, b"gregale-mcp-task-owner:v1", hashlib.sha256).digest()
        return hmac.new(key, identity.encode(), hashlib.sha256).digest()

    def _aad(self, task_id: str, field: str) -> bytes:
        return f"gregale-mcp-task:v1:{self.namespace}:{task_id}:{field}".encode()

    def _encrypt(self, task_id: str, field: str, value: Any) -> bytes:
        plain = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
        if len(plain) > 256 * 1024:
            raise ValueError("MCP task payload exceeds the 256 KiB limit")
        nonce = os.urandom(12)
        sealed = AESGCM(self.payload_key).encrypt(nonce, plain, self._aad(task_id, field))
        return b"\x01" + nonce + sealed[-16:] + sealed[:-16]

    def _decrypt(self, task_id: str, field: str, encrypted: bytes) -> Any:
        if len(encrypted) < 29 or encrypted[0] != 1:
            raise ValueError("invalid encrypted MCP task payload")
        nonce = encrypted[1:13]
        sealed = encrypted[29:] + encrypted[13:29]
        return json.loads(AESGCM(self.payload_key).decrypt(nonce, sealed, self._aad(task_id, field)))

    def create(self, args: dict[str, Any], principal) -> dict[str, Any]:
        task_id = str(uuid.uuid4())
        owner_hash = self._owner_hash(principal)
        arguments = self._encrypt(task_id, "arguments", args)
        with self.pool.connection() as connection:
            connection.execute(
                "SELECT pg_advisory_xact_lock(hashtextextended(%s, 0))",
                (f"gregale_mcp_tasks:admission:{self.namespace}",),
            )
            total, owned = connection.execute(
                """SELECT COUNT(*)::bigint, COUNT(*) FILTER (WHERE owner_hash=%s)::bigint
                FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp()
                    AND status IN ('queued','running','input_required')""",
                (owner_hash, self.namespace),
            ).fetchone()
            if total >= self.max_outstanding or owned >= self.max_per_owner:
                raise TaskCapacityError("MCP task queue capacity reached")
            row = connection.execute(
                """INSERT INTO gregale_mcp_tasks (
                    namespace, task_id, owner_hash, tool_name, handler_version,
                    arguments_encrypted, input_methods, status,
                    expires_at
                ) VALUES (%s, %s, %s, 'build_report', '1', %s, '{}'::text[], 'queued',
                    clock_timestamp() + (%s * interval '1 second'))
                RETURNING task_id::text, tool_name, handler_version, status,
                    created_at, updated_at, expires_at, attempt_count""",
                (self.namespace, task_id, owner_hash, arguments, self.ttl_seconds),
            ).fetchone()
        task = self._row(row)
        return {
            "resultType": "task",
            "taskId": task["task_id"],
            "status": "working",
            "statusMessage": "Task accepted for processing.",
            "createdAt": _iso(task["created_at"]),
            "lastUpdatedAt": _iso(task["updated_at"]),
            "ttlMs": self.ttl_seconds * 1000,
            "pollIntervalMs": self.poll_interval_ms,
        }

    def get(self, task_id: str, principal) -> dict[str, Any] | None:
        if not UUID_PATTERN.fullmatch(task_id):
            return None
        with self.pool.connection() as connection:
            row = connection.execute(
                """SELECT task_id::text, tool_name, handler_version, status, created_at, updated_at,
                    expires_at, attempt_count, arguments_encrypted, result_encrypted, error_encrypted
                FROM gregale_mcp_tasks
                WHERE namespace=%s AND task_id=%s AND owner_hash=%s AND expires_at>clock_timestamp()""",
                (self.namespace, task_id, self._owner_hash(principal)),
            ).fetchone()
        if row is None:
            return None
        keys = ("task_id", "tool_name", "handler_version", "status", "created_at", "updated_at", "expires_at", "attempt_count", "arguments_encrypted", "result_encrypted", "error_encrypted")
        task = dict(zip(keys, row))
        task["arguments"] = self._decrypt(task_id, "arguments", task.pop("arguments_encrypted"))
        result = task.pop("result_encrypted")
        error = task.pop("error_encrypted")
        task["result"] = self._decrypt(task_id, "result", result) if result else None
        task["error"] = self._decrypt(task_id, "error", error) if error else None
        return task

    def cancel(self, task_id: str, principal) -> bool:
        with self.pool.connection() as connection:
            row = connection.execute(
                """UPDATE gregale_mcp_tasks SET
                    status=CASE WHEN status IN ('queued','input_required') THEN 'cancelled' ELSE status END,
                    cancel_requested_at=CASE WHEN status IN ('queued','running','input_required') THEN COALESCE(cancel_requested_at,clock_timestamp()) ELSE cancel_requested_at END,
                    updated_at=CASE WHEN status IN ('queued','running','input_required') THEN clock_timestamp() ELSE updated_at END,
                    lease_token=CASE WHEN status IN ('queued','input_required') THEN NULL ELSE lease_token END,
                    lease_expires_at=CASE WHEN status IN ('queued','input_required') THEN NULL ELSE lease_expires_at END
                WHERE namespace=%s AND task_id=%s AND owner_hash=%s AND expires_at>clock_timestamp()
                RETURNING task_id""",
                (self.namespace, task_id, self._owner_hash(principal)),
            ).fetchone()
        return row is not None

    def update(self, task_id: str, principal, responses: dict[str, Any]) -> bool:
        if not isinstance(responses, dict) or responses:
            raise ValueError("this task has no outstanding client input requests")
        # The sample build_report handler does not request client input. Keep
        # this endpoint owner-scoped so handlers can add resumable input later.
        return self.get(task_id, principal) is not None

    def wire_task(self, task: dict[str, Any]) -> dict[str, Any]:
        status = "working" if task["status"] in {"queued", "running"} else task["status"]
        result = {
            "resultType": "complete",
            "taskId": task["task_id"],
            "status": status,
            "createdAt": _iso(task["created_at"]),
            "lastUpdatedAt": _iso(task["updated_at"]),
            "ttlMs": int((task["expires_at"] - task["created_at"]).total_seconds() * 1000),
            "pollIntervalMs": self.poll_interval_ms,
        }
        if status == "completed" and task["result"] is not None:
            result["result"] = task["result"]
        if status == "failed":
            result["error"] = task["error"] or {"code": -32603, "message": "Task execution failed"}
        return result

    def _claim(self) -> dict[str, Any] | None:
        with self.pool.connection() as connection:
            connection.execute(
                """UPDATE gregale_mcp_tasks SET status='failed', lease_token=NULL, lease_expires_at=NULL,
                    updated_at=clock_timestamp()
                WHERE namespace=%s AND status='running' AND lease_expires_at<=clock_timestamp() AND attempt_count >= %s""",
                (self.namespace, MAX_ATTEMPTS),
            )
            row = connection.execute(
                """SELECT task_id::text, tool_name, handler_version, arguments_encrypted, attempt_count
                FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp()
                    AND attempt_count<%s AND
                    ((status='queued' AND (next_attempt_at IS NULL OR next_attempt_at<=clock_timestamp())) OR
                    (status='running' AND lease_expires_at<=clock_timestamp()))
                ORDER BY created_at, task_id LIMIT 1 FOR UPDATE SKIP LOCKED""",
                (self.namespace, MAX_ATTEMPTS),
            ).fetchone()
            if row is None:
                return None
            task_id, tool_name, version, encrypted, _attempt = row
            lease = str(uuid.uuid4())
            attempt, updated_at = connection.execute(
                """UPDATE gregale_mcp_tasks SET status='running', attempt_count=attempt_count+1,
                    lease_token=%s, lease_expires_at=clock_timestamp()+(%s * interval '1 second'),
                    updated_at=clock_timestamp()
                WHERE namespace=%s AND task_id=%s RETURNING attempt_count, updated_at""",
                (lease, LEASE_SECONDS, self.namespace, task_id),
            ).fetchone()
            return {
                "task_id": task_id,
                "tool_name": tool_name,
                "handler_version": version,
                "arguments": self._decrypt(task_id, "arguments", encrypted),
                "attempt_count": attempt,
                "lease_token": lease,
                "updated_at": updated_at,
            }

    def _run(self) -> None:
        while not self._stop.is_set():
            try:
                task = self._claim()
                if task is not None:
                    self._execute(task)
                    continue
            except Exception:
                # Keep the worker alive; the durable lease lets another poll
                # recover the task after transient database failures.
                pass
            self._stop.wait(self.poll_interval_ms / 1000)

    def _execute(self, task: dict[str, Any]) -> None:
        args = task["arguments"]
        report = args.get("report", "")
        steps = args.get("steps", 1)
        for _ in range(steps):
            if self._stop.is_set():
                return
            try:
                with self.pool.connection() as connection:
                    row = connection.execute(
                        "SELECT cancel_requested_at IS NOT NULL FROM gregale_mcp_tasks WHERE namespace=%s AND task_id=%s AND lease_token=%s",
                        (self.namespace, task["task_id"], task["lease_token"]),
                    ).fetchone()
                if row is None:
                    return
                if row[0]:
                    with self.pool.connection() as connection:
                        connection.execute(
                            """UPDATE gregale_mcp_tasks SET status='cancelled', lease_token=NULL,
                                lease_expires_at=NULL, updated_at=clock_timestamp()
                            WHERE namespace=%s AND task_id=%s AND lease_token=%s""",
                            (self.namespace, task["task_id"], task["lease_token"]),
                        )
                    return
            except Exception:
                return
            if self._stop.wait(0.2):
                return
        result = {"content": [{"type": "text", "text": f"Report {report} is ready after {steps} steps."}]}
        try:
            encrypted = self._encrypt(task["task_id"], "result", result)
            with self.pool.connection() as connection:
                connection.execute(
                    """UPDATE gregale_mcp_tasks SET status='completed', result_encrypted=%s,
                        lease_token=NULL, lease_expires_at=NULL, updated_at=clock_timestamp()
                    WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running' AND cancel_requested_at IS NULL""",
                    (encrypted, self.namespace, task["task_id"], task["lease_token"]),
                )
        except Exception as exc:
            self._fail(task, exc)

    def _fail(self, task: dict[str, Any], exc: Exception) -> None:
        try:
            encrypted = self._encrypt(task["task_id"], "error", {"code": -32603, "message": str(exc)[:1000]})
            with self.pool.connection() as connection:
                connection.execute(
                    """UPDATE gregale_mcp_tasks SET
                        status=CASE WHEN attempt_count<%s THEN 'queued' ELSE 'failed' END,
                        error_encrypted=CASE WHEN attempt_count<%s THEN NULL ELSE %s END,
                        next_attempt_at=CASE WHEN attempt_count<%s THEN clock_timestamp()+interval '1 second' ELSE NULL END,
                        lease_token=NULL, lease_expires_at=NULL, updated_at=clock_timestamp()
                    WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running'""",
                    (MAX_ATTEMPTS, MAX_ATTEMPTS, encrypted, MAX_ATTEMPTS, self.namespace, task["task_id"], task["lease_token"]),
                )
        except Exception:
            pass

    def close(self) -> None:
        self._stop.set()
        self._worker.join(timeout=2)
        self.pool.close()

    @staticmethod
    def _row(row) -> dict[str, Any]:
        return dict(zip(("task_id", "tool_name", "handler_version", "status", "created_at", "updated_at", "expires_at", "attempt_count"), row))


def _iso(value: datetime) -> str:
    return value.astimezone(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")
