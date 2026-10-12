from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define, field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentWorkerDrainStatus")


@_attrs_define
class DeploymentWorkerDrainStatus:
    """Worker instance drain counts; overdue spans have no recorded outcome."""

    active: int
    completed: int
    timed_out: int
    failed: int
    overdue: int
    deadline: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        result = dict(self.additional_properties)
        result.update(active=self.active, completed=self.completed, timed_out=self.timed_out, failed=self.failed, overdue=self.overdue)
        if not isinstance(self.deadline, Unset):
            result["deadline"] = self.deadline.isoformat()
        return result

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        data = dict(src_dict)
        deadline = data.pop("deadline", UNSET)
        result = cls(active=data.pop("active"), completed=data.pop("completed"), timed_out=data.pop("timed_out"),
                     failed=data.pop("failed"), overdue=data.pop("overdue"),
                     deadline=UNSET if isinstance(deadline, Unset) else datetime.datetime.fromisoformat(deadline))
        result.additional_properties = data
        return result

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties)

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
