"""Read-only async deadline watches with fresh proof and a durable notification outbox."""

from __future__ import annotations

import asyncio
import json
import time
from collections.abc import Awaitable, Callable
from copy import deepcopy
from dataclasses import dataclass
from typing import Any
from uuid import UUID, uuid4

from ._event_deadline_policy import (
    MAX_COUNT,
    QUEUE_MAX,
    EventDeadlinePolicyOptions,
    EventDeadlineTarget,
    deadline_boundaries,
    deadline_evidence,
    duration,
    policy_dict,
    ratio_value,
    target_dict,
    timestamp,
)
from .api.events import get_event_subscription_deadline_window
from .client import AuthenticatedClient
from .event_deadline_checkpoint import KIND, acknowledge_event_deadline_alerts, validate_event_deadline_checkpoint


@dataclass(frozen=True)
class EventDeadlineWatchOptions(EventDeadlinePolicyOptions):
    interval_ms: int = 5000
    timeout_ms: int = 600000
    max_age_ms: int = 30000
    request_timeout_ms: int = 10000
    resume: dict[str, Any] | None = None
    on_progress: Callable[[dict[str, Any]], Awaitable[None]] | None = None
    poll_acknowledgements: Callable[[dict[str, Any]], Awaitable[list[dict[str, Any]]]] | None = None


class EventDeadlineWatchCallbackError(RuntimeError):
    """Callback failure with detached state available for persistence/retry."""

    def __init__(self, result: dict[str, Any]):
        super().__init__("deadline watch callback failed; persist result['checkpoint'] before notification retry")
        self.result = deepcopy(result)


class EventDeadlineWatchCancelled(asyncio.CancelledError):
    """Task cancellation remains cancellation, while exposing the latest checkpoint."""

    def __init__(self, result: dict[str, Any]):
        super().__init__("deadline watch interrupted")
        self.result = deepcopy(result)


@dataclass
class _Proof:
    first: float | None = None
    samples: int = 0
    last_observed: int | None = None
    last_local: float | None = None
    mode: str | None = None
    resume: bool = False

    def reset(self) -> None:
        self.first, self.samples = None, 0


def _confirmation(
    c: dict[str, Any], p: _Proof, now: float, policy: dict[str, Any], reason: str, blocked: bool = False
) -> dict[str, Any]:
    recovering = "incident_id" in c and deadline_boundaries(c)[1]
    required = policy["recovery_sustain_ms"] if recovering else policy["sustain_ms"]
    elapsed = 0 if p.first is None else max(0, now - p.first)
    phase = "resume_pending" if p.resume else c["state"]
    if p.first is not None:
        phase = "notification_blocked" if blocked else "recovery_pending" if recovering else "warning_pending"
    return {
        "metric": c["metric"],
        "phase": phase,
        "reason": reason,
        "fresh_samples": p.samples,
        "required_samples": 2,
        "elapsed_ms": elapsed,
        "required_ms": required,
        "remaining_ms": 0 if p.first is None else max(0, required - elapsed),
    }


