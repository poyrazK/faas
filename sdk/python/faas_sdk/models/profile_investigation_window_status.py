from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_investigation_window_status_status import (
    ProfileInvestigationWindowStatusStatus,
    check_profile_investigation_window_status_status,
)

T = TypeVar("T", bound="ProfileInvestigationWindowStatus")


@_attrs_define
class ProfileInvestigationWindowStatus:
    """Query eligibility under the current plan and installation. Retained does not guarantee that samples exist; expiry
    never implies zero CPU usage.

    """

    status: ProfileInvestigationWindowStatusStatus
    detail: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        detail = self.detail

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "detail": detail,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_profile_investigation_window_status_status(d.pop("status"))

        detail = d.pop("detail")

        profile_investigation_window_status = cls(
            status=status,
            detail=detail,
        )

        profile_investigation_window_status.additional_properties = d
        return profile_investigation_window_status

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
