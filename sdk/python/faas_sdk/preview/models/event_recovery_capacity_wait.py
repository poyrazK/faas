from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recovery_capacity_wait_gate import (
    EventRecoveryCapacityWaitGate,
    check_event_recovery_capacity_wait_gate,
)
from ..models.event_recovery_capacity_wait_scope import (
    EventRecoveryCapacityWaitScope,
    check_event_recovery_capacity_wait_scope,
)

T = TypeVar("T", bound="EventRecoveryCapacityWait")


@_attrs_define
class EventRecoveryCapacityWait:
    """Current capacity-wait episode with limiting scope and observation timestamps."""

    scope: EventRecoveryCapacityWaitScope
    gate: EventRecoveryCapacityWaitGate
    explanation: str
    """Safe explanation of the observed admission gate; does not include payloads or resolved work keys."""
    started_at: datetime.datetime
    observed_at: datetime.datetime
    age_seconds: float

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        gate: str = self.gate

        explanation = self.explanation

        started_at = self.started_at.isoformat()

        observed_at = self.observed_at.isoformat()

        age_seconds = self.age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
                "gate": gate,
                "explanation": explanation,
                "started_at": started_at,
                "observed_at": observed_at,
                "age_seconds": age_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = check_event_recovery_capacity_wait_scope(d.pop("scope"))

        gate = check_event_recovery_capacity_wait_gate(d.pop("gate"))

        explanation = d.pop("explanation")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        age_seconds = d.pop("age_seconds")

        event_recovery_capacity_wait = cls(
            scope=scope,
            gate=gate,
            explanation=explanation,
            started_at=started_at,
            observed_at=observed_at,
            age_seconds=age_seconds,
        )

        return event_recovery_capacity_wait