class _Watch:
    def __init__(self, target: EventDeadlineTarget, options: EventDeadlineWatchOptions):
        self.target, self.policy = target_dict(target), policy_dict(options)
        if (
            not isinstance(options, EventDeadlineWatchOptions)
            or not duration(options.interval_ms, 60000)
            or any(not duration(v) for v in (options.timeout_ms, options.max_age_ms, options.request_timeout_ms))
            or (options.on_progress is not None and not callable(options.on_progress))
            or (options.poll_acknowledgements is not None and not callable(options.poll_acknowledgements))
        ):
            raise ValueError("invalid deadline watch timing/callback options")
        self.interval, self.timeout = options.interval_ms, options.timeout_ms
        self.max_age, self.request_timeout = options.max_age_ms, options.request_timeout_ms
        self.on_progress, self.poll_acknowledgements = options.on_progress, options.poll_acknowledgements
        self.checkpoint = (
            validate_event_deadline_checkpoint(options.resume)
            if options.resume is not None
            else {
                "kind": KIND,
                "version": 2 if any("minimum_recipients" in rule for rule in self.policy["rules"]) else 1,
                "target": self.target,
                "policy": self.policy,
                "session": str(uuid4()),
                "sequence": 0,
                "conditions": [{**r, "state": "unknown"} for r in self.policy["rules"]],
                "pending": [],
                "acknowledged": [],
            }
        )
        if self.checkpoint["target"] != self.target or self.checkpoint["policy"] != self.policy:
            raise ValueError("deadline checkpoint target or effective policy mismatch")
        self.proofs = [
            _Proof(last_observed=timestamp(c.get("observed_at")), resume="incident_id" in c)
            for c in self.checkpoint["conditions"]
        ]
        for c in self.checkpoint["conditions"]:
            c["state"] = "unknown"
        self.result: dict[str, Any] = {
            "stop_reason": "watching",
            "observations": 0,
            "evidence_status": "unknown",
            "evidence_reason": "awaiting_observation",
            "checkpoint": self.checkpoint,
            "alerts": [],
            "confirmations": [
                _confirmation(c, p, 0, self.policy, "fresh_confirmation_required")
                for c, p in zip(self.checkpoint["conditions"], self.proofs, strict=True)
            ],
        }

    def emit(self, c: dict[str, Any], transition: str) -> None:
        self.checkpoint["sequence"] += 1
        if transition == "opened":
            c["incident_id"] = f"{self.checkpoint['session']}-{self.checkpoint['sequence']}"
        alert = {
            "sequence": self.checkpoint["sequence"],
            "incident_id": c["incident_id"],
            "metric": c["metric"],
            "transition": transition,
            "at": c["observed_at"],
            "value": c["value"],
            "threshold": c["threshold"],
            "recovery_threshold": c["recovery_threshold"],
        }
        if "ratio_sample" in c:
            alert["ratio_sample"] = dict(c["ratio_sample"])
        self.checkpoint["pending"].append(alert)
        self.result["alerts"].append(alert)

    def apply(self, c: dict[str, Any], p: _Proof, now: float) -> tuple[str, bool]:
        active = "incident_id" in c
        breached, recovered = deadline_boundaries(c)
        candidate = (breached or recovered if p.resume else recovered) if active else breached
        if not candidate:
            p.reset()
            c["state"] = ("unknown" if p.resume else "firing") if active else "clear"
            return (
                "fresh_confirmation_required" if p.resume else "recovery_boundary_not_confirmed"
            ) if active else "threshold_not_breached", False
        mode = "recovery" if active and recovered else "warning"
        if p.mode != mode:
            p.reset()
        p.mode = mode
        if p.first is None:
            p.first = now
        p.samples += 1
        required = self.policy["recovery_sustain_ms"] if mode == "recovery" else self.policy["sustain_ms"]
        c["state"] = "firing" if active and not p.resume else "unknown"
        if p.samples < 2 or now - p.first < required:
            return mode + "_confirmation_pending", False
        if not active:
            reserved = sum("incident_id" in row for row in self.checkpoint["conditions"])
            if (
                len(self.checkpoint["pending"]) + reserved + 2 > QUEUE_MAX
                or self.checkpoint["sequence"] > MAX_COUNT - reserved - 2
            ):
                return "notification_outbox_full", True
            self.emit(c, "opened")
            c["state"], p.resume = "firing", False
            p.reset()
            return "recovery_boundary_not_confirmed", False
        if recovered:
            self.emit(c, "resolved")
            del c["incident_id"]
            c["state"], p.resume = "clear", False
            p.reset()
            return "threshold_not_breached", False
        c["state"], p.resume = "firing", False
        p.reset()
        return "recovery_boundary_not_confirmed", False

    def observe(self, evidence: Any, reason: str) -> None:
        now, wall = time.monotonic_ns() / 1000000, time.time_ns()
        observed = (
            timestamp(evidence.get("summary", {}).get("observed_at"))
            if isinstance(evidence, dict) and isinstance(evidence.get("summary"), dict)
            else None
        )
        if not reason and not deadline_evidence(evidence, self.target, self.policy["within_ms"]):
            reason = "invalid_or_incomplete_deadline_evidence"
        if not reason and observed is not None:
            if observed > wall:
                reason = "future_observation"
            elif wall - observed > self.max_age * 1000000:
                reason = "stale_observation"
        self.result["observations"] += 1
        self.result.update(
            alerts=[],
            confirmations=[],
            evidence_status="unknown" if reason else "eligible",
            evidence_reason=reason or "complete_deadline_window",
        )
        if reason:
            self.result.pop("evidence", None)
        else:
            self.result["evidence"] = deepcopy(evidence)
        for c, p in zip(self.checkpoint["conditions"], self.proofs, strict=True):
            loss = reason
            if not loss and observed is not None and p.last_observed is not None and observed <= p.last_observed:
                loss = "repeated_observation" if observed == p.last_observed else "out_of_order_observation"
            if not loss and p.last_local is not None and now - p.last_local > self.max_age:
                loss = "sample_gap"
            if (
                not loss
                and "minimum_recipients" in c
                and evidence["summary"]["deadline_recipients"] < c["minimum_recipients"]
            ):
                loss = "insufficient_deadline_recipients"
            if (not loss or loss == "sample_gap") and observed is not None:
                p.last_observed, p.last_local = observed, now
            if loss:
                p.reset()
                c["state"], p.resume = "unknown", "incident_id" in c
                self.result["confirmations"].append(_confirmation(c, p, now, self.policy, loss))
                continue
            if "minimum_recipients" in c:
                c["ratio_sample"] = {
                    "expired_recipients": evidence["summary"]["already_expired_count"],
                    "deadline_recipients": evidence["summary"]["deadline_recipients"],
                }
                c["value"] = ratio_value(c["ratio_sample"])
            elif "reason" in c:
                c["value"] = evidence[c["cohort"] + "_waiting_reasons"][c["reason"]]
            else:
                c["value"] = (
                    evidence["upcoming_recipients"]
                    if c["cohort"] == "upcoming"
                    else evidence["summary"]["already_expired_count"]
                )
            c["observed_at"] = evidence["summary"]["observed_at"]
            why, blocked = self.apply(c, p, now)
            self.result["confirmations"].append(_confirmation(c, p, now, self.policy, why, blocked))
        self.result["checkpoint"] = self.checkpoint

    async def acknowledgements(self) -> None:
        if self.poll_acknowledgements is not None:
            try:
                pairs = await self.poll_acknowledgements(deepcopy(self.checkpoint))
                self.checkpoint = acknowledge_event_deadline_alerts(self.checkpoint, pairs)
                self.result["checkpoint"] = self.checkpoint
            except Exception as cause:
                self.result["stop_reason"] = "callback_failure"
                raise EventDeadlineWatchCallbackError(self.result) from cause

    async def progress(self) -> None:
        if self.on_progress is not None:
            try:
                await self.on_progress(deepcopy(self.result))
            except Exception as cause:
                self.result["stop_reason"] = "callback_failure"
                raise EventDeadlineWatchCallbackError(self.result) from cause


