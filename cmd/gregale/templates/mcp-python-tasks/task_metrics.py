"""Publish privacy-safe MCP task queue gauges to the Gregale API."""

from urllib.error import HTTPError
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener
import json
import logging
import os
import re
import threading
import time

DEFAULT_API_URL = "https://api.gregale.dev"
APP_SLUG_PATTERN = re.compile(r"^[a-z0-9][a-z0-9-]{0,62}$")
QUEUE_GAUGES = (
    ("mcp_tasks_outstanding", "outstanding_tasks"),
    ("mcp_tasks_oldest_age_seconds", "oldest_age_seconds"),
    ("mcp_tasks_without_compatible_worker", "without_compatible_worker_tasks"),
    ("mcp_tasks_running", "running_tasks"),
    ("mcp_tasks_failed", "failed_tasks"),
    ("mcp_tasks_capacity_waiting", "capacity_waiting_tasks"),
    ("mcp_tasks_retry_waiting", "retry_waiting_tasks"),
    ("mcp_tasks_active_workers", "active_workers"),
    ("mcp_tasks_draining_workers", "draining_workers"),
    ("mcp_tasks_unsupported_handler_tasks", "unsupported_handler_tasks"),
)


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


def resolve_metrics_settings(role):
    app_slug = os.environ.get("MCP_TASKS_SCALING_APP_SLUG", "").strip()
    token = os.environ.get("MCP_TASKS_SCALING_TOKEN", "").strip()
    if not app_slug and not token:
        return None
    if not app_slug or not token:
        raise ValueError("MCP task scaling requires both MCP_TASKS_SCALING_APP_SLUG and MCP_TASKS_SCALING_TOKEN")
    if role not in {"worker", "observer"}:
        raise ValueError("MCP task scaling metrics can only be published by the worker role or observer")
    if not APP_SLUG_PATTERN.fullmatch(app_slug):
        raise ValueError("invalid MCP task scaling app slug")
    api_url = os.environ.get("GREGALE_API_URL", "").strip() or DEFAULT_API_URL
    try:
        parsed = urlsplit(api_url)
        valid = (
            parsed.scheme == "https"
            and parsed.hostname
            and parsed.username is None
            and parsed.password is None
            and not parsed.query
            and not parsed.fragment
        )
        # Accessing .port also rejects malformed ports.
        _ = parsed.port
    except ValueError:
        valid = False
    if not valid:
        raise ValueError("Gregale metrics API URL must be HTTPS without credentials, query, or fragment")
    return {"app_slug": app_slug, "token": token, "api_url": f"{parsed.scheme}://{parsed.netloc}"}


def start_metrics_publisher(runtime, settings, observer=False):
    """Start a bounded-cadence publisher; close it before closing its DB pool."""
    stopped = threading.Event()
    thread = threading.Thread(
        target=_publish_loop,
        args=(runtime, settings, observer, stopped),
        name="gregale-mcp-task-metrics",
        daemon=True,
    )
    thread.start()

    def close():
        stopped.set()
        thread.join()

    return close


def _publish_loop(runtime, settings, observer, stopped):
    opener = build_opener(_NoRedirect())
    event_name = "mcp_task_observer_health_check_failed" if observer else "mcp_task_metrics_publish_failed"
    while not stopped.is_set():
        step = "queue_metrics"
        try:
            metrics = runtime.queue_metrics()
            deadline = time.monotonic() + 5

            def remaining():
                value = deadline - time.monotonic()
                if value <= 0:
                    raise TimeoutError("Task metric publication deadline exceeded")
                return value

            for name, field in QUEUE_GAUGES:
                step = f"metric:{name}"
                _push(opener, settings, name, metrics[field], timeout=remaining())
            if observer:
                step = "compatibility_report"
                _push_compatibility_report(opener, settings, metrics["handler_compatibility"], metrics["compatibility_truncated"], timeout=remaining())
                step = "metric:mcp_tasks_observer_heartbeat"
                _push(opener, settings, "mcp_tasks_observer_heartbeat", 1, timeout=remaining())
        except Exception as error:
            # Log only the failed step and an HTTP status: exception details can
            # quote database URLs or API tokens, which are configured secrets.
            entry = {"event": event_name, "step": step}
            status = getattr(error, "status", None)
            if isinstance(status, int):
                entry["status"] = status
            logging.error(json.dumps(entry, separators=(",", ":")))
        stopped.wait(15)


class _PublishFailed(RuntimeError):
    """A refused or failed publish; carries an HTTP status and nothing else."""

    def __init__(self, message, status=None):
        super().__init__(message)
        self.status = status


def _put_json(opener, request, timeout, message):
    try:
        with opener.open(request, timeout=timeout) as response:
            status = response.status
    except HTTPError as error:
        status = error.code
        error.close()
    except Exception:
        raise _PublishFailed(message) from None
    if status != 204:
        raise _PublishFailed(message, status)


def _push(opener, settings, name, value, timeout=5):
    endpoint = (
        f"{settings['api_url']}/v1/apps/{quote(settings['app_slug'], safe='')}"
        f"/custom-metrics/{name}"
    )
    request = Request(
        endpoint,
        data=json.dumps({"value": value}, allow_nan=False, separators=(",", ":")).encode("utf-8"),
        headers={
            "Authorization": f"Bearer {settings['token']}",
            "Content-Type": "application/json",
        },
        method="PUT",
    )
    _put_json(opener, request, timeout, "Gregale custom metric push failed")


def _push_compatibility_report(opener, settings, handlers, truncated, timeout=5):
    endpoint = f"{settings['api_url']}/v1/apps/{quote(settings['app_slug'], safe='')}/mcp-task-compatibility"
    request = Request(
        endpoint,
        data=json.dumps({"handlers": handlers, "truncated": truncated}, allow_nan=False, separators=(",", ":")).encode("utf-8"),
        headers={
            "Authorization": f"Bearer {settings['token']}",
            "Content-Type": "application/json",
        },
        method="PUT",
    )
    _put_json(opener, request, timeout, "Gregale MCP task compatibility report failed")
