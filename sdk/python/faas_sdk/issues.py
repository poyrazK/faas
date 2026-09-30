"""Bounded, best-effort exception reporting independent of trace sampling."""

from __future__ import annotations

import json
import inspect
from functools import wraps
import sys
import threading
import time
import traceback
import uuid
from collections import deque
from datetime import datetime, timezone
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _bounded(value: str, size: int) -> str:
    return value.encode("utf-8", errors="replace")[:size].decode("utf-8", errors="ignore")


class IssueReporter:
    """Report exceptions with a deployment-bound issue token, never an owner key.

    Reporting does not swallow exceptions. Local queues are not durable across
    process crashes. Inspect stats() and close() to detect incomplete delivery.
    """

    def __init__(self, base_url: str, app: str, token: str, *, max_queue: int = 100, timeout: float = 2.0):
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
        self._url = f"{parsed.scheme}://{parsed.netloc}/v1/apps/{quote(app, safe='')}/issue-events"
        self._token = token
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
            "occurred_at": datetime.now(timezone.utc).isoformat(),
            "exception_type": _bounded(f"{type(error).__module__}.{type(error).__name__}", 256),
            "message": _bounded(str(error), 2048),
            "stack_trace": _bounded(
                "".join(traceback.format_exception(type(error), error, error.__traceback__)), 16384
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
        for key in ("trace_id", "span_id", "request_id", "invocation_id", "route", "source_kind"):
            if key in context and isinstance(context[key], str):
                event[key] = _bounded(context[key], 256)
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
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {self._token}"},
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
