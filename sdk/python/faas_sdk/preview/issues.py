"""Bounded, best-effort exception reporting independent of trace sampling."""

from __future__ import annotations

import inspect
import json
import sys
import threading
import time
import traceback
import uuid
from collections import deque
from datetime import UTC, datetime
from functools import wraps
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _bounded(value: str, size: int) -> str:
    return value.encode("utf-8", errors="replace")[:size].decode("utf-8", errors="ignore")


_ISSUE_TAG_KEYS = (
    "component", "operation", "feature_flag", "feature_variant", "dependency", "region",
    "job_type", "workflow", "state", "error_class", "release_channel", "runtime",
)


def _copy_operational_context(event: dict[str, Any], context: dict[str, Any]) -> None:
    """Copy only bounded timeline fields and allowlisted operational tags."""
    raw_dropped = context.get("breadcrumbs_dropped", 0)
    dropped = min(2147483647, max(0, raw_dropped)) if type(raw_dropped) is int else 0
    entries = context.get("breadcrumbs")
    if isinstance(entries, (list, tuple)):
        dropped = min(2147483647, dropped + max(0, len(entries) - 16))
        copied = []
        for entry in entries[-16:]:
            if not isinstance(entry, dict):
                dropped = min(2147483647, dropped + 1)
                continue
            stamp = entry.get("occurred_at")
            if isinstance(stamp, datetime) and stamp.tzinfo is not None:
                stamp = stamp.isoformat()
            category, level, message = entry.get("category", "custom"), entry.get("level", "info"), entry.get("message")
            if (
                not isinstance(stamp, str) or not stamp or len(stamp) > 64 or "\0" in stamp
                or category not in ("http", "db", "cache", "workflow", "log", "custom")
                or level not in ("debug", "info", "warning", "error")
                or not isinstance(message, str) or "\0" in message
            ):
                dropped = min(2147483647, dropped + 1)
                continue
            copied.append({"occurred_at": stamp, "category": category, "level": level, "message": _bounded(message, 256)})
        if copied:
            event["breadcrumbs"] = copied
    if dropped:
        event["breadcrumbs_dropped"] = dropped
    tags = context.get("tags")
    if isinstance(tags, dict):
        copied_tags = {key: _bounded(tags[key], 128) for key in _ISSUE_TAG_KEYS
                       if isinstance(tags.get(key), str) and "\0" not in tags[key]}
        if copied_tags:
            event["tags"] = copied_tags


