from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recovery_execution_source import EventRecoveryExecutionSource, check_event_recovery_execution_source
from ..models.event_recovery_execution_state import EventRecoveryExecutionState, check_event_recovery_execution_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryExecution")


@_attrs_define
class EventRecoveryExecution:
    """Read-only observation of this job's admitted replay generation. Later replays are not attributed to this item.
    Missing history, untracked legacy items, or uncertain outcomes report unknown. Omitted for routing recovery and
    items not admitted.

    """

    observed_at: datetime.datetime
    state: EventRecoveryExecutionState
    source: EventRecoveryExecutionSource
    attempts: int
    """Recorded dispatch attempt count for the tracked generation."""
    completed_at: datetime.datetime | Unset = UNSET
    """Recorded terminal completion time when available."""

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        state: str = self.state

        source: str = self.source

        attempts = self.attempts

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
            completed_at=completed_at,
        )

        return event_recovery_execution
