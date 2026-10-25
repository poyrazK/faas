from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_check_attempt_outcome import (
    AutomationCheckAttemptOutcome,
    check_automation_check_attempt_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationCheckAttempt")


@_attrs_define
class AutomationCheckAttempt:
    """Expected ordered simulation attempt outcome and optional failure status."""

    outcome: AutomationCheckAttemptOutcome
    http_status: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        http_status = self.http_status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome": outcome,
            }
        )
        if http_status is not UNSET:
            field_dict["http_status"] = http_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome = check_automation_check_attempt_outcome(d.pop("outcome"))

        http_status = d.pop("http_status", UNSET)

        automation_check_attempt = cls(
            outcome=outcome,
            http_status=http_status,
        )

        return automation_check_attempt