class IssueReporter:
    """Report exceptions with a deployment-bound issue token, never an owner key.

    Reporting does not swallow exceptions. Local queues are not durable across
    process crashes. Inspect stats() and close() to detect incomplete delivery.
    """

    def __init__(
        self,
        base_url: str,
        app: str,
        token: str,
        *,
        max_queue: int = 100,
        timeout: float = 2.0,
        grouping_strategy: str = "legacy",
        symbolication_strategy: str | None = None,
    ):
        parsed = urlsplit(base_url)
        if (
            parsed.scheme not in ("http", "https")
            or not parsed.netloc
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
        ):
            raise ValueError("Issue base URL must be an HTTP(S) origin without credentials")
        if not token.startswith("g_issue_"):
            raise ValueError("A Gregale issue ingest token is required")
        if grouping_strategy not in ("legacy", "failure-site-v1", "cause-chain-v1"):
            raise ValueError("Invalid issue grouping strategy")
        if symbolication_strategy is not None and (
            symbolication_strategy != "deployment-source-maps-v1"
            or grouping_strategy not in ("failure-site-v1", "cause-chain-v1")
        ):
            raise ValueError("Server symbolication requires failure-site-v1 or cause-chain-v1")
        self._symbolication_strategy = symbolication_strategy
        self._grouping_strategy = grouping_strategy
        self._url = f"{parsed.scheme}://{parsed.netloc}/v1/apps/{quote(app, safe='')}/issue-events"
        self._token = token
        from . import __version__  # resolved once the package has finished importing

        # Cloudflare's browser integrity check answers urllib's default
        # "Python-urllib/3.x" User-Agent on api.gregale.dev with 403 (error
        # 1010), which _send treats as permanent, so every event was dropped.
        self._user_agent = f"gregale-python-sdk/{__version__} (issues)"
        self._max_queue = max(1, min(1000, max_queue))
        self._timeout = max(0.1, min(10.0, timeout))
        self._opener = build_opener(_NoRedirect())
        self._queue: deque[dict[str, Any]] = deque()
        self._cv = threading.Condition()
        self._stopped = False
        self._closing = False
        self._accepted = 0
        self._dropped = 0
        self._thread = threading.Thread(target=self._run, name="gregale-issues", daemon=True)
        self._thread.start()

    def capture_exception(self, error: BaseException, **context: Any) -> str | None:
        try:
            return self._capture(error, context)
        except Exception:
            with self._cv:
                self._dropped += 1
            return None

    def _capture(self, error: BaseException, context: dict[str, Any]) -> str | None:
        frames = traceback.extract_tb(error.__traceback__)[-32:]
        event: dict[str, Any] = {
            "event_id": str(uuid.uuid4()),
            "occurred_at": datetime.now(UTC).isoformat(),
            "exception_type": _bounded(f"{type(error).__module__}.{type(error).__name__}", 256),
            "message": _bounded(str(error), 2048),
            "stack_trace": _bounded(
                "".join(traceback.format_exception(type(error), error, error.__traceback__,
                                                   chain=self._grouping_strategy != "cause-chain-v1")), 16384
            ),
            "frames": [
                {
                    "file": _bounded(f.filename, 512),
                    "function": _bounded(f.name, 512),
                    "line": f.lineno,
                    "in_app": "site-packages" not in f.filename,
                }
                for f in frames
            ],
        }
        if self._symbolication_strategy is not None:
            event["symbolication_strategy"] = self._symbolication_strategy
        _copy_operational_context(event, context)
        if self._grouping_strategy in ("failure-site-v1", "cause-chain-v1"):
            event["grouping_strategy"] = self._grouping_strategy
            event["frame_order"] = "oldest-first"
        if self._grouping_strategy == "cause-chain-v1":
            causes = []
            seen = {id(error)}
            current = error
            try:
                while True:
                    if isinstance(current, BaseExceptionGroup):
                        event["causes_incomplete"] = True
                        break
                    cause = current.__cause__
                    if cause is None and not current.__suppress_context__:
                        cause = current.__context__
                    if cause is None:
                        break
                    if id(cause) in seen or len(causes) == 8:
                        event["causes_incomplete"] = True
                        break
                    seen.add(id(cause))
                    frames = traceback.extract_tb(cause.__traceback__)[-32:]
                    causes.append(
                        {
                            "exception_type": _bounded(f"{type(cause).__module__}.{type(cause).__name__}", 256),
                            "message": _bounded(str(cause), 2048),
                            "frames": [
                                {
                                    "file": _bounded(f.filename, 512),
                                    "function": _bounded(f.name, 512),
                                    "line": f.lineno,
                                    "in_app": "site-packages" not in f.filename,
                                }
                                for f in frames
                            ],
                            "frame_order": "oldest-first",
                        }
                    )
                    current = cause
            except Exception:
                event["causes_incomplete"] = True
            event["causes"] = causes
        for key in (
            "trace_id",
            "span_id",
            "request_id",
            "invocation_id",
            "route",
            "source_kind",
            "error_code",
            "fingerprint_override",
        ):
            if key in context and isinstance(context[key], str):
                event[key] = _bounded(context[key], 256)
        if self._grouping_strategy == "cause-chain-v1":
            while event["causes"] and len(json.dumps(event, ensure_ascii=False).encode()) > 65536:
                event["causes"].pop()
                event["causes_incomplete"] = True
        with self._cv:
            if self._closing or self._stopped or len(self._queue) >= self._max_queue:
                self._dropped += 1
                return None
            self._queue.append(event)
            self._cv.notify_all()
        return event["event_id"]

    def _send(self, event: dict[str, Any]) -> bool | None:
        request = Request(
            self._url,
            data=json.dumps(event, ensure_ascii=False).encode(),
            method="POST",
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {self._token}", "User-Agent": self._user_agent},
        )
        try:
            with self._opener.open(request, timeout=self._timeout) as response:
                return response.status == 202
        except HTTPError as error:
            status = error.code
            error.close()
            return None if status == 429 or status >= 500 else False
        except (URLError, TimeoutError, OSError):
            return None

    def _run(self) -> None:
        while True:
            with self._cv:
                self._cv.wait_for(lambda: bool(self._queue) or self._stopped)
                if self._stopped:
                    return
                event = self._queue[0]
            outcome = self._send(event)
            with self._cv:
                if outcome is not None:
                    self._queue.popleft()
                    if outcome:
                        self._accepted += 1
                    else:
                        self._dropped += 1
                    self._cv.notify_all()
                else:
                    self._cv.wait(timeout=1.0)

    def flush(self, timeout: float = 5.0) -> bool:
        deadline = time.monotonic() + max(0.0, timeout)
        with self._cv:
            self._cv.notify_all()
            while self._queue and not self._stopped:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    return False
                self._cv.wait(timeout=remaining)
            return not self._queue

    def close(self, timeout: float = 5.0) -> bool:
        with self._cv:
            self._closing = True
        delivered = self.flush(timeout)
        with self._cv:
            self._stopped = True
            self._cv.notify_all()
        return delivered

    def stats(self) -> dict[str, int]:
        with self._cv:
            return {"queued": len(self._queue), "accepted": self._accepted, "dropped": self._dropped}

    def wrap(self, function):
        if inspect.iscoroutinefunction(function):

            @wraps(function)
            async def async_wrapped(*args, **kwargs):
                try:
                    return await function(*args, **kwargs)
                except Exception as error:
                    self.capture_exception(error)
                    raise

            return async_wrapped

        @wraps(function)
        def wrapped(*args, **kwargs):
            try:
                return function(*args, **kwargs)
            except Exception as error:
                try:
                    self.capture_exception(error)
                except Exception:
                    pass
                raise

        return wrapped

    def install(self):
        """Preserve original process/thread hooks; return an uninstall function."""
        previous = sys.excepthook
        previous_thread = threading.excepthook

        def hook(typ, value, tb):
            try:
                self.capture_exception(value)
                self.flush(0.5)
            finally:
                previous(typ, value, tb)

        def thread_hook(args):
            try:
                self.capture_exception(args.exc_value)
            finally:
                previous_thread(args)

        sys.excepthook = hook
        threading.excepthook = thread_hook

        def uninstall():
            if sys.excepthook is hook:
                sys.excepthook = previous
            if threading.excepthook is thread_hook:
                threading.excepthook = previous_thread

        return uninstall
