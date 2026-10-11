from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AutomationHealthStepFailure")


@_attrs_define
class AutomationHealthStepFailure:
    """Number of terminal runs that failed at this logical step; loop items are grouped by parent."""

    step_name: str
    failed_run_count: int
    last_failed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        step_name = self.step_name

        failed_run_count = self.failed_run_count

        last_failed_at = self.last_failed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step_name": step_name,
                "failed_run_count": failed_run_count,
                "last_failed_at": last_failed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        step_name = d.pop("step_name")

        failed_run_count = d.pop("failed_run_count")

        last_failed_at = datetime.datetime.fromisoformat(d.pop("last_failed_at"))

        automation_health_step_failure = cls(
            step_name=step_name,
            failed_run_count=failed_run_count,
            last_failed_at=last_failed_at,
        )

        return automation_health_step_failure
