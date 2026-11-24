"""Combined subscription/processed-replay gate without transport or retry ownership."""

from __future__ import annotations

import asyncio
import math
import time
from dataclasses import dataclass

from .realtime_catch_up import RealtimeCatchUpStatus, RealtimeCatchUpTracker, RealtimeCatchUpWaitError
from .realtime_connection_status import RealtimeConnectionStatus, RealtimeConnectionStatusTracker
from .realtime_subscriptions import (
    RealtimeSubscriptionSummary,
    RealtimeSubscriptionTracker,
    RealtimeSubscriptionWaitError,
)


@dataclass(frozen=True, kw_only=True)
class RealtimeCatchUpRequirement:
    tracker: RealtimeCatchUpTracker
    generation: int


@dataclass(frozen=True, kw_only=True)
class RealtimeReadinessResult:
    connection: RealtimeConnectionStatus
    subscriptions: RealtimeSubscriptionSummary
    catch_up: tuple[RealtimeCatchUpStatus, ...]


class RealtimeReadinessError(Exception):
    """A gate failure with safe stage/reason and no transport response details."""

    def __init__(self, stage: str, reason: str):
        super().__init__(f"Realtime readiness failed: {stage}/{reason}")
        self.stage, self.reason = stage, reason


async def wait_realtime_ready(
    connection: RealtimeConnectionStatusTracker,
    subscriptions: RealtimeSubscriptionTracker,
    *,
    attempt_id: int,
    subscription_generation: int,
    catch_up: tuple[RealtimeCatchUpRequirement, ...] = (),
    channels: tuple[str, ...] | None = None,
    include_inbox: bool | None = None,
    timeout_seconds: float | None = None,
    fail_on_stall: bool = True,
) -> RealtimeReadinessResult:
    """Await current connected transport, selected ACKs and selected replay targets.

    Bind inputs to one account/endpoint/loop. A single deadline covers all gates.
    No ACK, subscribe, retry, replay or recovery request is sent.
    """
    if not isinstance(connection, RealtimeConnectionStatusTracker) or not isinstance(
        subscriptions, RealtimeSubscriptionTracker
    ):
        raise ValueError("Connection and subscription trackers are required")
    if (
        type(attempt_id) is not int
        or attempt_id < 1
        or type(subscription_generation) is not int
        or subscription_generation < 1
    ):
        raise ValueError("Attempt and generation must be positive integers")
    if type(fail_on_stall) is not bool:
        raise ValueError("fail_on_stall must be boolean")
    if timeout_seconds is not None and (
        isinstance(timeout_seconds, bool)
        or not isinstance(timeout_seconds, (int, float))
        or not math.isfinite(timeout_seconds)
        or not 0 <= timeout_seconds <= 86400
    ):
        raise ValueError("timeout_seconds must be between 0 and 86400, or None")
    if not isinstance(catch_up, (tuple, list)) or len(catch_up) > 32:
        raise ValueError("At most 32 catch-up requirements are supported")
    if channels is not None:
        if not isinstance(channels, (tuple, list)):
            raise ValueError("channels must be a tuple/list or None")
        channels = tuple(channels)
    requirements = tuple(catch_up)
    seen = set()
    for requirement in requirements:
        if (
            not isinstance(requirement, RealtimeCatchUpRequirement)
            or not isinstance(requirement.tracker, RealtimeCatchUpTracker)
            or type(requirement.generation) is not int
            or requirement.generation < 1
        ):
            raise ValueError("Invalid catch-up requirement")
        status = requirement.tracker.snapshot()
        key = (status.inbox, status.channel)
        if key in seen:
            raise ValueError("Duplicate catch-up stream")
        seen.add(key)
    loop = asyncio.get_running_loop()
    sources = [(connection._readiness_waiters, 128), (subscriptions._waiters, subscriptions._max_waiters)]
    sources += [(requirement.tracker._waiters, requirement.tracker._max_waiters) for requirement in requirements]
    async with asyncio.timeout(timeout_seconds):
        while True:
            current = connection.snapshot()
            if connection._disposed:
                raise RealtimeReadinessError("connection", "disposed")
            if current.attempt_id != attempt_id:
                raise RealtimeReadinessError("connection", "obsolete")
            if current.state == "offline":
                raise RealtimeReadinessError("connection", "disconnected")
            if subscriptions._attempt != attempt_id:
                raise RealtimeReadinessError("subscriptions", "obsolete")
            selected = None
            try:
                selected = await subscriptions.wait_ready(
                    subscription_generation,
                    channels=channels,
                    include_inbox=include_inbox,
                    timeout_seconds=0,
                )
            except TimeoutError:
                pass
            except RealtimeSubscriptionWaitError as error:
                raise RealtimeReadinessError("subscriptions", error.reason) from None
            if subscriptions._generation != subscription_generation or subscriptions._disposed:
                continue
            # Resolve the same selection even while its ACKs are pending.
            chosen = (
                {(False, channel) for channel in channels}
                if channels is not None
                else {key for key in subscriptions._entries if not key[0]}
            )
            if include_inbox is True or include_inbox is None and (True, None) in subscriptions._entries:
                chosen.add((True, None))
            completed = []
            stall_delay = None
            for requirement in requirements:
                tracker = requirement.tracker
                status = tracker.snapshot()
                if (status.inbox, status.channel) not in chosen:
                    raise ValueError("Catch-up stream must be in the selected subscriptions")
                if status.attempt_id != attempt_id:
                    raise RealtimeReadinessError("catch_up", "obsolete")
                try:
                    completed.append(
                        await tracker.wait_caught_up(
                            requirement.generation,
                            timeout_seconds=0,
                            fail_on_stall=fail_on_stall,
                        )
                    )
                except TimeoutError:
                    if (
                        fail_on_stall
                        and tracker._stall_after is not None
                        and status.state == "catching_up"
                        and tracker._last_progress is not None
                    ):
                        remaining = max(0.0, tracker._last_progress + tracker._stall_after - time.monotonic())
                        stall_delay = remaining if stall_delay is None else min(stall_delay, remaining)
                except RealtimeCatchUpWaitError as error:
                    raise RealtimeReadinessError("catch_up", error.reason) from None
            # Monitoring callbacks may change generations during snapshot evaluation.
            if connection.snapshot() != current or subscriptions._generation != subscription_generation:
                continue
            if any(requirement.tracker._status.generation != requirement.generation for requirement in requirements):
                continue
            if selected is not None and any(
                subscriptions._entries.get((entry.inbox, entry.channel)) != entry for entry in selected.subscriptions
            ):
                continue
            if any(requirement.tracker._disposed for requirement in requirements):
                continue
            if len(completed) == len(requirements) and any(
                requirement.tracker._status.state != "caught_up" for requirement in requirements
            ):
                continue
            if current.state == "connected" and selected is not None and len(completed) == len(requirements):
                return RealtimeReadinessResult(connection=current, subscriptions=selected, catch_up=tuple(completed))
            futures = []
            try:
                for source, capacity in sources:
                    if len(source) >= capacity:
                        raise ValueError("Readiness waiter capacity reached")
                    future = loop.create_future()
                    source.add(future)
                    futures.append((source, future))
                await asyncio.wait(
                    [future for _, future in futures], timeout=stall_delay, return_when=asyncio.FIRST_COMPLETED
                )
            finally:
                for source, future in futures:
                    source.discard(future)
                    if not future.done():
                        future.cancel()
