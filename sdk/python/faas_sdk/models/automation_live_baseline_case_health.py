from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.automation_live_baseline_case_health_status import (
    AutomationLiveBaselineCaseHealthStatus,
    check_automation_live_baseline_case_health_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationLiveBaselineCaseHealth")


@_attrs_define
class AutomationLiveBaselineCaseHealth:
    """Current retained verification availability and renewal readiness for one promoted suite case."""

    name: str
    status: AutomationLiveBaselineCaseHealthStatus
    run_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        status: str = self.status

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "status": status,
            }
        )
        if run_id is not UNSET:
            field_dict["run_id"] = run_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        status = check_automation_live_baseline_case_health_status(d.pop("status"))

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        automation_live_baseline_case_health = cls(
            name=name,
            status=status,
            run_id=run_id,
        )

        return automation_live_baseline_case_health
