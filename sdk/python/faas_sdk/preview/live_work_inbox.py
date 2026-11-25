"""Observe authorized inbox snapshots for a single application business record."""

from __future__ import annotations

import asyncio
import json
import math
import re
from collections import OrderedDict
from collections.abc import AsyncIterator, Awaitable, Callable, Coroutine
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Literal, TypedDict, TypeVar
from urllib.parse import parse_qs, urlsplit

from .._async_request import RequestOptions, RequestTimeoutError, _retain, run_request
from ..job_runtime import JobContract, job_input_id
from ._work_inbox_protocol import SPEC
from .pipeline_state import _hash, _uuid
from .query_tools import A, QueryTool
from .realtime_connection_status import RealtimeConnectionStatus
from .realtime_recovery import (
    RealtimeRecoveryCoordinator,
    RealtimeRecoveryEvent,
    RealtimeRecoverySession,
    RealtimeRecoveryTarget,
    _RealtimeSubscriptionDenied,
)
from .realtime_websocket import RealtimeWebSocketMessage
from .work_inbox import WorkInboxEntry, WorkInboxFilter

LiveWorkInboxCode = Literal[
    "unauthorized",
    "invalid_snapshot",
    "snapshot_failed",
    "subscription_failed",
    "deadline",
    "cancelled",
    "snapshot_limit",
]
_Stop = Literal["closed"] | LiveWorkInboxCode
_PipelineStates = frozenset(
    ("start_unknown", "running", "awaiting_approval", "awaiting_input", "succeeded", "failed", "cancelled")
)
_ActionStates = frozenset(("prepared", "unknown", "pending", "succeeded", "failed", "needs_review", "cancelled"))
_ENTRY: JobContract[WorkInboxFilter, WorkInboxEntry] = JobContract(SPEC)


class LiveWorkInboxError(Exception):
    def __init__(self, code: LiveWorkInboxCode):
        super().__init__("Live work inbox: " + code)
        self.code = code


class WorkInboxWatchTarget(TypedDict):
    resource_id: str
    business_ref: str


class LiveWorkInboxSnapshot(WorkInboxWatchTarget):
    version: Literal[1]
    items: list[WorkInboxEntry]
    truncated: bool


class WorkInboxInvalidation(TypedDict):
    version: Literal[1]
    kind: Literal["work_inbox_changed"]
    resource_id: str
    change_id: str


@dataclass(frozen=True, slots=True)
class LiveWorkInboxSink:
    ready: Callable[[], None]
    changed: Callable[[Any], None]
    disconnected: Callable[[], None]
    failed: Callable[[Literal["unauthorized", "subscription_failed"]], None]


LiveWorkInboxSource = Callable[[LiveWorkInboxSink], Coroutine[Any, Any, None]]


def work_inbox_watch_target(target: WorkInboxWatchTarget) -> WorkInboxWatchTarget:
    if (
        not isinstance(target, dict)
        or not job_input_id(target.get("resource_id"))
        or not job_input_id(target.get("business_ref"))
    ):
        raise ValueError("Invalid work inbox watch target")
    return WorkInboxWatchTarget(resource_id=target["resource_id"], business_ref=target["business_ref"])


def work_inbox_invalidation(resource_id: str, change_id: str) -> WorkInboxInvalidation:
    if not job_input_id(resource_id) or not job_input_id(change_id):
        raise ValueError("Invalid work inbox invalidation")
    return WorkInboxInvalidation(version=1, kind="work_inbox_changed", resource_id=resource_id, change_id=change_id)


def _change_id(event: Any, target: WorkInboxWatchTarget) -> str | None:
    if (
        isinstance(event, dict)
        and set(event) == {"change_id", "kind", "resource_id", "version"}
        and type(event["version"]) is int
        and event["version"] == 1
        and event["kind"] == "work_inbox_changed"
        and event["resource_id"] == target["resource_id"]
        and job_input_id(event["change_id"])
    ):
        return str(event["change_id"])
    return None


def _check_review(item: WorkInboxEntry) -> None:
    review = item["review"]
    if review["kind"] != ("approval" if item["state"] == "awaiting_approval" else "input") or not _hash(review["id"]):
        raise ValueError
    datetime.fromisoformat(review["expires_at"].replace("Z", "+00:00"))
    url = urlsplit(review["review_url"])
    _ = url.port
    if (
        not url.hostname
        or url.username is not None
        or url.password is not None
        or url.fragment
        or (url.scheme != "https" and not (url.scheme == "http" and url.hostname in ("127.0.0.1", "localhost", "::1")))
        or parse_qs(url.query, keep_blank_values=True) != {"gate": [review["id"]], "run": [item["run_id"]]}
    ):
        raise ValueError


