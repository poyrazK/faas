"""Application-authorized job snapshots driven by private realtime invalidations."""

from __future__ import annotations

import asyncio
import json
import math
import re
from collections.abc import AsyncIterator, Awaitable, Callable, Coroutine
from dataclasses import asdict, dataclass
from typing import Any, Literal, cast

from .._async_request import RequestOptions, run_request
from .realtime_recovery import (
    RealtimeRecoveryCoordinator,
    RealtimeRecoveryEvent,
    RealtimeRecoverySession,
    RealtimeRecoveryTarget,
)
from .realtime_websocket import RealtimeWebSocketMessage

LiveJobStatus = Literal[
    "awaiting_storage_event",
    "submission_unresolved",
    "queued",
    "running",
    "succeeded",
    "failed",
    "cancelled",
    "dead_letter",
]
_STATUSES = {
    "awaiting_storage_event",
    "submission_unresolved",
    "queued",
    "running",
    "succeeded",
    "failed",
    "cancelled",
    "dead_letter",
}
_TERMINAL = {"succeeded", "failed", "cancelled", "dead_letter"}


class LiveJobError(Exception):
    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


@dataclass(frozen=True, kw_only=True)
class LiveJobFailure:
    input_id: str
    code: str


@dataclass(frozen=True, kw_only=True)
class LiveJobWaiting:
    kind: Literal["approval", "input"]
    gate_id: str
    name: str


@dataclass(frozen=True, kw_only=True)
class LiveJobSnapshot:
    version: Literal[1]
    resource_id: str
    run_id: str | None
    status: LiveJobStatus
    total: int
    succeeded: int
    failed: int
    cancelled: int
    running: int
    report: Literal["none", "pending", "ready", "unavailable"]
    failures: tuple[LiveJobFailure, ...]
    waiting: LiveJobWaiting | None = None

    @classmethod
    def from_dict(cls, value: Any, resource_id: str) -> LiveJobSnapshot:
        if (
            not isinstance(value, dict)
            or not _id(resource_id)
            or type(value.get("version")) is not int
            or value["version"] != 1
            or value.get("resource_id") != resource_id
        ):
            raise LiveJobError("invalid_snapshot")
        v = value
        if (
            not isinstance(v.get("status"), str)
            or v["status"] not in _STATUSES
            or not isinstance(v.get("report"), str)
            or v["report"] not in {"none", "pending", "ready", "unavailable"}
            or "run_id" not in v
            or v["run_id"] is not None
            and not _id(v["run_id"])
        ):
            raise LiveJobError("invalid_snapshot")
        for key in ("total", "succeeded", "failed", "cancelled", "running"):
            if type(v.get(key)) is not int or not 0 <= v[key] <= 9007199254740991:
                raise LiveJobError("invalid_snapshot")
        if (
            sum(v[k] for k in ("succeeded", "failed", "cancelled", "running")) > v["total"]
            or v["status"] in _TERMINAL
            and v["run_id"] is None
            or v["report"] == "ready"
            and v["status"] not in _TERMINAL
        ):
            raise LiveJobError("invalid_snapshot")
        failures = v.get("failures")
        if (
            not isinstance(failures, list)
            or len(failures) > 1000
            or any(not isinstance(f, dict) or not _id(f.get("input_id")) or not _id(f.get("code")) for f in failures)
        ):
            raise LiveJobError("invalid_snapshot")
        waiting = v.get("waiting")
        if "waiting" in v and waiting is None:
            raise LiveJobError("invalid_snapshot")
        if waiting is not None and (
            not isinstance(waiting, dict)
            or waiting.get("kind") not in ("approval", "input")
            or not isinstance(waiting.get("gate_id"), str)
            or re.fullmatch(r"[a-f0-9]{64}", waiting["gate_id"]) is None
            or not isinstance(waiting.get("name"), str)
            or re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}", waiting["name"]) is None
            or v["status"] not in ("running", "queued")
        ):
            raise LiveJobError("invalid_snapshot")
        return cls(
            waiting=None
            if waiting is None
            else LiveJobWaiting(kind=waiting["kind"], gate_id=waiting["gate_id"], name=waiting["name"]),
            version=1,
            resource_id=resource_id,
            run_id=v["run_id"],
            status=v["status"],
            total=v["total"],
            succeeded=v["succeeded"],
            failed=v["failed"],
            cancelled=v["cancelled"],
            running=v["running"],
            report=v["report"],
            failures=tuple(LiveJobFailure(input_id=f["input_id"], code=f["code"]) for f in failures),
        )

    def to_dict(self) -> dict[str, Any]:
        value = asdict(self)
        value["failures"] = list(value["failures"])
        if self.waiting is None:
            value.pop("waiting")
        return value

    @property
    def complete(self) -> bool:
        return self.status in _TERMINAL and self.report != "pending"


def _id(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_.:-]{1,256}", value) is not None


def _event(value: Any, resource_id: str) -> bool:
    return (
        isinstance(value, dict)
        and type(value.get("version")) is int
        and value["version"] == 1
        and value.get("resource_id") == resource_id
        and isinstance(value.get("kind"), str)
        and value["kind"] in {"progress", "task_failed", "completed", "report_ready"}
    )


