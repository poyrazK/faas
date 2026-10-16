from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectPromotionAdmission")


@_attrs_define
class ProjectPromotionAdmission:
    """Durable capacity wait receipt. Queue time does not consume the execution health window. An absent started_at means
    execution has never started.

    """

    queued_at: datetime.datetime
    wait_deadline: datetime.datetime
    reason: str
    started_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queued_at = self.queued_at.isoformat()

        wait_deadline = self.wait_deadline.isoformat()

        reason = self.reason

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "queued_at": queued_at,
                "wait_deadline": wait_deadline,
                "reason": reason,
            }
        )
        if started_at is not UNSET:
            field_dict["started_at"] = started_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        queued_at = datetime.datetime.fromisoformat(d.pop("queued_at"))

        wait_deadline = datetime.datetime.fromisoformat(d.pop("wait_deadline"))

        reason = d.pop("reason")

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        project_promotion_admission = cls(
            queued_at=queued_at,
            wait_deadline=wait_deadline,
            reason=reason,
            started_at=started_at,
        )

        project_promotion_admission.additional_properties = d
        return project_promotion_admission

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
