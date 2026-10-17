"""Opt-in checkpointed recovery monitoring. Applications persist before notifying and fence every writer."""

from __future__ import annotations

import asyncio
import inspect
import time
from collections.abc import Awaitable, Callable
from copy import deepcopy
from dataclasses import asdict, dataclass
from typing import Any
from uuid import uuid4

from ._event_acknowledgment_intent_checkpoint_state import confirmation, observe, reset_proofs
from ._event_acknowledgment_intent_watch_policy import EventAcknowledgmentIntentWatchOptions, evidence, policy
from ._event_acknowledgment_protocol import (
    Control,
    EventAcknowledgmentWorkerCancellation,
    EventAcknowledgmentWorkerCancelled,
    owned_operation,
    pause,
)
from .event_acknowledgment_intent_checkpoint import (
    KIND,
    MAX_SEQUENCE,
    acknowledge_event_acknowledgment_intent_alerts,
    checkpoint_time,
    rules,
    validate_event_acknowledgment_intent_checkpoint,
)
from .event_acknowledgment_intent_watch import ConnectionFactory, _at, _read_health
from .event_consumer_transactions import _uuid

Progress = Callable[[dict[str, Any], EventAcknowledgmentWorkerCancellation], Awaitable[None] | None]
Acknowledgments = Callable[
    [dict[str, Any], EventAcknowledgmentWorkerCancellation], Awaitable[list[dict[str, Any]]] | list[dict[str, Any]]
]


@dataclass(frozen=True)
class EventAcknowledgmentIntentCheckpointWatchOptions(EventAcknowledgmentIntentWatchOptions):
    resume: Any = None
    on_progress: Progress | None = None
    poll_acknowledgments: Acknowledgments | None = None


class EventAcknowledgmentIntentCheckpointWatchCallbackError(RuntimeError):
    """A detached snapshot is available for fenced persistence/reconciliation. No raw cause."""

    code = "event_acknowledgment_intent_checkpoint_callback_failed"

    def __init__(self, phase: str, result: dict[str, Any]):
        super().__init__(
            "checkpointed recovery watch callback failed; reconcile result['checkpoint'] before notification retry"
        )
        self.phase, self.result = phase, deepcopy(result)


class EventAcknowledgmentIntentCheckpointWatchCancelled(asyncio.CancelledError):
    """Native cancellation category with detached pending/incident state for fenced shutdown persistence."""

    def __init__(self, result: dict[str, Any]):
        super().__init__("checkpointed recovery watch interrupted")
        self.result = deepcopy(result)