@dataclass(frozen=True, kw_only=True)
class LiveJobSink:
    ready: Callable[[], None]
    changed: Callable[[Any], None]
    disconnected: Callable[[], None]
    failed: Callable[[], None]


LiveJobSource = Callable[[LiveJobSink], Coroutine[Any, Any, None]]


class LiveJob:
    """One resource and authenticated identity. Create anew after session changes.

    Each updates() owns a subscription. Intermediate invalidations coalesce to
    one pending refresh. Native completion and report readiness come exclusively
    from load_snapshot, never from a worker or notification payload.
    """

    def __init__(
        self,
        *,
        resource_id: str,
        load_snapshot: Callable[[], Coroutine[Any, Any, Any]],
        source: LiveJobSource,
        timeout: float,
        max_snapshots: int = 10000,
    ):
        if (
            not _id(resource_id)
            or type(timeout) not in (int, float)
            or not math.isfinite(timeout)
            or timeout <= 0
            or type(max_snapshots) is not int
            or max_snapshots < 1
        ):
            raise ValueError("Invalid live job options")
        self._resource_id, self._load, self._source = resource_id, load_snapshot, source
        self._timeout, self._limit = timeout, max_snapshots
        self._workers: set[asyncio.Task[None]] = set()

    async def updates(self) -> AsyncIterator[LiveJobSnapshot]:
        changed = asyncio.Event()
        active, failed = True, False
        reader: asyncio.Task[Any] | None = None
        deadline = asyncio.get_running_loop().time() + self._timeout

        def ready() -> None:
            if active:
                changed.set()

        def event(value: Any) -> None:
            if active and _event(value, self._resource_id):
                changed.set()

        def failure() -> None:
            nonlocal failed
            if active:
                failed = True
                changed.set()
                if reader is not None:
                    reader.cancel()

        async def consume() -> None:
            try:
                await self._source(LiveJobSink(ready=ready, changed=event, disconnected=lambda: None, failed=failure))
                failure()
            except asyncio.CancelledError:
                raise
            except Exception:
                failure()

        worker = asyncio.create_task(consume())
        self._workers.add(worker)
        # Retrieve exceptions even if a custom source suppresses cancellation.
        worker.add_done_callback(lambda t: None if t.cancelled() else t.exception())
        worker.add_done_callback(self._workers.discard)

        def expire() -> None:
            nonlocal active
            active = False
            changed.set()
            worker.cancel()
            if reader is not None:
                reader.cancel()

        timer = asyncio.get_running_loop().call_later(self._timeout, expire)
        prior: LiveJobSnapshot | None = None
        count = 0
        try:
            while True:
                remaining = deadline - asyncio.get_running_loop().time()
                if remaining <= 0:
                    raise LiveJobError("deadline_exceeded")
                try:
                    await run_request(self, changed.wait, RequestOptions(timeout=remaining), remaining)
                except TimeoutError:
                    raise LiveJobError("deadline_exceeded") from None
                if failed:
                    raise LiveJobError("subscription_failed")
                changed.clear()
                count += 1
                if count > self._limit:
                    raise LiveJobError("snapshot_limit")
                remaining = deadline - asyncio.get_running_loop().time()
                if remaining <= 0:
                    raise LiveJobError("deadline_exceeded")
                try:
                    reader = asyncio.create_task(
                        run_request(self, self._load, RequestOptions(timeout=remaining), remaining)
                    )
                    value = await reader
                    reader = None
                    snapshot = LiveJobSnapshot.from_dict(value, self._resource_id)
                except asyncio.CancelledError:
                    if failed:
                        raise LiveJobError("subscription_failed") from None
                    if asyncio.get_running_loop().time() >= deadline:
                        raise LiveJobError("deadline_exceeded") from None
                    raise
                except TimeoutError:
                    raise LiveJobError("deadline_exceeded") from None
                except Exception:
                    raise LiveJobError("snapshot_failed") from None
                if failed:
                    raise LiveJobError("subscription_failed")
                if prior is not None and (
                    prior.run_id is not None
                    and snapshot.run_id != prior.run_id
                    or prior.status in _TERMINAL
                    and snapshot.status != prior.status
                ):
                    raise LiveJobError("snapshot_failed")
                prior = snapshot
                yield snapshot
                if snapshot.complete:
                    return
        finally:
            active = False
            timer.cancel()
            if reader is not None:
                reader.cancel()
            worker.cancel()
            await asyncio.wait({worker}, timeout=0)


def realtime_live_job_source(
    *, connect: Callable[[], Awaitable[RealtimeRecoverySession]], channel: str, max_retries: int = 20
) -> LiveJobSource:
    """Reuse native reconnect/replay. RealtimeWebSocketClient resolves a fresh
    app-issued private ticket on each connect. Gaps trigger an authoritative load.
    """

    async def source(sink: LiveJobSink) -> None:
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

        async def load(target: RealtimeRecoveryTarget) -> int:
            sink.ready()
            return target.latest_sequence

        await RealtimeRecoveryCoordinator(
            connect=connect,
            process=process,
            load_snapshot=load,
            channel=channel,
            on_ready=lambda _: sink.ready(),
            max_retries=max_retries,
        ).run()

    return cast(LiveJobSource, source)