def parse_live_work_inbox_snapshot(raw: Any, expected: WorkInboxWatchTarget) -> LiveWorkInboxSnapshot:
    target = work_inbox_watch_target(expected)
    try:
        if (
            not isinstance(raw, dict)
            or set(raw) != {"version", "resource_id", "business_ref", "items", "truncated"}
            or type(raw["version"]) is not int
            or raw["version"] != 1
            or type(raw["truncated"]) is not bool
            or any(raw[key] != target[key] for key in ("resource_id", "business_ref"))
            or not isinstance(raw["items"], list)
            or len(raw["items"]) > 100
        ):
            raise ValueError
        items, keys = [], set()
        for value in raw["items"]:
            item = _ENTRY.output(value)
            pipeline = item["kind"] == "pipeline"
            waiting = item["state"] in ("awaiting_approval", "awaiting_input")
            if (
                not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", item["name"])
                or item["state"] not in (_PipelineStates if pipeline else _ActionStates)
                or not _uuid(item["request_id"])
                or item.get("business_ref") != target["business_ref"]
                or waiting != ("review" in item)
            ):
                raise ValueError
            if pipeline:
                if "action_id" in item or (
                    ("run_id" in item) if item["state"] == "start_unknown" else not _uuid(item.get("run_id"))
                ):
                    raise ValueError
            elif not _hash(item.get("action_id")) or "run_id" in item:
                raise ValueError
            if waiting:
                _check_review(item)
            key = (item["kind"], item["name"], item.get("action_id", item["request_id"]))
            if key in keys:
                raise ValueError
            keys.add(key)
            items.append(item)
        return LiveWorkInboxSnapshot(version=1, **target, items=items, truncated=raw["truncated"])
    except Exception:
        raise LiveWorkInboxError("invalid_snapshot") from None


def _duration(value: float) -> bool:
    return type(value) in (int, float) and math.isfinite(value) and 0 < value <= 2147483.647


def watch_work_inbox(
    *,
    target: WorkInboxWatchTarget,
    source: LiveWorkInboxSource,
    load_snapshot: Callable[[float], Coroutine[Any, Any, Any]],
    timeout: float,
    refresh_interval: float = 15,
    max_snapshots: int = 1000,
) -> AsyncIterator[LiveWorkInboxSnapshot]:
    """Each iterator owns its subscription and emits verified current snapshots."""
    return _watch_inbox_snapshots(
        target=target, source=source, load_snapshot=load_snapshot, timeout=timeout,
        refresh_interval=refresh_interval, max_snapshots=max_snapshots, parse=parse_live_work_inbox_snapshot,
    )


_Snapshot = TypeVar("_Snapshot")
_RawSnapshot = TypeVar("_RawSnapshot")


async def _watch_inbox_snapshots(
    *,
    target: WorkInboxWatchTarget,
    source: LiveWorkInboxSource,
    load_snapshot: Callable[[float], Coroutine[Any, Any, _RawSnapshot]],
    timeout: float,
    refresh_interval: float,
    max_snapshots: int,
    parse: Callable[[_RawSnapshot, WorkInboxWatchTarget], _Snapshot],
    retry_read: Callable[[Exception], bool] | None = None,
) -> AsyncIterator[_Snapshot]:
    target = work_inbox_watch_target(target)
    if (
        not _duration(timeout)
        or not _duration(refresh_interval)
        or type(max_snapshots) is not int
        or not 1 <= max_snapshots <= 10000
        or not callable(source)
        or not callable(load_snapshot)
    ):
        raise ValueError("Invalid live work inbox options")
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    wake = asyncio.Event()
    seen: OrderedDict[str, None] = OrderedDict()
    terminal: list[_Stop] = []
    connected = dirty = False
    generation = count = 0
    prior: str | None = None
    period: asyncio.TimerHandle | None = None
    reader: asyncio.Task[Any] | None = None
    worker: asyncio.Task[None] | None = None
    workers: set[asyncio.Future[Any]] = set()

    def invalidate() -> None:
        nonlocal dirty
        if connected and not terminal:
            dirty = True
            wake.set()

    def periodic() -> None:
        nonlocal period
        if connected and not terminal:
            invalidate()
            period = loop.call_later(refresh_interval, periodic)

    def ready() -> None:
        nonlocal connected, period
        if terminal:
            return
        if not connected:
            connected = True
            period = loop.call_later(refresh_interval, periodic)
        invalidate()

    def changed(event: Any) -> None:
        if terminal or not connected:
            return
        key = _change_id(event, target)
        if key is None or key in seen:
            return
        if len(seen) >= 256:
            seen.popitem(last=False)
        seen[key] = None
        invalidate()

    def disconnected() -> None:
        nonlocal connected, dirty, generation, prior
        if terminal or not connected:
            return
        connected = dirty = False
        generation += 1
        prior = None
        if period is not None:
            period.cancel()
        if reader is not None:
            reader.cancel()

    def stop(code: _Stop) -> None:
        if terminal:
            return
        terminal.append(code)
        timer.cancel()
        if period is not None:
            period.cancel()
        if reader is not None:
            reader.cancel()
        if worker is not None:
            worker.cancel()
        wake.set()

    def failed(code: Literal["unauthorized", "subscription_failed"]) -> None:
        stop("unauthorized" if code == "unauthorized" else "subscription_failed")

    async def consume() -> None:
        try:
            await source(LiveWorkInboxSink(ready, changed, disconnected, failed))
        except asyncio.CancelledError:
            raise
        except Exception:
            pass
        stop("subscription_failed")

    timer = loop.call_later(timeout, stop, "deadline")
    worker = asyncio.create_task(consume())
    _retain(worker, workers)
    try:
        while not terminal:
            if not connected or not dirty:
                wake.clear()
                await wake.wait()
                continue
            dirty = False
            count += 1
            if count > max_snapshots:
                stop("snapshot_limit")
                break
            left = deadline - loop.time()
            if left <= 0:
                stop("deadline")
                break
            epoch = generation
            reader = asyncio.create_task(
                run_request(target, lambda: load_snapshot(left), RequestOptions(timeout=left), left)
            )
            try:
                value = await reader
            except asyncio.CancelledError:
                task = asyncio.current_task()
                if task is not None and task.cancelling():
                    raise
                if not terminal and epoch == generation:
                    stop("snapshot_failed")
                continue
            except RequestTimeoutError:
                if epoch == generation and not terminal:
                    stop("deadline")
                continue
            except Exception as error:
                if epoch == generation and not terminal:
                    if retry_read is not None and retry_read(error):
                        prior = None
                        continue
                    code = getattr(error, "code", None)
                    if code == "unauthorized":
                        stop("unauthorized")
                    elif code == "deadline":
                        stop("deadline")
                    elif code == "invalid_snapshot":
                        stop("invalid_snapshot")
                    else:
                        stop("snapshot_failed")
                continue
            finally:
                reader = None
            if terminal or epoch != generation:
                continue
            if loop.time() >= deadline:
                stop("deadline")
                break
            snapshot = parse(value, target)
            key = json.dumps(snapshot, sort_keys=True, ensure_ascii=False)
            if key != prior:
                prior = key
                yield snapshot
        if terminal and terminal[0] != "closed":
            raise LiveWorkInboxError(terminal[0])
    finally:
        stop("closed")
        await asyncio.wait({worker}, timeout=0)


