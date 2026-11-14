"""Publish privacy-safe MCP task queue gauges to the Gregale API."""

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
        try:
            metrics = runtime.queue_metrics()
            deadline = time.monotonic() + 5

            def remaining():
                value = deadline - time.monotonic()
                if value <= 0:
                    raise TimeoutError("Task metric publication deadline exceeded")
                return value

            for name, field in QUEUE_GAUGES:
                _push(opener, settings, name, metrics[field], timeout=remaining())
            if observer:
                _push_compatibility_report(opener, settings, metrics["handler_compatibility"], metrics["compatibility_truncated"], timeout=remaining())
                _push(opener, settings, "mcp_tasks_observer_heartbeat", 1, timeout=remaining())
        except Exception:
            # Avoid logging exception details: database URLs and API tokens are
            # configured secrets and do not belong in application logs.
            logging.error(json.dumps({"event": event_name}, separators=(",", ":")))
        stopped.wait(15)


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
    try:
        with opener.open(request, timeout=timeout) as response:
            if response.status != 204:
                raise RuntimeError("Gregale custom metric push failed")
    except Exception:
        raise RuntimeError("Gregale custom metric push failed") from None


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
    try:
        with opener.open(request, timeout=timeout) as response:
            if response.status != 204:
                raise RuntimeError("Gregale MCP task compatibility report failed")
    except Exception:
        raise RuntimeError("Gregale MCP task compatibility report failed") from None
