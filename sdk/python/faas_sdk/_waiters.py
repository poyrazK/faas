"""Bounded observation of existing builds and server-authored release timelines."""

from __future__ import annotations

import asyncio
import copy
import inspect
import math
import random
from collections.abc import Awaitable, Callable, Coroutine
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Generic, Literal, TypeVar

from ._async_request import (
    RequestOptions,
    RequestTimeoutError,
    owned_async_transport,
    run_request,
    snapshot_async_client,
)
from .types import UNSET

if TYPE_CHECKING:
    from ._wrapper import FaaSClient
    from .models.build_response import BuildResponse
    from .models.release_timeline import ReleaseTimeline

T = TypeVar("T", bound="BuildResponse | ReleaseTimeline")
_CopyT = TypeVar("_CopyT")
WaiterFailureCode = Literal[
    "failed",
    "cancelled",
    "rolled_back",
    "superseded",
    "blocked",
    "not_serving",
    "regressed",
    "invalid_response",
    "unexpected_state",
    "timeout",
    "poll_limit",
]
_Decision = Literal["success", "retry"] | WaiterFailureCode
_MAX_SECONDS = (2**31 - 1) / 1000


def _snapshot(value: _CopyT) -> _CopyT:
    # Generated serializers sometimes use identity checks for optional fields.
    # Preserve UNSET throughout nested DTOs while isolating mutable model data.
    return copy.deepcopy(value, {id(UNSET): UNSET})


@dataclass(frozen=True, slots=True)
class WaiterProgress:
    """One valid observation. HTTP retry attempts count within this poll."""

    poll: int
    elapsed_ms: float


@dataclass(frozen=True, kw_only=True)
class WaiterOptions(RequestOptions, Generic[T]):
    """Required total timeout in seconds, including awaited progress callbacks.

    Poll immediately, then use capped exponential jitter (default 1–5 seconds).
    The explicit total budget overrides the client's timeout for status reads.
    Progress receives a separate typed model copy and immutable timing metadata.
    """

    timeout: float
    min_delay: float = 1.0
    max_delay: float = 5.0
    max_polls: int | None = None
    on_progress: Callable[[T, WaiterProgress], None | Awaitable[None]] | None = None

    def __post_init__(self) -> None:
        for name in ("timeout", "min_delay", "max_delay"):
            value = getattr(self, name)
            if (
                not isinstance(value, (int, float))
                or isinstance(value, bool)
                or not 0 < value <= _MAX_SECONDS
                or not math.isfinite(value)
            ):
                raise ValueError(f"{name} must be positive finite seconds, at most {_MAX_SECONDS}")
        RequestOptions.__post_init__(self)
        if self.min_delay > self.max_delay:
            raise ValueError("min_delay must not exceed max_delay")
        if self.max_polls is not None and (type(self.max_polls) is not int or not 1 <= self.max_polls <= 2**53 - 1):
            raise ValueError("max_polls must be a positive safe integer")
        if self.on_progress is not None and not callable(self.on_progress):
            raise TypeError("on_progress must be callable")


@dataclass(frozen=True, kw_only=True)
class BuildWaitOptions(WaiterOptions["BuildResponse"]):
    """Policy and typed progress for wait_for_build."""


@dataclass(frozen=True, kw_only=True)
class DeploymentWaitOptions(WaiterOptions["ReleaseTimeline"]):
    """Policy for wait_for_deployment; approval waits/holds stop by default."""

    stop_on_blocked: bool = True

    def __post_init__(self) -> None:
        WaiterOptions.__post_init__(self)
        if type(self.stop_on_blocked) is not bool:
            raise TypeError("stop_on_blocked must be a boolean")


class WaiterError(Exception):
    """Resource or budget outcome with the last valid typed observation.

    HTTP, network and callback failures retain their original exception identity.
    last_observed returns a copy so callers cannot change retained diagnostics.
    """

    def __init__(
        self,
        code: WaiterFailureCode,
        resource: Literal["build", "deployment"],
        id: str,
        polls: int,
        last_observed: BuildResponse | ReleaseTimeline | None = None,
    ) -> None:
        self.code = code
        self.resource = resource
        self.id = id
        self.polls = polls
        self._last_observed = _snapshot(last_observed)
        super().__init__(f"SDK {resource} wait stopped: {code}")

    @property
    def last_observed(self) -> BuildResponse | ReleaseTimeline | None:
        return _snapshot(self._last_observed)


class WaiterTimeoutError(WaiterError, RequestTimeoutError):
    """A waiter exceeded its total budget. Catch as WaiterError or TimeoutError."""


def _build_state(value: Any, id: str) -> _Decision:
    if not isinstance(value, dict) or value.get("id") != id or not isinstance(value.get("status"), str):
        return "invalid_response"
    state = value["status"]
    if state in ("queued", "running"):
        return "retry"
    if state == "succeeded":
        return "success"
    if state in ("failed", "cancelled"):
        return state
    return "unexpected_state"


