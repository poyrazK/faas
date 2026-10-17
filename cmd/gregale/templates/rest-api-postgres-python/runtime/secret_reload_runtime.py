# Generated from sdk/python; run python sdk/python/scripts/sync_binding_starters.py.
"""Workload-local atomic secret reloads; no control-plane client or provider calls."""

from __future__ import annotations

import http.client
import json
import math
import os
import re
import signal
import socket
import threading
import time
from collections.abc import Callable, Mapping, Sequence
from concurrent.futures import Future
from dataclasses import dataclass
from pathlib import Path
from queue import Empty, Queue
from types import FrameType, MappingProxyType
from urllib.parse import urlsplit


class SecretReloadError(Exception):
    """Sanitized protocol error without underlying credential/driver diagnostics."""


@dataclass(frozen=True, repr=False)
class SecretSnapshot:
    revision: str
    secrets: Mapping[str, str]

    def __post_init__(self) -> None:
        object.__setattr__(self, "secrets", MappingProxyType(dict(self.secrets)))

    def __repr__(self) -> str:
        return "SecretSnapshot(redacted)"


class SecretReloadCancellation:
    """Event-like cancellation with a shared shutdown and optional deadline."""

    def __init__(self, events: Sequence[threading.Event], deadline: float | None = None):
        self._events = tuple(events)
        self._deadline = deadline

    def is_set(self) -> bool:
        return any(event.is_set() for event in self._events) or (
            self._deadline is not None and time.monotonic() >= self._deadline
        )

    def check(self) -> None:
        if self.is_set():
            raise SecretReloadError("Secret reload cancelled")

    def wait(self, timeout: float | None = None) -> bool:
        deadline = None if timeout is None else time.monotonic() + timeout
        while not self.is_set():
            if deadline is not None and time.monotonic() >= deadline:
                return False
            duration = 0.05 if deadline is None else max(0, min(0.05, deadline - time.monotonic()))
            self._events[0].wait(duration)
        return True

    def with_timeout(self, seconds: float) -> SecretReloadCancellation:
        deadline = time.monotonic() + seconds
        if self._deadline is not None:
            deadline = min(deadline, self._deadline)
        return SecretReloadCancellation(self._events, deadline)


@dataclass(frozen=True)
class SecretConsumer:
    """Invalidate synchronously/idempotently and fence late publication. Apply
    must honor cancellation before publishing resources. Register every user.
    """

    apply: Callable[[SecretSnapshot, SecretReloadCancellation], None]
    invalidate: Callable[[], None]


def _read(path: str, cancel: SecretReloadCancellation) -> str:
    cancel.check()
    raw = Path(path).read_text(encoding="utf-8")
    cancel.check()
    return raw


def _ready(path: str, cancel: SecretReloadCancellation) -> None:
    cancel.check()
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as file:
        file.write("ready\n")
    cancel.check()


def _sleep(seconds: float, cancel: SecretReloadCancellation) -> None:
    cancel.wait(seconds)
    cancel.check()


def _post(endpoint: str, body: bytes, cancel: SecretReloadCancellation) -> int:
    cancel.check()
    url = urlsplit(endpoint)
    transport = http.client.HTTPSConnection if url.scheme == "https" else http.client.HTTPConnection
    connection = transport(url.hostname, url.port, timeout=5)
    outcome: Future[int] = Future()

    def send() -> None:
        try:
            connection.connect()
            # A late DNS/connect result must not start a POST after shutdown.
            cancel.check()
            connection.request(
                "POST",
                (url.path or "/") + ("?" + url.query if url.query else ""),
                body,
                {"Content-Type": "application/json"},
            )
            response = connection.getresponse()
            try:
                cancel.check()
                status = response.status
            finally:
                response.close()
            outcome.set_result(status)
        except BaseException:
            outcome.set_exception(SecretReloadError("Secret acknowledgement transport failed"))
        finally:
            connection.close()

    # Bound the whole request, including slow headers/DNS, rather than each
    # socket read. The owned daemon can drain late DNS; it cannot POST after
    # cancellation. No body parsing, redirects, proxy env or API wrappers.
    threading.Thread(target=send, name="gregale-secret-ack", daemon=True).start()
    try:
        while True:
            cancel.check()
            try:
                result = outcome.result(timeout=0.05)
                cancel.check()
                return result
            except TimeoutError:
                continue
    finally:
        active_socket = connection.sock
        if active_socket is not None:
            try:
                active_socket.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        connection.close()


@dataclass(frozen=True)
class SecretReloadIO:
    """Optional local adapters; callbacks must honor cancellation/deadlines."""

    read: Callable[[str, SecretReloadCancellation], str] = _read
    ready: Callable[[str, SecretReloadCancellation], None] = _ready
    post: Callable[[str, bytes, SecretReloadCancellation], int] = _post
    sleep: Callable[[float, SecretReloadCancellation], None] = _sleep


