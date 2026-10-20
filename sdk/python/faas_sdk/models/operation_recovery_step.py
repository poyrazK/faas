from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationRecoveryStep")


@_attrs_define
class OperationRecoveryStep:
    """Ordered native step summary from the captured workflow contract and durable step state."""

    name: str
    state: str
    attempt: int
    confirmed: bool
    outcome_unknown: bool
    """A dispatched action lacks confirmed success; operator reconciliation must establish its external effects."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        state = self.state

        attempt = self.attempt

        confirmed = self.confirmed

        outcome_unknown = self.outcome_unknown

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "state": state,
                "attempt": attempt,
                "confirmed": confirmed,
                "outcome_unknown": outcome_unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        state = d.pop("state")

        attempt = d.pop("attempt")

        confirmed = d.pop("confirmed")

        outcome_unknown = d.pop("outcome_unknown")

        operation_recovery_step = cls(
            name=name,
            state=state,
            attempt=attempt,
            confirmed=confirmed,
            outcome_unknown=outcome_unknown,
        )

        return operation_recovery_step
