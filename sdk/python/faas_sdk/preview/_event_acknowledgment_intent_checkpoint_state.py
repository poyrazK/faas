"""Process-local confirmation proof. Timers and unfinished proof never enter checkpoints."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from ._event_acknowledgment_intent_watch_policy import Loss
from .event_acknowledgment_intent_checkpoint import EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX, MAX_SEQUENCE
from .event_acknowledgment_intent_health import EventAcknowledgmentIntentHealth


@dataclass
class Proof:
    first: int | None = None
    samples: int = 0
    mode: str | None = None
    resume: bool = False

    def reset(self) -> None:
        self.first, self.samples, self.mode = None, 0, None


def reset_proofs(checkpoint: dict[str, Any]) -> list[Proof]:
    proofs = []
    for condition in checkpoint["conditions"]:
        condition["state"] = "unknown"
        proofs.append(Proof(resume="incident_id" in condition))
    return proofs


def confirmation(
    condition: dict[str, Any], proof: Proof, local: int, sustain: int, reason: str, blocked: bool = False
) -> dict[str, Any]:
    elapsed = 0 if proof.first is None else max(0, local - proof.first) / 1000000
    phase = "resume_pending" if proof.resume else condition["state"]
    if proof.first is not None:
        phase = (
            "notification_blocked" if blocked else "recovery_pending" if proof.mode == "recovery" else "warning_pending"
        )
    return {
        "metric": condition["metric"],
        "phase": phase,
        "reason": reason,
        "fresh_samples": proof.samples,
        "required_samples": 2,
        "elapsed_ms": elapsed,
        "required_ms": sustain,
        "remaining_ms": 0 if proof.first is None else max(0, sustain - elapsed),
    }


def _notification(checkpoint: dict[str, Any], condition: dict[str, Any], transition: str, at: str) -> dict[str, Any]:
    checkpoint["sequence"] += 1
    if transition == "opened":
        condition["incident_id"] = checkpoint["session"] + "-" + str(checkpoint["sequence"])
    out = {
        "sequence": checkpoint["sequence"],
        "incident_id": condition["incident_id"],
        "metric": condition["metric"],
        "transition": transition,
        "at": at,
        "value": condition["value"],
        "threshold": condition["threshold"],
    }
    checkpoint["pending"].append(out)
    return out


def _apply(
    checkpoint: dict[str, Any], condition: dict[str, Any], proof: Proof, local: int, at: str
) -> tuple[str, bool, dict[str, Any] | None]:
    active, breach = "incident_id" in condition, condition["value"] >= condition["threshold"]
    candidate = proof.resume or not breach if active else breach
    if not candidate:
        proof.reset()
        condition["state"] = "firing" if active else "clear"
        return ("threshold_not_cleared" if active else "threshold_not_breached"), False, None
    mode = "recovery" if active and not breach else "warning"
    if mode != proof.mode:
        proof.reset()
    proof.mode = mode
    if proof.first is None:
        proof.first = local
    proof.samples = min(2, proof.samples + 1)
    condition["state"] = "firing" if active and not proof.resume else "unknown"
    if proof.samples < 2 or local - proof.first < checkpoint["policy"]["sustain_ms"] * 1000000:
        return mode + "_confirmation_pending", False, None
    if not active:
        reserved = sum("incident_id" in current for current in checkpoint["conditions"])
        if len(checkpoint["pending"]) + reserved + 2 > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX:
            return "notification_outbox_full", True, None
        if checkpoint["sequence"] > MAX_SEQUENCE - reserved - 2:
            return "notification_sequence_exhausted", True, None
        alert = _notification(checkpoint, condition, "opened", at)
        condition["state"], proof.resume = "firing", False
        proof.reset()
        return "threshold_not_cleared", False, alert
    if not breach:
        alert = _notification(checkpoint, condition, "resolved", at)
        del condition["incident_id"]
        condition["state"], proof.resume = "clear", False
        proof.reset()
        return "threshold_not_breached", False, alert
    condition["state"], proof.resume = "firing", False
    proof.reset()
    return "incident_reconfirmed", False, None


def observe(
    checkpoint: dict[str, Any],
    proofs: list[Proof],
    health: EventAcknowledgmentIntentHealth | None,
    reason: Loss | None,
    local: int,
    at: str,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    alerts, confirmations = [], []
    for condition, proof in zip(checkpoint["conditions"], proofs, strict=True):
        if reason is not None:
            proof.reset()
            condition["state"], proof.resume = "unknown", "incident_id" in condition
            confirmations.append(confirmation(condition, proof, local, checkpoint["policy"]["sustain_ms"], reason))
            continue
        condition["value"] = (
            health.due
            if condition["metric"] == "due_backlog"
            else health.stalled
            if condition["metric"] == "stalled_recovery"
            else health.parked
        )
        condition["observed_at"] = health.observed_at
        why, blocked, alert = _apply(checkpoint, condition, proof, local, at)
        if alert is not None:
            alerts.append(alert)
        confirmations.append(confirmation(condition, proof, local, checkpoint["policy"]["sustain_ms"], why, blocked))
    return alerts, confirmations
