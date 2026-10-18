"""PostgreSQL-backed durable MCP Tasks for the Python starter."""

from datetime import datetime, timezone
import hashlib
import hmac
import json
import logging
import math
import os
import queue
import re
import threading
import time
import uuid

TASKS_EXTENSION_ID = "io.modelcontextprotocol/tasks"
TASK_ID_PATTERN = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$")
TASK_KEY_ID_PATTERN = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
TASK_TOOL_NAME_PATTERN = re.compile(r"^[\x21-\x7e]{1,128}$")
TASK_HANDLER_VERSION_PATTERN = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
MAX_INPUT_STATE_BYTES = 256 * 1024
SUPPORTED_INPUT_METHODS = {"elicitation/create:form", "elicitation/create:url", "sampling/createMessage", "roots/list"}
_TASK_HANDLER_LOCK = threading.RLock()
_TASK_HANDLERS = {}
CREATE_SCHEMA = """
CREATE TABLE IF NOT EXISTS gregale_mcp_tasks (
  namespace text NOT NULL, task_id uuid NOT NULL, owner_hash bytea NOT NULL,
  tool_name text NOT NULL, handler_version text NOT NULL, arguments_encrypted bytea NOT NULL,
  status text NOT NULL CHECK (status IN ('queued','running','input_required','completed','cancelled','failed')),
  result_encrypted bytea, error_encrypted bytea, input_state_encrypted bytea,
  input_methods text[] NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), expires_at timestamptz NOT NULL,
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0), resume_pending boolean NOT NULL DEFAULT false,
  lease_token uuid, lease_expires_at timestamptz, cancel_requested_at timestamptz,
  PRIMARY KEY(namespace, task_id),
  CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
  CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)))
"""
CREATE_WORKER_HANDLERS_SCHEMA = """
CREATE TABLE IF NOT EXISTS gregale_mcp_task_worker_handlers (
  namespace text NOT NULL, worker_id uuid NOT NULL, tool_name text NOT NULL,
  handler_version text NOT NULL, deployment_id text NOT NULL DEFAULT '',
  heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(namespace,worker_id,tool_name,handler_version))
"""
CREATE_TASK_NOTIFY_FUNCTION = """
CREATE OR REPLACE FUNCTION gregale_mcp_task_notify_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    PERFORM pg_notify('gregale_mcp_task_' || md5(NEW.namespace), NEW.task_id::text);
  ELSIF current_setting('gregale.mcp_task_rewrap', true) IS DISTINCT FROM 'on' AND (
        NEW.status IS DISTINCT FROM OLD.status OR
        NEW.updated_at IS DISTINCT FROM OLD.updated_at OR
        NEW.result_encrypted IS DISTINCT FROM OLD.result_encrypted OR
        NEW.error_encrypted IS DISTINCT FROM OLD.error_encrypted OR
        NEW.input_state_encrypted IS DISTINCT FROM OLD.input_state_encrypted) THEN
    PERFORM pg_notify('gregale_mcp_task_' || md5(NEW.namespace), NEW.task_id::text);
  END IF;
  RETURN NEW;
END;
$$
"""
CREATE_TASK_NOTIFY_TRIGGER = """
DO $body$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger
     WHERE tgrelid = 'gregale_mcp_tasks'::regclass
       AND tgname = 'gregale_mcp_tasks_notify'
       AND NOT tgisinternal
  ) THEN
    CREATE TRIGGER gregale_mcp_tasks_notify
      AFTER INSERT OR UPDATE ON gregale_mcp_tasks
      FOR EACH ROW EXECUTE FUNCTION gregale_mcp_task_notify_change();
  END IF;
END;
$body$
"""


def _derive_key(key, purpose):
    return hmac.new(key, purpose.encode("ascii"), hashlib.sha256).digest()


