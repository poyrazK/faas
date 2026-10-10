from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recovery_execution_evidence_source import (
    EventRecoveryExecutionEvidenceSource,
    check_event_recovery_execution_evidence_source,
)
from ..models.event_recovery_execution_source import EventRecoveryExecutionSource, check_event_recovery_execution_source
from ..models.event_recovery_execution_state import EventRecoveryExecutionState, check_event_recovery_execution_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryExecution")


@_attrs_define
class EventRecoveryExecution:
    """Read-only observation of this job's exact admitted replay generation. Saved confirmed terminal results take
    precedence and survive execution-history pruning until recovery job retention. Later replays never replace them.
    Missing evidence, untracked legacy items, or uncertain outcomes report unknown. Omitted for routing recovery and
    items not admitted.

    """

    observed_at: datetime.datetime
    state: EventRecoveryExecutionState
    source: EventRecoveryExecutionSource
    attempts: int
    """Recorded dispatch attempt count for the tracked generation."""
    recorded_at: datetime.datetime | Unset = UNSET
    """When terminal evidence was saved; present only for recovery_result."""
    evidence_source: EventRecoveryExecutionEvidenceSource | Unset = UNSET
    """Original source of a saved terminal result; present only for recovery_result."""
    completed_at: datetime.datetime | Unset = UNSET
    """Recorded terminal completion time when available."""

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        state: str = self.state

        source: str = self.source

        attempts = self.attempts

        recorded_at: str | Unset = UNSET
        if not isinstance(self.recorded_at, Unset):
            recorded_at = self.recorded_at.isoformat()

        evidence_source: str | Unset = UNSET
        if not isinstance(self.evidence_source, Unset):
            evidence_source = self.evidence_source

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "state": state,
                "source": source,
                "attempts": attempts,
            }
        )
        if recorded_at is not UNSET:
            field_dict["recorded_at"] = recorded_at
        if evidence_source is not UNSET:
            field_dict["evidence_source"] = evidence_source
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        state = check_event_recovery_execution_state(d.pop("state"))

        source = check_event_recovery_execution_source(d.pop("source"))

        attempts = d.pop("attempts")

        _recorded_at = d.pop("recorded_at", UNSET)
        recorded_at: datetime.datetime | Unset
        if isinstance(_recorded_at, Unset):
            recorded_at = UNSET
        else:
            recorded_at = datetime.datetime.fromisoformat(_recorded_at)

        _evidence_source = d.pop("evidence_source", UNSET)
        evidence_source: EventRecoveryExecutionEvidenceSource | Unset
        if isinstance(_evidence_source, Unset):
            evidence_source = UNSET
        else:
            evidence_source = check_event_recovery_execution_evidence_source(_evidence_source)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        event_recovery_execution = cls(
            observed_at=observed_at,
            state=state,
            source=source,
            attempts=attempts,
            recorded_at=recorded_at,
            evidence_source=evidence_source,
            completed_at=completed_at,
        )

        return event_recovery_execution