async def awatch_event_acknowledgment_intent_health_with_checkpoint(
    connection_factory: ConnectionFactory,
    account_id: str,
    app_id: str,
    options: EventAcknowledgmentIntentCheckpointWatchOptions,
    *,
    stop_event: asyncio.Event | None = None,
) -> dict[str, Any]:
    """Explicit durable monitoring mode; no storage, transport, tables or background host are installed.

    on_progress must atomically persist its checkpoint before any pending notification.
    Resume/offline acknowledgments require one ownership-fenced serialized writer.
    """
    if (
        not callable(connection_factory)
        or not isinstance(options, EventAcknowledgmentIntentCheckpointWatchOptions)
        or not callable(options.on_progress)
        or options.poll_acknowledgments is not None
        and not callable(options.poll_acknowledgments)
        or stop_event is not None
        and not isinstance(stop_event, asyncio.Event)
    ):
        raise ValueError("invalid checkpointed acknowledgment intent watch options")
    account, app = _uuid(account_id), _uuid(app_id)
    settings = policy(options)
    progress, poll_acknowledgments = options.on_progress, options.poll_acknowledgments
    checkpoint = (
        {
            "kind": KIND,
            "version": 1,
            "target": {"account_id": account, "app_id": app},
            "policy": asdict(settings),
            "session": str(uuid4()),
            "sequence": 0,
            "conditions": [{**rule, "state": "unknown"} for rule in rules(settings)],
            "pending": [],
            "acknowledged": [],
        }
        if options.resume is None
        else validate_event_acknowledgment_intent_checkpoint(options.resume)
    )
    if checkpoint["target"] != {"account_id": account, "app_id": app} or checkpoint["policy"] != asdict(settings):
        raise ValueError("intent checkpoint owner or effective policy mismatch")
    proofs = reset_proofs(checkpoint)
    result: dict[str, Any] = {
        "stop_reason": "watching",
        "observations": 0,
        "evidence_status": "unknown",
        "evidence_reason": "awaiting_observation",
        "checkpoint": checkpoint,
        "alerts": [],
        "confirmations": [
            confirmation(condition, proof, 0, settings.sustain_ms, "fresh_confirmation_required")
            for condition, proof in zip(checkpoint["conditions"], proofs, strict=True)
        ],
    }

    async def watch(control: Control) -> dict[str, Any]:
        nonlocal checkpoint
        previous_observed = (
            None if "last_observed_at" not in checkpoint else checkpoint_time(checkpoint["last_observed_at"])
        )
        previous_poll = None

        async def invoke(phase: str) -> None:
            nonlocal checkpoint
            control.check()
            failed = False
            try:
                callback = progress if phase == "progress" else poll_acknowledgments
                payload = result if phase == "progress" else checkpoint
                returned = callback(deepcopy(payload), EventAcknowledgmentWorkerCancellation(control))
                if inspect.isawaitable(returned):
                    returned = await returned
            except (Exception, asyncio.CancelledError):
                failed = True
            control.check()
            if not failed and phase == "acknowledgments":
                try:
                    updated = acknowledge_event_acknowledgment_intent_alerts(checkpoint, returned)
                except Exception:
                    failed = True
                else:
                    checkpoint = updated
                    result["checkpoint"] = checkpoint
            if failed:
                result["stop_reason"] = "callback_failure"
                raise EventAcknowledgmentIntentCheckpointWatchCallbackError(phase, result)

        async def acknowledge() -> None:
            if poll_acknowledgments is not None:
                await invoke("acknowledgments")

        control.check()
        await acknowledge()
        await invoke("progress")  # Persist/replay pending history before the first health query.
        while True:
            control.check()
            health = await _read_health(connection_factory, account, app, settings, control)
            control.check()
            wall, local = time.time_ns(), time.monotonic_ns()
            max_age = settings.max_age_ms * 1000000
            reason = evidence(health, previous_observed, previous_poll, wall, local, max_age)
            previous_poll = local
            observed = None if health is None else checkpoint_time(health.observed_at)
            if (
                observed is not None
                and observed <= wall
                and wall - observed <= max_age
                and (previous_observed is None or observed > previous_observed)
            ):
                previous_observed = observed
                checkpoint["last_observed_at"] = health.observed_at
            result["observations"] = min(MAX_SEQUENCE, result["observations"] + 1)
            result["evidence_status"] = "eligible" if reason is None else "unknown"
            result["evidence_reason"] = "complete_retained_intents" if reason is None else reason
            if health is None:
                result.pop("health", None)
            else:
                result["health"] = asdict(health)
            result["alerts"], result["confirmations"] = observe(checkpoint, proofs, health, reason, local, _at(wall))
            result["checkpoint"] = checkpoint
            await invoke("progress")
            await pause(settings.poll_interval_ms, control)
            result["alerts"] = []
            await acknowledge()

    interrupted = False
    try:
        return await owned_operation(watch, stop_event)
    except EventAcknowledgmentWorkerCancelled:
        interrupted = True
    if interrupted:
        result.update(stop_reason="interrupted", evidence_status="unknown", evidence_reason="interrupted", alerts=[])
        proofs = reset_proofs(checkpoint)
        result["checkpoint"] = checkpoint
        result["confirmations"] = [
            confirmation(condition, proof, 0, settings.sustain_ms, "interrupted")
            for condition, proof in zip(checkpoint["conditions"], proofs, strict=True)
        ]
        raise EventAcknowledgmentIntentCheckpointWatchCancelled(result)