def _timestamp(value):
    return value.astimezone(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def resolve_task_role(role=None):
    resolved = role if role is not None else os.environ.get("MCP_TASKS_ROLE", "combined").strip() or "combined"
    if resolved not in {"combined", "web", "worker", "observer"}:
        raise ValueError("MCP_TASKS_ROLE must be combined, web, worker, or observer")
    return resolved


class TaskNotFound(Exception):
    pass


class TaskInputLeaseLost(Exception):
    pass


class TaskInputCancelled(Exception):
    pass


class TaskInputRequired(Exception):
    """Raised to pause execution until the client answers outstanding input."""


class TaskExecutionContext:
    """Context passed to application task handlers."""

    def __init__(self, task_id, cancelled, request_inputs):
        self.task_id = task_id
        self.cancelled = cancelled
        self._request_inputs = request_inputs

    def request_inputs(self, requests):
        return self._request_inputs(requests)

    def request_input(self, key, request):
        return self.request_inputs({key: request})[key]


def register_task_handler(name, *, version, execute, validate, previous_versions=None):
    """Register a durable executor before starting the web or worker process.

    Register the same active and retained versions in every process sharing a
    queue. Validators run before new task arguments are persisted.
    """
    if not isinstance(name, str) or not TASK_TOOL_NAME_PATTERN.fullmatch(name):
        raise ValueError("MCP task tool name must contain 1 to 128 visible ASCII characters")
    if (not isinstance(version, str) or not TASK_HANDLER_VERSION_PATTERN.fullmatch(version)
            or not callable(execute) or not callable(validate)):
        raise ValueError("MCP task handlers require a version, validator, and executor")
    if previous_versions is None:
        previous_versions = {}
    if not isinstance(previous_versions, dict):
        raise ValueError("MCP task previous_versions must be a mapping")
    executors = {}
    for previous_version, previous_execute in previous_versions.items():
        if (not isinstance(previous_version, str) or not TASK_HANDLER_VERSION_PATTERN.fullmatch(previous_version)
                or previous_version == version or not callable(previous_execute)):
            raise ValueError("MCP task previous handler versions are invalid")
        executors[previous_version] = previous_execute
    executors[version] = execute
    handler = {"version": version, "validate": validate, "execute": execute, "executors": executors}
    with _TASK_HANDLER_LOCK:
        if name in _TASK_HANDLERS:
            raise ValueError(f"MCP task handler {name!r} is already registered")
        _TASK_HANDLERS[name] = handler
    return handler


def _snapshot_task_handlers():
    with _TASK_HANDLER_LOCK:
        return {
            name: {**handler, "executors": dict(handler["executors"])}
            for name, handler in _TASK_HANDLERS.items()
        }


def _input_methods_for(capabilities):
    capabilities = capabilities if isinstance(capabilities, dict) else {}
    methods = []
    elicitation = capabilities.get("elicitation")
    if isinstance(elicitation, dict):
        if elicitation.get("form") or not elicitation:
            methods.append("elicitation/create:form")
        if elicitation.get("url"):
            methods.append("elicitation/create:url")
    if isinstance(capabilities.get("sampling"), dict):
        methods.append("sampling/createMessage")
    if isinstance(capabilities.get("roots"), dict):
        methods.append("roots/list")
    return methods


def _task_string_length(value):
    return len(value.encode("utf-16-le", errors="surrogatepass")) // 2


class DurableTaskRuntime:
    def __init__(self, config, role=None):
        self.role = resolve_task_role(role)
        raw_timeout = os.environ.get("MCP_DRAIN_TIMEOUT_SECONDS", "10")
        if not raw_timeout.isdecimal() or not 1 <= int(raw_timeout) <= 300:
            raise ValueError("MCP_DRAIN_TIMEOUT_SECONDS must be an integer between 1 and 300")
        self.drain_timeout = int(raw_timeout)
        self._drain_lock = threading.Lock()
        self._drain_deadline = None
        self._drain_timer = None
        self._drain_pending = []
        self._drain_expired = threading.Event()
        self._active_tasks = {}
        self._close_lock = threading.Lock()
        self._closed = False
        self._worker_id = uuid.uuid4()
        self._deployment_id = os.environ.get("FAAS_DEPLOYMENT_ID", "").strip()
        self._task_handlers = _snapshot_task_handlers()
        self._task_executors = {
            (name, version): execute
            for name, handler in self._task_handlers.items()
            for version, execute in handler["executors"].items()
        }
        self._subscription_lock = threading.Lock()
        self._subscription_count = 0
        self._watched_task_count = 0
        self._task_watchers_lock = threading.Lock()
        self._task_watchers = {}
        settings = config.get("tasks")
        if settings is None:
            self.enabled = False
            if self.role in {"worker", "observer"}:
                raise ValueError("MCP Tasks worker or observer requires tasks.enabled=true")
            return
        if not isinstance(settings, dict) or not isinstance(settings.get("enabled"), bool):
            raise ValueError("tasks.enabled must be explicitly true or false when task settings are present")
        self.enabled = settings["enabled"]
        if not self.enabled:
            if self.role in {"worker", "observer"}:
                raise ValueError("MCP Tasks worker or observer requires tasks.enabled=true")
            return

        try:
            from cryptography.hazmat.primitives.ciphers.aead import AESGCM
            from psycopg.conninfo import conninfo_to_dict
            from psycopg.rows import dict_row
            from psycopg_pool import ConnectionPool
        except ImportError as exc:
            raise ValueError("install requirements.txt to enable durable MCP Tasks") from exc
        self._aesgcm = AESGCM

        names = {
            "database_url": settings.get("database_url_env", "DATABASE_URL"),
            "owner_key": settings.get("owner_key_env", "MCP_TASK_OWNER_KEY"),
            "namespace": settings.get("namespace_env", "FAAS_APP_ID"),
        }
        owner_key_id = settings.get("owner_key_id", "default")
        payload_encryption_version = settings.get("payload_encryption_version", 1)
        previous_settings = settings.get("previous_owner_keys", [])
        if not isinstance(owner_key_id, str) or not TASK_KEY_ID_PATTERN.fullmatch(owner_key_id):
            raise ValueError("Invalid MCP task owner key ID")
        if not isinstance(previous_settings, list) or len(previous_settings) > 16:
            raise ValueError("MCP task previous_owner_keys must contain at most 16 keys")
        if isinstance(payload_encryption_version, bool) or not isinstance(payload_encryption_version, int) or payload_encryption_version not in {1, 2}:
            raise ValueError("MCP task payload encryption version must be 1 or 2")
        previous_key_settings = []
        key_ids = {owner_key_id}
        for previous in previous_settings:
            if not isinstance(previous, dict) or not isinstance(previous.get("id"), str) or not TASK_KEY_ID_PATTERN.fullmatch(previous["id"]):
                raise ValueError("Invalid MCP task previous owner key configuration")
            env_name = previous.get("env")
            if not isinstance(env_name, str) or not re.fullmatch(r"[A-Z_][A-Z0-9_]*", env_name) or previous["id"] in key_ids:
                raise ValueError("MCP task owner key IDs must be valid and unique")
            key_ids.add(previous["id"])
            previous_key_settings.append({"id": previous["id"], "env": env_name})
        if any(not isinstance(name, str) or not re.fullmatch(r"[A-Z_][A-Z0-9_]*", name) for name in names.values()):
            raise ValueError("MCP task environment variable name is invalid")
        database_url = os.environ.get(names["database_url"])
        owner_key = os.environ.get(names["owner_key"])
        previous_owner_keys = [
            {"id": previous["id"], "key": os.environ.get(previous["env"])}
            for previous in previous_key_settings
        ]
        namespace_override = os.environ.get("MCP_TASK_NAMESPACE", "").strip()
        if self.role != "combined" and not namespace_override:
            raise ValueError(f"MCP Tasks role {self.role} requires MCP_TASK_NAMESPACE to match every app sharing the queue")
        namespace = namespace_override or os.environ.get(names["namespace"], "").strip()
        if not database_url or (self.role != "observer" and (not owner_key or len(owner_key.encode("utf-8")) < 32)) or not namespace or len(namespace) > 255:
            raise ValueError("MCP Tasks require a PostgreSQL URL, a 32-byte owner key, and a stable namespace")

        self.namespace = namespace
        self.payload_encryption_version = payload_encryption_version
        self.notification_channel = "gregale_mcp_task_" + hashlib.md5(namespace.encode("utf-8")).hexdigest()
        if self.role != "observer":
            key_specs = [{"id": owner_key_id, "key": owner_key}, *previous_owner_keys]
            self.owner_keys = []
            for spec in key_specs:
                if not isinstance(spec["key"], str) or len(spec["key"].encode("utf-8")) < 32:
                    raise ValueError("MCP task owner and previous keys must contain at least 32 bytes")
                master_key = spec["key"].encode("utf-8")
                self.owner_keys.append({
                    "id": spec["id"],
                    "owner_hash_key": _derive_key(master_key, "gregale-mcp-task-owner:v1"),
                    "payload_key": _derive_key(master_key, "gregale-mcp-task-payload:v1"),
                })
            self.active_owner_key_id = owner_key_id
            self.payload_keys = {item["id"]: item["payload_key"] for item in self.owner_keys}
        self._main_payload_keys = {}
        if self.role != "observer" and settings.get("encryption_keys_env"):
            name = settings["encryption_keys_env"]
            if not isinstance(name,str) or not re.fullmatch(r"[A-Z_][A-Z0-9_]*",name):
                raise ValueError("invalid task encryption environment name")
            keys = json.loads(os.environ.get(name,""))
            if not isinstance(keys,dict) or set(keys)!={"activeKeyId","keys"} or not isinstance(keys["keys"],dict) or not 1 <= len(keys["keys"]) <= 16:
                raise ValueError("invalid MCP task encryption keys")
            for key_id,secret in keys["keys"].items():
                if key_id=="legacy" or not re.fullmatch(r"[A-Za-z0-9_-]{1,64}",key_id) or not isinstance(secret,str) or len(secret.encode())<32:
                    raise ValueError("invalid MCP task encryption keys")
                self._main_payload_keys[key_id]=_derive_key(secret.encode(),"gregale-mcp-task-payload:v2:"+key_id)
            active=keys["activeKeyId"]
            if active!="legacy" and active not in self._main_payload_keys:
                raise ValueError("unknown active MCP task encryption key")
            self._legacy_payload_keys=dict(self.payload_keys)
            self.payload_keys.update(self._main_payload_keys)
            self.payload_keys["legacy"]=self.owner_keys[0]["payload_key"]
            self.active_owner_key_id=active
            self.payload_encryption_version=1 if active=="legacy" else 2
        def integer_setting(name, default, zero_uses_default=False):
            value = settings.get(name)
            if value is None or zero_uses_default and value == 0:
                return default
            if isinstance(value, bool) or not isinstance(value, int):
                raise ValueError(f"tasks.{name} must be an integer")
            return value

        self.ttl_ms = integer_setting("ttl_seconds", 86400) * 1000
        self.poll_interval_ms = integer_setting("poll_interval_ms", 2000)
        self.concurrency = integer_setting("worker_concurrency", 1)
        self.max_outstanding = integer_setting("max_outstanding", 1000, zero_uses_default=True)
        self.max_per_owner = integer_setting("max_outstanding_per_owner", 100, zero_uses_default=True)
        if not 60_000 <= self.ttl_ms <= 30 * 24 * 60 * 60 * 1000 or not 500 <= self.poll_interval_ms <= 30_000 or not 1 <= self.concurrency <= 16 or self.max_outstanding < 1 or not 1 <= self.max_per_owner <= self.max_outstanding:
            raise ValueError("MCP task limits are outside their supported ranges")

        from task_metrics import resolve_metrics_settings, start_metrics_publisher

        metrics_settings = resolve_metrics_settings(self.role)
        if self.role == "observer" and metrics_settings is None:
            raise ValueError("MCP task observer requires scaling metric credentials")

        self.pool = ConnectionPool(
            database_url,
            min_size=1,
            max_size=max(4, self.concurrency + 2),
            kwargs={"row_factory": dict_row, "connect_timeout": 5, "options": conninfo_to_dict(database_url).get("options", "") + " -c statement_timeout=10000"},
            open=False,
        )
        try:
            self.pool.open(wait=True, timeout=10)
            check_task_schema(self.pool)
            if self.role == "observer":
                self.queue_metrics()
        except Exception:
            self.pool.close()
            if self.role == "observer":
                raise ValueError("MCP task observer health check failed; verify its read-only database grants") from None
            raise ValueError("MCP Task schema is unavailable or incompatible; run python task_schema.py apply with the migration account") from None
        self._stopping = threading.Event()
        self._notification_thread = None
        self._worker = None
        self._capability_heartbeat_thread = None
        self._rewrapper = None
        if self.role in {"combined", "web"}:
            self._notification_thread = threading.Thread(
                target=self._task_notification_loop,
                name="gregale-mcp-task-notifications",
                daemon=True,
            )
            self._notification_thread.start()
        if self.role in {"combined", "worker"}:
            self._capability_heartbeat_thread = threading.Thread(
                target=self._worker_capability_heartbeat_loop,
                name="gregale-mcp-task-capabilities",
                daemon=True,
            )
            self._capability_heartbeat_thread.start()
            self._worker = threading.Thread(target=self._worker_loop, name="gregale-mcp-tasks", daemon=True)
            self._worker.start()
        if self.role != "observer" and self.payload_encryption_version == 2:
            self._rewrapper = threading.Thread(target=self._rewrap_loop, name="gregale-mcp-task-key-rewrap", daemon=True)
            self._rewrapper.start()
        self._metrics_close = start_metrics_publisher(self, metrics_settings, observer=self.role == "observer") if metrics_settings else None

    def begin_drain(self):
        if not getattr(self, "enabled", False):
            return
        with self._drain_lock:
            if self._drain_deadline is not None:
                return
            self._drain_deadline = time.monotonic() + self.drain_timeout
            self._stopping.set()
            count = len(self._active_tasks)
            self._drain_timer = threading.Timer(self.drain_timeout, self._expire_drain)
            self._drain_timer.daemon = True
            self._drain_timer.start()
        print(json.dumps({"event": "mcp_task_drain", "state": "draining", "active_tasks": count, "timeout_seconds": self.drain_timeout}), flush=True)

    def _expire_drain(self):
        with self._drain_lock:
            if self._drain_expired.is_set():
                return
            self._drain_expired.set()
            self._drain_pending = list(self._active_tasks.values())
            for _, cancelled in self._drain_pending:
                cancelled.set()

    def _release_drained_task(self, task):
        with self.pool.connection() as connection:
            connection.execute(
                "UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NULL THEN 'queued' ELSE 'cancelled' END,"
                "resume_pending=cancel_requested_at IS NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() "
                "WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running'",
                (self.namespace, task["task_id"], task["lease_token"]),
            )

    def close(self):
        if not getattr(self, "enabled", False):
            return
        with self._close_lock:
            if self._closed:
                return
            self.begin_drain()
            if self._metrics_close is not None:
                self._metrics_close()
            if self._capability_heartbeat_thread is not None:
                self._capability_heartbeat_thread.join(timeout=5)
            if self._worker is not None:
                self._worker.join(timeout=max(0, self._drain_deadline - time.monotonic()))
            self._expire_drain()
            self._drain_timer.cancel()
            with self._drain_lock:
                pending = list(self._drain_pending)
            # Revoke the lease before requeueing. All heartbeat/input/result
            # writes use that token, so a late handler cannot affect a new owner.
            if pending:
                try:
                    with self.pool.connection(timeout=5) as connection:
                        with connection.transaction():
                            connection.execute("SET LOCAL statement_timeout='5s'")
                            connection.execute(
                                "UPDATE gregale_mcp_tasks AS task SET status=CASE WHEN task.cancel_requested_at IS NULL THEN 'queued' ELSE 'cancelled' END,"
                                "resume_pending=task.cancel_requested_at IS NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() "
                                "FROM unnest(%s::uuid[],%s::uuid[]) AS owner(task_id,lease_token) "
                                "WHERE task.namespace=%s AND task.task_id=owner.task_id AND task.lease_token=owner.lease_token AND task.status='running'",
                                ([task["task_id"] for task, _ in pending], [task["lease_token"] for task, _ in pending], self.namespace),
                            )
                except Exception:
                    logging.getLogger(__name__).error("MCP task drain release failed; lease expiry will recover unfinished work")
            print(json.dumps({"event": "mcp_task_drain", "state": "timed_out" if pending else "completed", "unfinished_tasks": len(pending)}), flush=True)
            for thread in (self._rewrapper, self._notification_thread):
                if thread is not None:
                    thread.join(timeout=1)
            self.pool.close()
            self._closed = True

    def can_create(self, tool_name):
        return tool_name in self._task_handlers

    def subscribe_task_changes(self, task_ids, updates=None):
        if updates is None:
            updates = queue.Queue(maxsize=64)
        normalized_ids = tuple(set(task_id.lower() for task_id in task_ids))
        with self._task_watchers_lock:
            for task_id in normalized_ids:
                self._task_watchers.setdefault(task_id, set()).add(updates)
        active = True

        def unsubscribe():
            nonlocal active
            with self._task_watchers_lock:
                if not active:
                    return
                active = False
                for task_id in normalized_ids:
                    watchers = self._task_watchers.get(task_id)
                    if watchers is None:
                        continue
                    watchers.discard(updates)
                    if not watchers:
                        self._task_watchers.pop(task_id, None)

        return updates, unsubscribe

    def _publish_task_change(self, task_id):
        with self._task_watchers_lock:
            watchers = tuple(self._task_watchers.get(task_id.lower(), ()))
        for updates in watchers:
            try:
                updates.put_nowait(task_id.lower())
            except queue.Full:
                # The bounded periodic snapshot check recovers dropped events.
                pass

    def _task_notification_loop(self):
        retry_delay = 1.0
        while not self._stopping.is_set():
            connection = None
            try:
                connection = self.pool.getconn(timeout=5)
                connection.execute(f"LISTEN {self.notification_channel}")
                connection.commit()
                retry_delay = 1.0
                while not self._stopping.is_set():
                    for notification in connection.notifies(timeout=1.0, stop_after=1):
                        if (
                            notification.channel == self.notification_channel
                            and isinstance(notification.payload, str)
                            and TASK_ID_PATTERN.fullmatch(notification.payload)
                        ):
                            self._publish_task_change(notification.payload)
            except Exception:
                # Task snapshots are still recovered by the bounded polling path.
                pass
            finally:
                if connection is not None:
                    try:
                        self.pool.putconn(connection, close=True)
                    except Exception:
                        pass
            if not self._stopping.is_set():
                self._stopping.wait(retry_delay)
                retry_delay = min(retry_delay * 2, 30.0)

    def reserve_task_subscription(self):
        with self._subscription_lock:
            if self._subscription_count >= 64:
                return False
            self._subscription_count += 1
            return True

    def reserve_watched_task_ids(self, count):
        with self._subscription_lock:
            if count < 0 or self._watched_task_count + count > 512:
                return False
            self._watched_task_count += count
            return True

    def release_task_subscription(self, task_count):
        with self._subscription_lock:
            task_count = min(max(task_count, 0), self._watched_task_count)
            self._watched_task_count -= task_count
            if self._subscription_count > 0:
                self._subscription_count -= 1

    def release_watched_task_ids(self, count):
        with self._subscription_lock:
            count = min(max(count, 0), self._watched_task_count)
            self._watched_task_count -= count

    def queue_metrics(self):
        if self.role == "observer":
            self._assert_read_only()
        with self.pool.connection() as connection:
            row = connection.execute(
                "SELECT LEAST(COUNT(*),1000000000000)::bigint AS outstanding_tasks, "
                "LEAST(GREATEST(COALESCE(EXTRACT(EPOCH FROM (clock_timestamp()-MIN(created_at))),0),0),1000000000000)::double precision AS oldest_age_seconds "
                ",(SELECT LEAST(COUNT(*),1000000000000)::bigint FROM gregale_mcp_tasks AS queued "
                "WHERE queued.namespace=%s AND queued.expires_at>clock_timestamp() "
                "AND (queued.status='queued' OR (queued.status='running' AND queued.lease_expires_at<=clock_timestamp())) "
                "AND (queued.attempt_count<3 OR (queued.status='queued' AND queued.resume_pending AND queued.attempt_count=3)) "
                "AND NOT EXISTS (SELECT 1 FROM gregale_mcp_task_worker_handlers AS handler "
                "WHERE handler.namespace=queued.namespace AND handler.tool_name=queued.tool_name "
                "AND handler.handler_version=queued.handler_version "
                "AND handler.heartbeat_at>clock_timestamp()-interval '45 seconds')) AS without_compatible_worker_tasks "
                "FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp() AND status IN ('queued','running')",
                (self.namespace, self.namespace),
            ).fetchone()
        if row is None:
            raise ValueError("could not read MCP task queue metrics")
        metrics = {
            "outstanding_tasks": int(row["outstanding_tasks"]),
            "oldest_age_seconds": float(row["oldest_age_seconds"]),
            "without_compatible_worker_tasks": int(row["without_compatible_worker_tasks"]),
        }
        if metrics["outstanding_tasks"] < 0 or metrics["oldest_age_seconds"] < 0 or metrics["without_compatible_worker_tasks"] < 0:
            raise ValueError("MCP task queue metrics returned invalid values")
        metrics["handler_compatibility"] = []
        metrics["compatibility_truncated"] = False
        if self.role == "observer":
            with self.pool.connection() as connection:
                try:
                    rows = connection.execute(
                    "WITH pending AS ("
                    "SELECT queued.namespace, queued.tool_name, queued.handler_version "
                    "FROM gregale_mcp_tasks AS queued "
                    "WHERE queued.namespace=%s AND queued.expires_at>clock_timestamp() "
                    "AND queued.status IN ('queued','running','input_required')"
                    "), waiting AS ("
                    "SELECT tool_name, handler_version, COUNT(*)::bigint AS waiting_tasks "
                    "FROM pending GROUP BY tool_name, handler_version"
                    "), limited_waiting AS ("
                    "SELECT *, COUNT(*) OVER () AS queue_pair_count FROM waiting "
                    "ORDER BY waiting_tasks DESC, tool_name, handler_version LIMIT 257"
                    "), by_deployment AS ("
                    "SELECT waiting.tool_name, waiting.handler_version, waiting.waiting_tasks, waiting.queue_pair_count, "
                    "handler.deployment_id, COUNT(DISTINCT handler.worker_id)::bigint AS workers "
                    "FROM limited_waiting AS waiting LEFT JOIN gregale_mcp_task_worker_handlers AS handler "
                    "ON handler.namespace=%s AND handler.tool_name=waiting.tool_name "
                    "AND handler.handler_version=waiting.handler_version "
                    "AND handler.heartbeat_at>clock_timestamp()-interval '45 seconds' "
                    "GROUP BY waiting.tool_name, waiting.handler_version, waiting.waiting_tasks, waiting.queue_pair_count, handler.deployment_id"
                    "), ranked AS ("
                    "SELECT *, SUM(workers) OVER (PARTITION BY tool_name,handler_version) AS all_workers, "
                    "COUNT(*) OVER (PARTITION BY tool_name,handler_version) AS deployment_count, "
                    "ROW_NUMBER() OVER (PARTITION BY tool_name,handler_version ORDER BY workers DESC,deployment_id) AS deployment_rank "
                    "FROM by_deployment"
                    ") "
                    "SELECT tool_name,handler_version, "
                    "LEAST(waiting_tasks,1000000000000::bigint)::bigint AS waiting_tasks, "
                    "LEAST(all_workers,1000000000000::bigint)::bigint AS live_compatible_workers, "
                    "COALESCE(deployment_id,'') AS deployment_id, "
                    "LEAST(workers,1000000000000::bigint)::bigint AS deployment_workers, deployment_count, queue_pair_count "
                    "FROM ranked WHERE deployment_rank<=64 "
                    "ORDER BY waiting_tasks DESC,tool_name,handler_version,deployment_rank",
                    (self.namespace, self.namespace),
                    ).fetchall()
                except Exception as exc:
                    if getattr(exc, "sqlstate", None) != "42703":
                        raise
                    # The read-only observer cannot migrate an old worker
                    # table. Publish a fail-closed snapshot until a worker
                    # adds deployment identity.
                    metrics["compatibility_truncated"] = True
                    return metrics
            by_handler = {}
            deployment_entries = 0
            for row in rows:
                key = (row["tool_name"], row["handler_version"])
                item = by_handler.get(key)
                if item is None:
                    item = {
                        "tool_name": row["tool_name"],
                        "handler_version": row["handler_version"],
                        "waiting_tasks": int(row["waiting_tasks"]),
                        "live_compatible_workers": int(row["live_compatible_workers"]),
                        "unknown_deployment_workers": int(row["live_compatible_workers"]),
                        "deployments": [],
                    }
                    by_handler[key] = item
                if int(row["queue_pair_count"]) > 256 or int(row["deployment_count"]) > 64:
                    metrics["compatibility_truncated"] = True
                workers = int(row["deployment_workers"])
                if row["deployment_id"] and workers > 0 and deployment_entries < 768:
                    item["deployments"].append({"deployment_id": row["deployment_id"], "workers": workers})
                    item["unknown_deployment_workers"] -= workers
                    deployment_entries += 1
                elif row["deployment_id"] and workers > 0:
                    metrics["compatibility_truncated"] = True
            metrics["handler_compatibility"] = list(by_handler.values())
            if len(metrics["handler_compatibility"]) > 256:
                metrics["compatibility_truncated"] = True
                metrics["handler_compatibility"] = metrics["handler_compatibility"][:256]
        return metrics

    def _assert_read_only(self):
        with self.pool.connection() as connection:
            row = connection.execute(
                "SELECT pg_catalog.to_regclass(%s)::oid IS NOT NULL AS table_exists, "
                "COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'INSERT') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'INSERT'),false) AS can_insert, "
                "COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'UPDATE') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'UPDATE'),false) AS can_update, "
                "COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'DELETE'),false) AS can_delete, "
                "COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'TRUNCATE'),false) AS can_truncate, "
                "COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'REFERENCES') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'REFERENCES'),false) AS can_reference, "
                "COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'TRIGGER'),false) AS can_create_trigger, "
                "pg_catalog.has_database_privilege(current_user,current_database(),'CREATE') AS can_create_schemas, "
                "COALESCE((SELECT pg_catalog.has_schema_privilege(current_user,n.nspname,'CREATE') FROM pg_catalog.pg_class AS c JOIN pg_catalog.pg_namespace AS n ON n.oid=c.relnamespace WHERE c.oid=pg_catalog.to_regclass(%s)::oid),false) AS can_create_in_task_schema",
                ("gregale_mcp_tasks",) * 11,
            ).fetchone()
        if row is None or not row["table_exists"] or any(row[key] for key in (
            "can_insert", "can_update", "can_delete", "can_truncate", "can_reference",
            "can_create_trigger", "can_create_schemas", "can_create_in_task_schema",
        )):
            raise ValueError("MCP task observer requires read-only PostgreSQL access")
        with self.pool.connection() as connection:
            worker_table = connection.execute(
                "SELECT pg_catalog.to_regclass(%s)::oid IS NOT NULL AS table_exists, "
                "COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'INSERT') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'INSERT') OR "
                "pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'UPDATE') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'UPDATE') OR "
                "pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'DELETE') OR pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'TRUNCATE') OR "
                "pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'REFERENCES') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'REFERENCES') OR "
                "pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass(%s)::oid,'TRIGGER'),false) AS can_write",
                ("gregale_mcp_task_worker_handlers",) * 10,
            ).fetchone()
        if worker_table is None or not worker_table["table_exists"] or worker_table["can_write"]:
            raise ValueError("MCP task observer requires read-only PostgreSQL access")

    def _owner_hashes(self, identity):
        if not getattr(identity, "subject", ""):
            principal = "open"
        else:
            principal = json.dumps(
                [identity.resource, identity.subject, identity.client_id],
                ensure_ascii=False,
                separators=(",", ":"),
            )
        principal_bytes = principal.encode("utf-8")
        return [hmac.new(key["owner_hash_key"], principal_bytes, hashlib.sha256).digest() for key in self.owner_keys]

    def _aad(self, task_id, field):
        return f"gregale-mcp-task:v1:{self.namespace}:{task_id}:{field}".encode("utf-8")

    def _encrypt(self, value, task_id, field, limit):
        plain = json.dumps(value, ensure_ascii=True, separators=(",", ":"), allow_nan=False).encode("utf-8")
        if len(plain) > limit:
            raise ValueError("MCP task JSON payload is too large")
        return self._encrypt_payload(plain, task_id, field)

    def _encrypt_payload(self, plain, task_id, field):
        key_id = self.active_owner_key_id.encode("ascii")
        nonce = os.urandom(12)
        aad=self._aad(task_id,field)
        if self.active_owner_key_id in self._main_payload_keys:
            aad+=b":key:"+key_id
        sealed = self._aesgcm(self.payload_keys[self.active_owner_key_id]).encrypt(nonce, plain, aad)
        if self.payload_encryption_version == 1:
            return b"\x01" + nonce + sealed[-16:] + sealed[:-16]
        return b"\x02" + bytes([len(key_id)]) + key_id + nonce + sealed[-16:] + sealed[:-16]

    def _decrypt_payload(self, value, task_id, field):
        if not isinstance(value, bytes) or len(value) < 30:
            raise ValueError("invalid encrypted MCP task payload")
        if value[0] == 1:
            offset = 1
            candidates = [(key["payload_key"],self._aad(task_id,field)) for key in self.owner_keys]
        elif value[0] == 2:
            key_id_length = value[1]
            if not 1 <= key_id_length <= 64 or len(value) < 2 + key_id_length + 28:
                raise ValueError("invalid encrypted MCP task payload")
            key_id = value[2:2 + key_id_length].decode("ascii", errors="strict")
            if not TASK_KEY_ID_PATTERN.fullmatch(key_id) or key_id not in self.payload_keys:
                raise ValueError("unknown MCP task payload key ID")
            offset = 2 + key_id_length
            candidates = [(self.payload_keys[key_id], self._aad(task_id,field)+(b":key:"+key_id.encode() if key_id in self._main_payload_keys else b""))]
            legacy=getattr(self,"_legacy_payload_keys",{}).get(key_id)
            if legacy is not None:
                candidates.append((legacy,self._aad(task_id,field)))
        else:
            raise ValueError("invalid encrypted MCP task payload")
        if len(value) < offset + 28:
            raise ValueError("invalid encrypted MCP task payload")
        nonce = value[offset:offset + 12]
        sealed = value[offset + 28:] + value[offset + 12:offset + 28]
        last_error = None
        for key,aad in candidates:
            try:
                return self._aesgcm(key).decrypt(nonce, sealed, aad)
            except Exception as exc:
                last_error = exc
        raise ValueError("could not decrypt MCP task payload with configured keys") from last_error

    def _decrypt(self, value, task_id, field):
        return json.loads(self._decrypt_payload(value, task_id, field))

    def create(self, tool_name, args, identity, client_capabilities=None):
        if self._stopping.is_set():
            raise ValueError("MCP task runtime is draining")
        handler = self._task_handlers.get(tool_name)
        if handler is None:
            raise ValueError("MCP task tool is not registered")
        if not isinstance(args, dict):
            raise ValueError("MCP task arguments must be an object")
        handler["validate"](args)
        task_id = str(uuid.uuid4())
        encrypted = self._encrypt(args, task_id, "arguments", 256 * 1024)
        owners = self._owner_hashes(identity)
        owner = owners[0]
        input_methods = _input_methods_for(client_capabilities)
        with self.pool.connection() as connection:
            with connection.transaction():
                connection.execute("SELECT pg_advisory_xact_lock(hashtextextended(%s,0))", (f"gregale_mcp_tasks:admission:{self.namespace}",))
                counts = connection.execute(
                    "SELECT COUNT(*)::int AS total, COUNT(*) FILTER (WHERE owner_hash=ANY(%s::bytea[]))::int AS owned FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp() AND status IN ('queued','running','input_required')",
                    (owners, self.namespace),
                ).fetchone()
                if counts["total"] >= self.max_outstanding or counts["owned"] >= self.max_per_owner:
                    raise ValueError("MCP task queue capacity reached; retry after outstanding tasks finish")
                row = connection.execute(
                    "INSERT INTO gregale_mcp_tasks(namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,input_methods,status,expires_at) VALUES(%s,%s,%s,%s,%s,%s,%s,'queued',clock_timestamp()+(%s * interval '1 millisecond')) RETURNING created_at,updated_at,expires_at",
                    (self.namespace, task_id, owner, tool_name, handler["version"], encrypted, input_methods, self.ttl_ms),
                ).fetchone()
        return {
            "resultType": "task", "taskId": task_id, "status": "working",
            "statusMessage": "Task accepted for processing.",
            "createdAt": _timestamp(row["created_at"]), "lastUpdatedAt": _timestamp(row["updated_at"]),
            "ttlMs": int((row["expires_at"] - row["created_at"]).total_seconds() * 1000),
            "pollIntervalMs": self.poll_interval_ms,
        }

    def _read_task(self, task_id, identity):
        if not isinstance(task_id, str) or not TASK_ID_PATTERN.fullmatch(task_id):
            raise TaskNotFound()
        with self.pool.connection() as connection:
            row = connection.execute(
                "SELECT task_id::text AS task_id,tool_name,handler_version,status,created_at,updated_at,expires_at,attempt_count,lease_token::text AS lease_token,cancel_requested_at,arguments_encrypted,result_encrypted,error_encrypted,input_state_encrypted,input_methods FROM gregale_mcp_tasks WHERE namespace=%s AND task_id=%s AND owner_hash=ANY(%s::bytea[]) AND expires_at>clock_timestamp()",
                (self.namespace, task_id, self._owner_hashes(identity)),
            ).fetchone()
        if row is None:
            raise TaskNotFound()
        row["arguments"] = self._decrypt(row.pop("arguments_encrypted"), task_id, "arguments")
        row["input_methods"] = row.get("input_methods") or []
        encrypted_input_state = row.pop("input_state_encrypted")
        row["input_requests"] = {}
        if encrypted_input_state is not None:
            state = self._decrypt(encrypted_input_state, task_id, "input-state")
            requests = state.get("requests", {})
            responses = state.get("responses", {})
            row["input_requests"] = {key: value for key, value in requests.items() if key not in responses}
        for column, field in (("result_encrypted", "result"), ("error_encrypted", "error")):
            encrypted = row.pop(column)
            if encrypted is not None:
                row[field] = self._decrypt(encrypted, task_id, field)
        return row

    def _wire(self, task):
        status = "working" if task["status"] in {"queued", "running"} else task["status"]
        value = {
            "resultType": "complete", "taskId": task["task_id"], "status": status,
            "createdAt": _timestamp(task["created_at"]), "lastUpdatedAt": _timestamp(task["updated_at"]),
            "ttlMs": int((task["expires_at"] - task["created_at"]).total_seconds() * 1000),
            "pollIntervalMs": self.poll_interval_ms,
        }
        if status == "completed":
            value["result"] = task.get("result")
        if status == "failed":
            value["error"] = task.get("error") or {"code": -32603, "message": "Task execution failed"}
        if status == "input_required":
            value["inputRequests"] = task.get("input_requests") or {}
        return value

    def get(self, task_id, identity, authenticator, scopes):
        task = self._read_task(task_id, identity)
        if not authenticator.can_access_tool(task["tool_name"], scopes):
            raise TaskNotFound()
        return self._wire(task), task

    def cancel(self, task_id, identity, authenticator, scopes):
        task = self._read_task(task_id, identity)
        if not authenticator.can_access_tool(task["tool_name"], scopes):
            raise TaskNotFound()
        with self.pool.connection() as connection:
            result = connection.execute(
                "UPDATE gregale_mcp_tasks SET status=CASE WHEN status IN ('queued','input_required') THEN 'cancelled' ELSE status END,cancel_requested_at=CASE WHEN status IN ('queued','running','input_required') THEN COALESCE(cancel_requested_at,clock_timestamp()) ELSE cancel_requested_at END,updated_at=CASE WHEN status IN ('queued','running','input_required') THEN clock_timestamp() ELSE updated_at END,lease_token=CASE WHEN status IN ('queued','input_required') THEN NULL ELSE lease_token END,lease_expires_at=CASE WHEN status IN ('queued','input_required') THEN NULL ELSE lease_expires_at END WHERE namespace=%s AND task_id=%s AND owner_hash=ANY(%s::bytea[]) AND expires_at>clock_timestamp()",
                (self.namespace, task_id, self._owner_hashes(identity)),
            )
            if result.rowcount == 0:
                raise TaskNotFound()

    def update_inputs(self, task_id, input_responses, identity, authenticator, scopes):
        task = self._read_task(task_id, identity)
        if not authenticator.can_access_tool(task["tool_name"], scopes):
            raise TaskNotFound()
        if not isinstance(input_responses, dict):
            raise ValueError("MCP task input responses must be an object")
        if len(input_responses) > 64 or any(not isinstance(key, str) or not key or _task_string_length(key) > 128 for key in input_responses):
            raise ValueError("Invalid MCP task input response keys")
        encoded_responses = json.dumps(input_responses, ensure_ascii=True, separators=(",", ":"), allow_nan=False).encode("utf-8")
        if len(encoded_responses) > MAX_INPUT_STATE_BYTES:
            raise ValueError("MCP task input responses are too large")
        with self.pool.connection() as connection:
            with connection.transaction():
                row = connection.execute(
                    "SELECT status,input_state_encrypted FROM gregale_mcp_tasks WHERE namespace=%s AND task_id=%s AND owner_hash=ANY(%s::bytea[]) AND expires_at>clock_timestamp() FOR UPDATE",
                    (self.namespace, task_id, self._owner_hashes(identity)),
                ).fetchone()
                if row is None:
                    raise TaskNotFound()
                if row["status"] != "input_required" or row["input_state_encrypted"] is None:
                    return
                state = self._decrypt(row["input_state_encrypted"], task_id, "input-state")
                requests = state.get("requests", {})
                responses = state.setdefault("responses", {})
                changed = False
                for key, response in input_responses.items():
                    if key not in requests or key in responses:
                        continue
                    responses[key] = response
                    changed = True
                if not changed:
                    return
                pending = any(key not in responses for key in requests)
                encrypted = self._encrypt(state, task_id, "input-state", MAX_INPUT_STATE_BYTES)
                connection.execute(
                    "UPDATE gregale_mcp_tasks SET input_state_encrypted=%s,status=CASE WHEN %s THEN 'input_required' ELSE 'queued' END,resume_pending=NOT %s,updated_at=clock_timestamp() WHERE namespace=%s AND task_id=%s AND status='input_required'",
                    (encrypted, pending, pending, self.namespace, task_id),
                )

    def _claim(self):
        supported = sorted(self._task_executors)
        if not supported:
            return None
        handler_filter = " OR ".join("(tool_name=%s AND handler_version=%s)" for _ in supported)
        handler_params = tuple(value for pair in supported for value in pair)
        with self.pool.connection() as connection:
            with connection.transaction():
                connection.execute(
                    "UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE 'failed' END,updated_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE namespace=%s AND status='running' AND lease_expires_at<=clock_timestamp() AND attempt_count>=3",
                    (self.namespace,),
                )
                row = connection.execute(
                    f"WITH candidate AS (SELECT namespace,task_id FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp() AND (attempt_count<3 OR (status='queued' AND resume_pending AND attempt_count=3)) AND ({handler_filter}) AND (status='queued' OR (status='running' AND lease_expires_at<=clock_timestamp())) ORDER BY created_at,task_id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE gregale_mcp_tasks AS task SET status='running',attempt_count=task.attempt_count+CASE WHEN task.resume_pending THEN 0 ELSE 1 END,resume_pending=false,lease_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '30 seconds',updated_at=clock_timestamp() FROM candidate WHERE task.namespace=candidate.namespace AND task.task_id=candidate.task_id RETURNING task.task_id::text AS task_id,task.tool_name,task.handler_version,task.lease_token::text AS lease_token,task.arguments_encrypted,task.input_methods",
                    (self.namespace, *handler_params),
                ).fetchone()
        if row is not None:
            row["arguments"] = self._decrypt(row.pop("arguments_encrypted"), row["task_id"], "arguments")
            row["input_methods"] = row.get("input_methods") or []
        return row

    def _request_inputs(self, task, requests):
        if not isinstance(requests, dict) or not 1 <= len(requests) <= 64:
            raise ValueError("MCP task input request batches must contain between 1 and 64 requests")
        for key, request in requests.items():
            if not isinstance(key, str) or not key or _task_string_length(key) > 128:
                raise ValueError("MCP task input request keys must contain between 1 and 128 characters")
            if not isinstance(request, dict) or not isinstance(request.get("params"), dict):
                raise ValueError("MCP task input requests must use a supported server-to-client MCP method")
            method = request.get("method")
            if method not in {"elicitation/create", "sampling/createMessage", "roots/list"}:
                raise ValueError("MCP task input requests must use a supported server-to-client MCP method")
            if method == "elicitation/create" and request["params"].get("mode") not in {"form", "url"}:
                raise ValueError("MCP elicitation task requests must declare form or url mode")
            capability = method + (":" + request["params"]["mode"] if method == "elicitation/create" else "")
            if capability not in task.get("input_methods", []):
                raise ValueError(f"MCP task client did not declare support for {method}")

        with self.pool.connection() as connection:
            with connection.transaction():
                row = connection.execute(
                    "SELECT input_state_encrypted,cancel_requested_at FROM gregale_mcp_tasks WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running' FOR UPDATE",
                    (self.namespace, task["task_id"], task["lease_token"]),
                ).fetchone()
                if row is None:
                    raise TaskInputLeaseLost()
                if row["cancel_requested_at"] is not None:
                    raise TaskInputCancelled()
                state = {"requests": {}, "responses": {}}
                if row["input_state_encrypted"] is not None:
                    state = self._decrypt(row["input_state_encrypted"], task["task_id"], "input-state")
                stored_requests = state.setdefault("requests", {})
                stored_responses = state.setdefault("responses", {})
                for key, request in requests.items():
                    if key in stored_requests and stored_requests[key] != request:
                        raise ValueError("MCP task input request keys cannot be reused for different requests")
                    stored_requests.setdefault(key, request)
                if len(stored_requests) > 64:
                    raise ValueError("MCP task exceeds the 64 unique input request limit")
                responses = {key: stored_responses[key] for key in requests if key in stored_responses}
                ready = len(responses) == len(requests)
                if not ready:
                    encrypted = self._encrypt(state, task["task_id"], "input-state", MAX_INPUT_STATE_BYTES)
                    connection.execute(
                        "UPDATE gregale_mcp_tasks SET status='input_required',input_state_encrypted=%s,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running'",
                        (encrypted, self.namespace, task["task_id"], task["lease_token"]),
                    )
                return responses, ready

    def _heartbeat(self, task_id, lease_token):
        with self.pool.connection() as connection:
            row = connection.execute(
                "UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()+interval '30 seconds' WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running' RETURNING cancel_requested_at IS NOT NULL AS cancelled",
                (self.namespace, task_id, lease_token),
            ).fetchone()
        return row

    def _finish(self, task, status, value=None):
        if status == "completed":
            encrypted = self._encrypt(value, task["task_id"], "result", 1024 * 1024)
            sql = "UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NULL THEN 'completed' ELSE 'cancelled' END,result_encrypted=CASE WHEN cancel_requested_at IS NULL THEN %s ELSE NULL END,error_encrypted=NULL,updated_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running'"
            args = (encrypted, self.namespace, task["task_id"], task["lease_token"])
        elif status == "cancelled":
            sql = "UPDATE gregale_mcp_tasks SET status='cancelled',result_encrypted=NULL,error_encrypted=NULL,updated_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running'"
            args = (self.namespace, task["task_id"], task["lease_token"])
        else:
            encrypted = self._encrypt({"code": -32603, "message": "Task execution failed"}, task["task_id"], "error", 1024 * 1024)
            sql = "UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NULL THEN 'failed' ELSE 'cancelled' END,error_encrypted=CASE WHEN cancel_requested_at IS NULL THEN %s ELSE NULL END,result_encrypted=NULL,updated_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE namespace=%s AND task_id=%s AND lease_token=%s AND status='running'"
            args = (encrypted, self.namespace, task["task_id"], task["lease_token"])
        with self.pool.connection() as connection:
            connection.execute(sql, args)

    def _run_task(self, task, cancelled):
        if self._drain_expired.is_set():
            return
        stop_heartbeat = threading.Event()
        lease_lost = threading.Event()

        handler = self._task_executors.get((task["tool_name"], task["handler_version"]))
        if handler is None:
            self._finish(task, "failed")
            return

        def heartbeat_loop():
            while not stop_heartbeat.wait(5) and not self._drain_expired.is_set():
                try:
                    state = self._heartbeat(task["task_id"], task["lease_token"])
                    if state is None:
                        lease_lost.set()
                        cancelled.set()
                        return
                    if state["cancelled"]:
                        cancelled.set()
                        return
                except Exception:
                    continue

        heartbeat = threading.Thread(target=heartbeat_loop, daemon=True)
        heartbeat.start()

        def request_inputs(requests):
            responses, ready = self._request_inputs(task, requests)
            if not ready:
                raise TaskInputRequired()
            return responses

        try:
            result = handler(task["arguments"], TaskExecutionContext(task["task_id"], cancelled, request_inputs))
            if lease_lost.is_set() or self._drain_expired.is_set():
                return
            if cancelled.is_set():
                self._finish(task, "cancelled")
            elif not isinstance(result, dict) or not isinstance(result.get("content"), list):
                self._finish(task, "failed")
            else:
                self._finish(task, "completed", result)
        except TaskInputRequired:
            return
        except TaskInputLeaseLost:
            return
        except TaskInputCancelled:
            if not lease_lost.is_set() and not self._drain_expired.is_set():
                self._finish(task, "cancelled")
        except Exception:
            if lease_lost.is_set() or self._drain_expired.is_set():
                return
            try:
                self._finish(task, "cancelled" if cancelled.is_set() else "failed")
            except Exception:
                pass
        finally:
            stop_heartbeat.set()
            heartbeat.join(timeout=1)

    def _rewrap_batch(self, limit=64):
        if self.payload_encryption_version != 2:
            return 0
        active_key_id = self.active_owner_key_id.encode("ascii")
        columns = ("arguments_encrypted", "result_encrypted", "error_encrypted", "input_state_encrypted")
        checks = []
        for column in columns:
            checks.append(
                f"({column} IS NOT NULL AND CASE "
                f"WHEN octet_length({column}) < 2 THEN TRUE "
                f"WHEN get_byte({column},0) <> 2 THEN TRUE "
                f"WHEN get_byte({column},1) = 0 OR get_byte({column},1) > 64 THEN TRUE "
                f"WHEN octet_length({column}) < 2 + get_byte({column},1) + 28 THEN TRUE "
                f"ELSE substring({column} FROM 3 FOR get_byte({column},1)) <> %s::bytea END)"
            )
        query = (
            "SELECT task_id::text AS task_id,arguments_encrypted,result_encrypted,error_encrypted,input_state_encrypted "
            "FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at>clock_timestamp() AND ("
            + " OR ".join(checks)
            + ") ORDER BY created_at LIMIT %s FOR UPDATE SKIP LOCKED"
        )
        with self.pool.connection() as connection:
            with connection.transaction():
                connection.execute("SELECT set_config('gregale.mcp_task_rewrap','on',true)")
                rows = connection.execute(query, (self.namespace, active_key_id, active_key_id, active_key_id, active_key_id, limit)).fetchall()
                for row in rows:
                    task_id = row["task_id"]

                    def rotate(value, field, max_bytes):
                        if value is None:
                            return None
                        plain = self._decrypt_payload(value, task_id, field)
                        if len(plain) > max_bytes:
                            raise ValueError("MCP task payload exceeds its configured size limit")
                        return self._encrypt_payload(plain, task_id, field)

                    encrypted = (
                        rotate(row["arguments_encrypted"], "arguments", 256 * 1024),
                        rotate(row["result_encrypted"], "result", 1024 * 1024),
                        rotate(row["error_encrypted"], "error", 1024 * 1024),
                        rotate(row["input_state_encrypted"], "input-state", MAX_INPUT_STATE_BYTES),
                    )
                    connection.execute(
                        "UPDATE gregale_mcp_tasks SET arguments_encrypted=%s,result_encrypted=%s,error_encrypted=%s,input_state_encrypted=%s WHERE namespace=%s AND task_id=%s",
                        (*encrypted, self.namespace, task_id),
                    )
        return len(rows)

    def _rewrap_loop(self):
        last_error_log = 0
        while not self._stopping.is_set():
            try:
                self._rewrap_batch()
            except Exception:
                now = time.monotonic()
                if now - last_error_log >= 60:
                    logging.getLogger(__name__).warning("MCP task key rewrap batch failed")
                    last_error_log = now
            self._stopping.wait(2)

    def _heartbeat_worker_capabilities(self):
        if not self._task_executors:
            return
        handlers = list(self._task_executors)
        tool_names = [name for name, _ in handlers]
        handler_versions = [version for _, version in handlers]
        with self.pool.connection() as connection:
            connection.execute(
                "INSERT INTO gregale_mcp_task_worker_handlers(namespace,worker_id,tool_name,handler_version,deployment_id,heartbeat_at) "
                "SELECT %s,%s,handler.tool_name,handler.handler_version,%s,clock_timestamp() "
                "FROM unnest(%s::text[],%s::text[]) AS handler(tool_name,handler_version) "
                "ON CONFLICT (namespace,worker_id,tool_name,handler_version) "
                "DO UPDATE SET deployment_id=EXCLUDED.deployment_id,heartbeat_at=EXCLUDED.heartbeat_at",
                (self.namespace, self._worker_id, self._deployment_id, tool_names, handler_versions),
            )

    def _remove_worker_capabilities(self):
        with self.pool.connection() as connection:
            connection.execute(
                "DELETE FROM gregale_mcp_task_worker_handlers WHERE namespace=%s AND worker_id=%s",
                (self.namespace, self._worker_id),
            )

    def _worker_capability_heartbeat_loop(self):
        last_cleanup = 0.0
        last_error_log = 0.0
        while not self._stopping.is_set():
            try:
                self._heartbeat_worker_capabilities()
                now = time.monotonic()
                if now - last_cleanup >= 3600:
                    with self.pool.connection() as connection:
                        connection.execute(
                            "DELETE FROM gregale_mcp_task_worker_handlers "
                            "WHERE namespace=%s AND heartbeat_at<clock_timestamp()-interval '1 day'",
                            (self.namespace,),
                        )
                    last_cleanup = now
            except Exception:
                now = time.monotonic()
                if now - last_error_log >= 60:
                    logging.getLogger(__name__).warning(
                        '{"event":"mcp_task_worker_capability_heartbeat_failed"}'
                    )
                    last_error_log = now
            if self._stopping.wait(10):
                break
        try:
            self._remove_worker_capabilities()
        except Exception:
            logging.getLogger(__name__).warning(
                '{"event":"mcp_task_worker_capability_cleanup_failed"}'
            )

    def _worker_loop(self):
        last_cleanup = 0
        def execute_task(task, cancelled):
            try:
                self._run_task(task, cancelled)
            finally:
                with self._drain_lock:
                    self._active_tasks.pop(task["task_id"], None)
        while not self._stopping.is_set():
            try:
                if time.monotonic() - last_cleanup >= 60:
                    with self.pool.connection() as connection:
                        connection.execute("DELETE FROM gregale_mcp_tasks WHERE namespace=%s AND task_id IN (SELECT task_id FROM gregale_mcp_tasks WHERE namespace=%s AND expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 250)", (self.namespace, self.namespace))
                    last_cleanup = time.monotonic()
                while not self._stopping.is_set():
                    with self._drain_lock:
                        if len(self._active_tasks) >= self.concurrency:
                            break
                    task = self._claim()
                    if task is None:
                        break
                    cancelled = threading.Event()
                    with self._drain_lock:
                        stopping = self._stopping.is_set()
                        if not stopping:
                            self._active_tasks[task["task_id"]] = (task, cancelled)
                    if stopping:
                        self._release_drained_task(task)
                        break
                    # Uncooperative user handlers must not hold process exit
                    # beyond the deadline (ThreadPoolExecutor uses non-daemon threads).
                    threading.Thread(target=execute_task, args=(task, cancelled), daemon=True, name="mcp-task").start()
            except Exception:
                pass
            self._stopping.wait(self.poll_interval_ms / 1000)
        while True:
            with self._drain_lock:
                if not self._active_tasks or self._drain_expired.is_set():
                    return
            time.sleep(0.01)


def task_resource_template():
    return {
        "uriTemplate": "task://tasks/{taskId}", "name": "task", "title": "MCP task",
        "description": "Read the current state and result of a task owned by this caller.",
        "mimeType": "application/json",
    }


def task_id_from_uri(uri):
    prefix = "task://tasks/"
    if isinstance(uri, str) and uri.startswith(prefix):
        task_id = uri[len(prefix):]
        if TASK_ID_PATTERN.fullmatch(task_id):
            return task_id
    return None


def _validate_build_report_task(arguments):
    report = arguments.get("report")
    steps = arguments.get("steps")
    allowed = {"report", "steps", "request_approval"}
    valid_steps = (not isinstance(steps, bool) and isinstance(steps, (int, float))
                   and math.isfinite(steps) and int(steps) == steps and 1 <= steps <= 20)
    if (set(arguments) not in ({"report", "steps"}, allowed)
            or not isinstance(report, str) or not report.strip() or _task_string_length(report) > 80
            or not valid_steps
            or "request_approval" in arguments and not isinstance(arguments["request_approval"], bool)):
        raise ValueError("report must be 1 to 80 characters and steps must be an integer from 1 to 20")
    arguments["steps"] = int(steps)


def _execute_legacy_build_report(arguments, context):
    return _execute_build_report_steps(arguments, context)


def _execute_build_report(arguments, context):
    if arguments.get("request_approval") is True:
        approval = context.request_input("approval", {
            "method": "elicitation/create",
            "params": {
                "mode": "form",
                "message": "Approve generating this report?",
                "requestedSchema": {
                    "type": "object",
                    "properties": {"approved": {"type": "boolean", "title": "Approve report generation"}},
                    "required": ["approved"],
                },
            },
        })
        content = approval.get("content", {}) if isinstance(approval, dict) else {}
        approved = isinstance(approval, dict) and approval.get("action") == "accept" and isinstance(content, dict) and content.get("approved") is True
        if not approved:
            return {"content": [{"type": "text", "text": "Report was not approved."}]}
    return _execute_build_report_steps(arguments, context)


def _execute_build_report_steps(arguments, context):
    for _ in range(arguments["steps"]):
        if context.cancelled.is_set():
            raise RuntimeError("MCP task was cancelled")
        time.sleep(0.2)
    report = arguments["report"]
    steps = arguments["steps"]
    return {"content": [{"type": "text", "text": f"Report {report} is ready after {steps} steps."}]}


register_task_handler(
    "build_report",
    version="2",
    previous_versions={"1": _execute_legacy_build_report},
    validate=_validate_build_report_task,
    execute=_execute_build_report,
)


def migrate_task_schema(pool):
    statements = [
        CREATE_SCHEMA,
        CREATE_WORKER_HANDLERS_SCHEMA,
        "ALTER TABLE gregale_mcp_task_worker_handlers ADD COLUMN IF NOT EXISTS deployment_id text NOT NULL DEFAULT ''",
        "CREATE INDEX IF NOT EXISTS gregale_mcp_task_worker_handlers_live_idx ON gregale_mcp_task_worker_handlers(namespace,tool_name,handler_version,heartbeat_at DESC)",
        "CREATE INDEX IF NOT EXISTS gregale_mcp_task_worker_handlers_stale_idx ON gregale_mcp_task_worker_handlers(namespace,heartbeat_at)",
        "ALTER TABLE gregale_mcp_tasks ADD COLUMN IF NOT EXISTS input_state_encrypted bytea, ADD COLUMN IF NOT EXISTS input_methods text[] NOT NULL DEFAULT '{}', ADD COLUMN IF NOT EXISTS resume_pending boolean NOT NULL DEFAULT false",
        "ALTER TABLE gregale_mcp_tasks DROP CONSTRAINT IF EXISTS gregale_mcp_tasks_status_check",
        "ALTER TABLE gregale_mcp_tasks ADD CONSTRAINT gregale_mcp_tasks_status_check CHECK (status IN ('queued','running','input_required','completed','cancelled','failed'))",
        "ALTER TABLE gregale_mcp_tasks DROP CONSTRAINT IF EXISTS gregale_mcp_tasks_input_state_check",
        "ALTER TABLE gregale_mcp_tasks ADD CONSTRAINT gregale_mcp_tasks_input_state_check CHECK (status <> 'input_required' OR input_state_encrypted IS NOT NULL)",
        "CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_queue_idx ON gregale_mcp_tasks(namespace,created_at,task_id) WHERE status IN ('queued','running')",
        "CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_admission_idx ON gregale_mcp_tasks(namespace,owner_hash,expires_at) WHERE status IN ('queued','running','input_required')",
        CREATE_TASK_NOTIFY_FUNCTION,
        CREATE_TASK_NOTIFY_TRIGGER,
    ]
    with pool.connection() as connection:
        with connection.transaction():
            connection.execute("SELECT pg_advisory_xact_lock(hashtextextended(%s,0))", ("gregale_mcp_tasks",))
            check_migration_schema(connection)
            installed = connection.execute("SELECT to_regclass('gregale_mcp_task_starter_schema') IS NOT NULL AS installed").fetchone()["installed"]
            if installed:
                row = connection.execute("SELECT version FROM gregale_mcp_task_starter_schema WHERE id=1").fetchone()
                if row is None or row["version"] != 1:
                    raise ValueError("unsupported MCP Task starter schema version")
            connection.execute("CREATE TABLE IF NOT EXISTS gregale_mcp_task_starter_schema (id integer PRIMARY KEY CHECK (id=1), version integer NOT NULL CHECK (version>0))")
            connection.execute("INSERT INTO gregale_mcp_task_starter_schema (id,version) VALUES (1,1) ON CONFLICT (id) DO NOTHING")
            for statement in statements:
                connection.execute(statement)


def check_migration_schema(connection):
    row = connection.execute("""SELECT NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_namespace n,
        LATERAL pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a
        WHERE n.nspname=current_schema() AND a.grantee=0 AND a.privilege_type='CREATE')
        AND current_schema() IS NOT NULL AS trusted""").fetchone()
    if not row["trusted"]:
        raise ValueError("MCP Task migrations require a schema without PUBLIC CREATE")
    connection.execute("SELECT set_config('search_path',quote_ident(current_schema()) || ',pg_catalog',true)")


def check_task_schema(pool):
    """Read installed schema state without DDL or a migration credential."""
    with pool.connection() as connection:
        row = connection.execute("SELECT version FROM gregale_mcp_task_starter_schema WHERE id=1").fetchone()
        if row is None or row["version"] != 1:
            raise ValueError("unsupported MCP Task starter schema version")
        connection.execute("""SELECT namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,status,
            result_encrypted,error_encrypted,input_state_encrypted,input_methods,created_at,updated_at,
            expires_at,attempt_count,resume_pending,lease_token,lease_expires_at,cancel_requested_at
            FROM gregale_mcp_tasks LIMIT 0""")
        connection.execute("SELECT namespace,worker_id,tool_name,handler_version,deployment_id,heartbeat_at FROM gregale_mcp_task_worker_handlers LIMIT 0")
        row = connection.execute("""SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_trigger
            WHERE tgrelid='gregale_mcp_tasks'::regclass AND tgname='gregale_mcp_tasks_notify'
            AND NOT tgisinternal AND tgenabled IN ('O','A')) AS ready""").fetchone()
        if not row["ready"]:
            raise ValueError("MCP Task notification trigger is unavailable")