def _deployment_state(value: Any, id: str, stop_on_blocked: bool) -> _Decision:
    if not isinstance(value, dict):
        return "invalid_response"
    traffic = value.get("traffic_percent")
    events = value.get("events")
    if isinstance(traffic, bool) or not isinstance(traffic, (int, float)):
        return "invalid_response"
    if (
        value.get("deployment_id") != id
        or not isinstance(value.get("phase"), str)
        or not isinstance(value.get("status"), str)
        or not 0 <= traffic <= 100
        or not math.isfinite(traffic)
        or not isinstance(events, list)
        or any(not isinstance(event, dict) or not isinstance(event.get("kind"), str) for event in events)
    ):
        return "invalid_response"
    phase = value["phase"]
    if phase in ("building", "scheduled", "rolling_out", "watching"):
        return "retry"
    if phase in ("awaiting_approval", "held"):
        return "blocked" if stop_on_blocked else "retry"
    if phase in ("failed", "cancelled", "rolled_back", "superseded"):
        return phase
    if phase == "released":
        if any(event["kind"] == "watch_regressed" for event in events):
            return "regressed"
        return "success" if value["status"] == "live" and traffic > 0 else "not_serving"
    return "unexpected_state"


def _wait(
    owner: FaaSClient,
    resource: Literal["build", "deployment"],
    id: str,
    options: WaiterOptions[T],
    *,
    kwargs: dict[str, Any],
    parse: Callable[[dict[str, Any]], T],
    decide: Callable[[Any], _Decision],
) -> Coroutine[Any, Any, T]:
    if not isinstance(id, str) or not id:
        raise TypeError("A resource id is required")
    if not isinstance(options, WaiterOptions):
        raise TypeError("options must be WaiterOptions")
    source = owner.async_httpx_client
    transport = owned_async_transport(source)
    client = snapshot_async_client(source)

    async def observe() -> T:
        loop = asyncio.get_running_loop()
        start = loop.time()
        deadline = start + options.timeout
        polls = 0
        last_observed: T | None = None
        active = True
        callback_error: BaseException | None = None

        def failure(code: WaiterFailureCode) -> WaiterError:
            kind = WaiterTimeoutError if code == "timeout" else WaiterError
            return kind(code, resource, id, polls, last_observed)

        def check() -> None:
            if not active:
                raise asyncio.CancelledError()
            if loop.time() >= deadline:
                raise failure("timeout")

        async def run() -> T:
            nonlocal polls, last_observed, callback_error
            while True:
                check()
                polls += 1

                async def read() -> tuple[T, _Decision]:
                    response = await client.request(**kwargs)
                    response.raise_for_status()
                    check()
                    try:
                        body = response.json()
                    except ValueError:
                        raise failure("invalid_response") from None
                    decision = decide(body)
                    if decision in ("invalid_response", "unexpected_state"):
                        raise failure(decision)
                    try:
                        value = parse(body)
                    except (ValueError, TypeError, KeyError, AttributeError, OverflowError):
                        raise failure("invalid_response") from None
                    return value, decision

                value, decision = await run_request(
                    transport.owner,
                    read,
                    RequestOptions(timeout=0, retry=options.retry, on_response=options.on_response),
                    0,
                )
                check()
                last_observed = _snapshot(value)
                check()
                if options.on_progress is not None:
                    try:
                        progress_value = _snapshot(value)
                        check()
                        result = options.on_progress(
                            progress_value, WaiterProgress(polls, max(0.0, (loop.time() - start) * 1000))
                        )
                        if inspect.isawaitable(result):
                            await result
                    except BaseException as error:
                        callback_error = error
                        raise
                check()
                if decision == "success":
                    return value
                if decision != "retry":
                    raise failure(decision)
                if options.max_polls is not None and polls >= options.max_polls:
                    raise failure("poll_limit")
                ceiling = min(options.max_delay, options.min_delay * 2 ** min(31, polls - 1))
                await asyncio.sleep(min(deadline - loop.time(), random.uniform(options.min_delay, ceiling)))

        try:
            # An independent scope owns the entire loop, including callbacks.
            # Actual status reads get separate scopes so callback SDK calls do
            # not inherit waiter retries/observers. Cancellation retires both.
            return await run_request(object(), run, RequestOptions(timeout=options.timeout), options.timeout)
        except RequestTimeoutError as error:
            if error is callback_error or isinstance(error, WaiterTimeoutError) or loop.time() < deadline:
                raise
            raise failure("timeout") from None
        finally:
            active = False  # Late, cancellation-resistant callbacks cannot poll again.
            await client.aclose()

    return observe()


def wait_for_build(owner: FaaSClient, id: str, options: BuildWaitOptions) -> Coroutine[Any, Any, BuildResponse]:
    if not isinstance(options, BuildWaitOptions):
        raise TypeError("options must be BuildWaitOptions")
    from .api.deployments import get_build
    from .models.build_response import BuildResponse

    return _wait(
        owner,
        "build",
        id,
        options,
        kwargs=get_build._get_kwargs(id),
        parse=BuildResponse.from_dict,
        decide=lambda value: _build_state(value, id),
    )


def wait_for_deployment(
    owner: FaaSClient, id: str, options: DeploymentWaitOptions
) -> Coroutine[Any, Any, ReleaseTimeline]:
    if not isinstance(options, DeploymentWaitOptions):
        raise TypeError("options must be DeploymentWaitOptions")
    from .api.deployments import get_release_timeline
    from .models.release_timeline import ReleaseTimeline

    return _wait(
        owner,
        "deployment",
        id,
        options,
        kwargs=get_release_timeline._get_kwargs(id),
        parse=ReleaseTimeline.from_dict,
        decide=lambda value: _deployment_state(value, id, options.stop_on_blocked),
    )