def _reload_settings(env: Mapping[str, str] | None, startup_timeout: float) -> tuple[str, str, str, str]:
    settings = os.environ if env is None else env
    snapshot, ready = (
        settings.get(key, "") for key in ("FAAS_SECRETS_SNAPSHOT_FILE", "FAAS_SECRETS_RELOAD_READY_FILE")
    )
    if any(
        not isinstance(path, str) or not os.path.isabs(path) or re.search(r"[\x00\r\n]", path)
        for path in (snapshot, ready)
    ):
        raise SecretReloadError("Atomic secret reload paths are unavailable")
    endpoint, generation = (
        settings.get(key, "") for key in ("FAAS_SECRETS_RELOAD_ACK_ENDPOINT", "FAAS_SECRETS_RELOAD_GENERATION")
    )
    try:
        url = urlsplit(endpoint)
        valid = (
            url.scheme in ("http", "https")
            and url.hostname
            and not (url.username or url.password or url.fragment)
            and not re.search(r"[\x00\r\n]", endpoint)
        )
        _ = url.port
    except (ValueError, TypeError):
        valid = False
    if not valid:
        raise SecretReloadError("Secret acknowledgement endpoint is unavailable")
    if not isinstance(generation, str) or not re.fullmatch(r"[0-9a-f]{32}", generation):
        raise SecretReloadError("Secret execution identity is unavailable")
    if not isinstance(startup_timeout, (float, int)) or not math.isfinite(startup_timeout) or startup_timeout <= 0:
        raise SecretReloadError("Initial secret wait deadline is invalid")
    return snapshot, ready, endpoint, generation