async def awatch_event_subscription_deadlines(
    client: AuthenticatedClient,
    target: EventDeadlineTarget,
    options: EventDeadlineWatchOptions,
) -> dict[str, Any]:
    """Watch full deadline counts. Persist progress checkpoints before sending notifications.

    Timeout returns the latest result. Task cancellation raises EventDeadlineWatchCancelled
    (a CancelledError) with result/checkpoint attached. Callbacks are awaited serially.
    """
    watch = _Watch(target, options)
    deadline = time.monotonic_ns() / 1000000 + watch.timeout
    try:
        while time.monotonic_ns() / 1000000 < deadline:
            await watch.acknowledgements()
            remaining = deadline - time.monotonic_ns() / 1000000
            if remaining <= 0:
                break
            evidence, reason = None, ""
            try:
                async with asyncio.timeout(min(watch.request_timeout, remaining) / 1000):
                    response = await get_event_subscription_deadline_window.asyncio_detailed(
                        watch.target["app"],
                        UUID(watch.target["subscription_id"]),
                        client=client,
                        within_millis=watch.policy["within_ms"],
                    )
                    if response.status_code != 200:
                        reason = "deadline_read_failed"
                    else:
                        # Read the original JSON: generated datetimes round timestamps to microseconds.
                        evidence = json.loads(response.content)
            except Exception:
                reason = "deadline_read_failed"
            if time.monotonic_ns() / 1000000 >= deadline:
                break
            watch.observe(evidence, reason)
            await watch.progress()
            await asyncio.sleep(max(0, min(watch.interval, deadline - time.monotonic_ns() / 1000000)) / 1000)
    except asyncio.CancelledError as cause:
        watch.result["stop_reason"] = "interrupted"
        raise EventDeadlineWatchCancelled(watch.result) from cause
    watch.result["stop_reason"] = "timed_out"
    return deepcopy(watch.result)