async def read_work_inbox_snapshot(
    inbox: QueryTool[WorkInboxFilter, WorkInboxEntry, A],
    expected: WorkInboxWatchTarget,
    access: A,
    *,
    timeout: float,
    max_pages: int = 10,
    max_items: int = 100,
) -> LiveWorkInboxSnapshot:
    """Read a bounded set of current, authorized inbox entries, including sparse pages."""
    target = work_inbox_watch_target(expected)
    if (
        type(max_pages) is not int
        or not 1 <= max_pages <= 100
        or type(max_items) is not int
        or not 1 <= max_items <= 100
    ):
        raise ValueError("Invalid work inbox snapshot limits")
    items: list[WorkInboxEntry] = []
    truncated = True
    async for page in inbox.pages(
        {"business_ref": target["business_ref"]}, access, timeout=timeout, max_pages=max_pages, max_items=max_items
    ):
        items.extend(page["items"])
        truncated = page["next_cursor"] is not None
    return parse_live_work_inbox_snapshot(
        LiveWorkInboxSnapshot(version=1, **target, items=items, truncated=truncated), target
    )


def realtime_live_work_inbox_source(
    *, connect: Callable[[], Awaitable[RealtimeRecoverySession]], channel: str, max_retries: int = 20
) -> LiveWorkInboxSource:
    """Observe one private channel through native reconnect, replay and gap handling."""

    async def source(sink: LiveWorkInboxSink) -> None:
        async def connect_current() -> RealtimeRecoverySession:
            try:
                return await connect()
            except LiveWorkInboxError as error:
                if error.code == "unauthorized":
                    sink.failed("unauthorized")
                raise

        async def process(event: RealtimeRecoveryEvent) -> None:
            message = event.data
            if (
                isinstance(message, RealtimeWebSocketMessage)
                and not message.binary
                and not message.deleted
                and len(message.data) <= 4096
            ):
                try:
                    sink.changed(json.loads(message.data))
                except (ValueError, UnicodeError):
                    pass

        async def skip_history(target: RealtimeRecoveryTarget) -> int:
            sink.disconnected()
            return target.latest_sequence

        def connection(status: RealtimeConnectionStatus) -> None:
            if status.state == "offline":
                sink.disconnected()

        coordinator = RealtimeRecoveryCoordinator(
            connect=connect_current,
            process=process,
            load_snapshot=skip_history,
            channel=channel,
            on_ready=lambda _: sink.ready(),
            on_connection_change=connection,
            max_retries=max_retries,
        )
        try:
            await coordinator.run()
        except _RealtimeSubscriptionDenied:
            sink.failed("unauthorized")

    return source
