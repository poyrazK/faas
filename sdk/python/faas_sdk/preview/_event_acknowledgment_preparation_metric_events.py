"""Closed metric labels and getter-free current-event validation."""

from __future__ import annotations

from types import MemberDescriptorType
from typing import Any, NoReturn, get_args

from .event_acknowledgment_preparation_events import (
    EventAcknowledgmentPreparationOutcome,
    EventAcknowledgmentPreparationReason,
    EventAcknowledgmentPreparationStage,
    _ClaimAcquired,
    _CommitUnknown,
    _Event,
    _IntentCommitted,
    _PreparationFinished,
    _PreparationStarted,
    _RenewalFailed,
    _Shutdown,
    _Started,
)

_MAX = 9007199254740991
_INVALID_EVENT = "invalid acknowledgment preparation metrics event"
_KINDS = (
    "started",
    "claim_acquired",
    "preparation_started",
    "preparation_finished",
    "renewal_failed",
    "intent_committed",
    "commit_unknown",
    "shutdown",
)
_OUTCOMES = get_args(EventAcknowledgmentPreparationOutcome)
_REASONS = get_args(EventAcknowledgmentPreparationReason)
_STAGES = get_args(EventAcknowledgmentPreparationStage)
_BASE = ("kind", "elapsed_ms")
_FIELDS = frozenset((*_BASE, "outcome", "duration_ms", "lease", "reason", "replayed", "stage"))
_SHAPES = (
    (_Started, _BASE),
    (_ClaimAcquired, _BASE),
    (_PreparationStarted, _BASE),
    (_PreparationFinished, (*_BASE, "outcome", "duration_ms")),
    (_RenewalFailed, (*_BASE, "lease", "reason")),
    (_IntentCommitted, (*_BASE, "replayed")),
    (_CommitUnknown, (*_BASE, "stage")),
    (_Shutdown, (*_BASE, "stage", "outcome", "reason")),
)
# Capture only the SDK's native slot descriptors, not descriptors from inputs.
_SLOTS = tuple(
    (variant, tuple((name, (_Event if name == "elapsed_ms" else variant).__dict__[name]) for name in names))
    for variant, names in _SHAPES
)


def _invalid() -> NoReturn:
    raise ValueError(_INVALID_EVENT)


def _copy(event: object) -> dict[str, Any]:
    if type(event) is dict:
        if not 2 <= len(event) <= 5:
            _invalid()
        data = {}
        for key, value in dict.items(event):
            if type(key) is not str or key not in _FIELDS:
                _invalid()
            data[key] = value
        return data
    for variant, slots in _SLOTS:
        if type(event) is variant:
            data = {}
            for name, descriptor in slots:
                if type(descriptor) is not MemberDescriptorType:
                    _invalid()
                data[name] = descriptor.__get__(event, variant)
            return data
    _invalid()


def _label(value: Any, labels: tuple[str, ...]) -> bool:
    return type(value) is str and value in labels


def _milliseconds(value: Any) -> bool:
    return type(value) is int and 0 <= value <= _MAX


def _shape(data: dict[str, Any], required: tuple[str, ...], optional: tuple[str, ...] = ()) -> None:
    if any(key not in data for key in required) or any(key not in required and key not in optional for key in data):
        _invalid()


def _validated(event: object) -> dict[str, Any]:
    data = _copy(event)
    if not _label(data.get("kind"), _KINDS) or not _milliseconds(data.get("elapsed_ms")):
        _invalid()
    kind = data["kind"]
    if kind in ("started", "claim_acquired", "preparation_started"):
        _shape(data, _BASE)
    elif kind == "preparation_finished":
        _shape(data, (*_BASE, "outcome", "duration_ms"))
        if not _label(data["outcome"], ("returned", "threw")) or not _milliseconds(data["duration_ms"]):
            _invalid()
    elif kind == "renewal_failed":
        _shape(data, (*_BASE, "lease", "reason"))
        if not _label(data["lease"], ("local", "platform")) or not _label(data["reason"], _REASONS):
            _invalid()
    elif kind == "intent_committed":
        _shape(data, (*_BASE, "replayed"))
        if type(data["replayed"]) is not bool:
            _invalid()
    elif kind == "commit_unknown":
        _shape(data, (*_BASE, "stage"))
        if not _label(data["stage"], _STAGES):
            _invalid()
    else:
        _shape(data, (*_BASE, "stage", "outcome"), ("reason",))
        if (
            not _label(data["stage"], _STAGES)
            or not _label(data["outcome"], _OUTCOMES)
            or data.get("reason") is not None
            and not _label(data["reason"], _REASONS)
        ):
            _invalid()
    return data