class SecretReloadCoordinator:
    """One coordinator per workload. Start/stop on the main thread to install
    and restore SIGHUP. Application work is serialized on a daemon worker; stop
    disables clients without joining arbitrary callbacks. A background stop
    disables immediately; call stop again on the main thread to restore SIGHUP.
    """

    def __init__(
        self,
        consumers: Sequence[SecretConsumer],
        *,
        env: Mapping[str, str] | None = None,
        cancel: threading.Event | None = None,
        startup_timeout: float = 30,
        on_error: Callable[[SecretReloadError], None] | None = None,
        io: SecretReloadIO | None = None,
    ):
        if not consumers or any(
            not callable(getattr(c, "apply", None)) or not callable(getattr(c, "invalidate", None)) for c in consumers
        ):
            raise SecretReloadError("Secret consumers are unavailable")
        self._consumers = tuple(consumers)
        self._snapshot, self._ready, self._endpoint, self._generation = _reload_settings(env, startup_timeout)
        self._startup_timeout, self._on_error, self._io = startup_timeout, on_error, io or SecretReloadIO()
        self._shutdown, self._hangup = threading.Event(), threading.Event()
        self._cancel = SecretReloadCancellation([self._shutdown] + ([] if cancel is None else [cancel]))
        self._lock = threading.RLock()
        self._requests: Queue[Future[str]] = Queue()
        self._initial: Future[str] = Future()
        self._started = False
        self._signal_installed = False
        self._previous_signal: object = None
        self._handler = self._on_hangup  # Stable bound-method identity for cleanup.

    def start(self) -> str:
        try:
            if threading.current_thread() is not threading.main_thread() or not hasattr(signal, "SIGHUP"):
                raise SecretReloadError("Secret reload startup requires SIGHUP on the main thread")
            with self._lock:
                if self._cancel.is_set():
                    raise SecretReloadError("Secret reload cancelled")
                if not self._started:
                    previous = signal.getsignal(signal.SIGHUP)
                    # Do not replace framework-owned reload behavior; apps must
                    # explicitly remove/delegate it before opting into this helper.
                    if callable(previous):
                        raise SecretReloadError("SIGHUP already has an application handler")
                    self._previous_signal = previous
                    signal.signal(signal.SIGHUP, self._handler)
                    self._signal_installed, self._started = True, True
                    threading.Thread(target=self._run, name="gregale-secret-reload", daemon=True).start()
                    threading.Thread(target=self._watch_cancellation, name="gregale-secret-cancel", daemon=True).start()
            return self._wait(self._initial)
        except SecretReloadError:
            self.stop()
            raise
        except Exception:
            self.stop()
            raise SecretReloadError("Secret reload could not be started") from None
        except BaseException:
            self.stop()
            raise

    def reload(self) -> str:
        with self._lock:
            if not self._started or self._cancel.is_set():
                raise SecretReloadError("Secret reload is not running")
            result: Future[str] = Future()
            self._requests.put(result)
        return self._wait(result)

    def stop(self) -> None:
        with self._lock:
            self._shutdown.set()
            if self._signal_installed and threading.current_thread() is threading.main_thread():
                if signal.getsignal(signal.SIGHUP) is self._handler:
                    signal.signal(signal.SIGHUP, self._previous_signal)
                self._signal_installed = False
        self._disable()

    def _watch_cancellation(self) -> None:
        self._cancel.wait()
        self.stop()

    def _on_hangup(self, _number: int, _frame: FrameType | None) -> None:
        if not self._cancel.is_set():
            self._hangup.set()  # No application work; bursts coalesce.

    def _wait(self, result: Future[str]) -> str:
        while True:
            if self._cancel.is_set():
                self.stop()
                raise SecretReloadError("Secret reload cancelled")
            try:
                revision = result.result(timeout=0.05)
                self._cancel.check()
                return revision
            except TimeoutError:
                continue
            except Exception:
                raise SecretReloadError("Secret reload could not be completed") from None
            except BaseException:
                self.stop()
                raise

    def _run(self) -> None:
        try:
            self._initial.set_result(self._refresh(True))
        except BaseException:
            self._initial.set_exception(SecretReloadError("Secret reload could not be completed"))
            self.stop()
            return
        while not self._cancel.is_set():
            if self._hangup.is_set():
                self._hangup.clear()
                try:
                    self._refresh(False)
                except BaseException:
                    if not self._cancel.is_set() and self._on_error is not None:
                        try:
                            self._on_error(SecretReloadError("Secret reload could not be completed"))
                        except BaseException:
                            pass
                continue
            try:
                result = self._requests.get(timeout=0.05)
            except Empty:
                continue
            try:
                result.set_result(self._refresh(False))
            except BaseException:
                result.set_exception(SecretReloadError("Secret reload could not be completed"))
        self.stop()

    def _disable(self) -> bool:
        success = True
        for consumer in self._consumers:
            try:
                result = consumer.invalidate()
                if result is not None:
                    # Reject accidentally async invalidation without leaving a
                    # coroutine warning or accepting adoption evidence.
                    if hasattr(result, "close"):
                        result.close()
                    success = False
            except BaseException:
                success = False
        return success

    def _refresh(self, initial: bool) -> str:
        try:
            self._cancel.check()
            if not self._disable():
                raise SecretReloadError("Secret consumers could not be disabled")
            if initial:
                self._io.ready(self._ready, self._cancel)
            for attempt in range(5):
                if not self._disable():
                    raise SecretReloadError("Secret consumers could not be disabled")
                snapshot = self._read_snapshot(initial and attempt == 0)
                status = "applied"
                try:
                    for consumer in self._consumers:
                        self._cancel.check()
                        outcome = consumer.apply(snapshot, self._cancel)
                        if outcome is not None:
                            if hasattr(outcome, "close"):
                                outcome.close()
                            raise SecretReloadError("Secret consumer must apply synchronously")
                        self._cancel.check()
                except BaseException:
                    self._disable()
                    self._cancel.check()
                    status = "failed"
                if self._ack(snapshot.revision, status):
                    continue
                if status == "failed":
                    raise SecretReloadError("Secret snapshot could not be applied")
                self._cancel.check()
                return snapshot.revision
            raise SecretReloadError("Secret projection changed too many times")
        except BaseException:
            self._disable()
            raise SecretReloadError("Secret reload could not be completed") from None

    def _read_snapshot(self, initial: bool) -> SecretSnapshot:
        cancel = self._cancel.with_timeout(self._startup_timeout) if initial else self._cancel
        backoff = 0.1
        while True:
            for attempt in range(5):
                cancel.check()
                try:
                    raw = self._io.read(self._snapshot, cancel)
                    cancel.check()
                    break
                except FileNotFoundError:
                    if attempt == 4:
                        raise SecretReloadError("Secret snapshot could not be read") from None
            parsed = json.loads(raw)
            if (
                not isinstance(parsed, dict)
                or not isinstance(parsed.get("revision"), str)
                or not (initial and parsed["revision"] == "" or re.fullmatch(r"[0-9a-f]{64}", parsed["revision"]))
                or not isinstance(parsed.get("secrets"), dict)
                or any(not key or not isinstance(value, str) for key, value in parsed["secrets"].items())
            ):
                raise SecretReloadError("Secret snapshot is invalid")
            snapshot = SecretSnapshot(parsed["revision"], parsed["secrets"])
            if snapshot.revision:
                return snapshot
            self._io.sleep(backoff, cancel)
            backoff = min(2, backoff * 2)

    def _ack(self, revision: str, status: str) -> bool:
        body = json.dumps({"revision": revision, "status": status, "generation": self._generation}).encode()
        for attempt in range(10):
            self._cancel.check()
            request = self._cancel.with_timeout(5)
            code = None
            try:
                code = self._io.post(self._endpoint, body, request)
                request.check()
            except BaseException:
                self._cancel.check()
                code = None
            if code == 202:
                return False
            if code == 409:
                return True
            if code is not None and code != 503 or attempt == 9:
                break
            self._io.sleep(min(2, 0.1 * 2**attempt), self._cancel)
        raise SecretReloadError("Secret acknowledgement could not be confirmed")
