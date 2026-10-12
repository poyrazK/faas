from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentWorkerDrainStatus")


@_attrs_define
class DeploymentWorkerDrainStatus:
    """Worker instance shutdown progress from the deployment audit timeline. Counts are instances, not application tasks.
    Overdue spans passed their deadline without a recorded outcome.

    """

    active: int
    completed: int
    timed_out: int
    failed: int
    overdue: int
    deadline: datetime.datetime | Unset = UNSET
    """Earliest unfinished worker drain deadline."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        active = self.active

        completed = self.completed

        timed_out = self.timed_out

        failed = self.failed

        overdue = self.overdue

        deadline: str | Unset = UNSET
        if not isinstance(self.deadline, Unset):
            deadline = self.deadline.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "active": active,
                "completed": completed,
                "timed_out": timed_out,
                "failed": failed,
                "overdue": overdue,
            }
        )
        if deadline is not UNSET:
            field_dict["deadline"] = deadline

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        active = d.pop("active")

        completed = d.pop("completed")

        timed_out = d.pop("timed_out")

        failed = d.pop("failed")

        overdue = d.pop("overdue")

        _deadline = d.pop("deadline", UNSET)
        deadline: datetime.datetime | Unset
        if isinstance(_deadline, Unset):
            deadline = UNSET
        else:
            deadline = datetime.datetime.fromisoformat(_deadline)

        deployment_worker_drain_status = cls(
            active=active,
            completed=completed,
            timed_out=timed_out,
            failed=failed,
            overdue=overdue,
            deadline=deadline,
        )

        deployment_worker_drain_status.additional_properties = d
        return deployment_worker_drain_status

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
