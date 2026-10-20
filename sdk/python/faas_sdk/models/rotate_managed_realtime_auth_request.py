from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RotateManagedRealtimeAuthRequest")


@_attrs_define
class RotateManagedRealtimeAuthRequest:
    """Start a bounded overlap window for a replacement static bearer credential."""

    new_auth_token: str
    grace_period_seconds: int | Unset = 300
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        new_auth_token = self.new_auth_token

        grace_period_seconds = self.grace_period_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "new_auth_token": new_auth_token,
            }
        )
        if grace_period_seconds is not UNSET:
            field_dict["grace_period_seconds"] = grace_period_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        new_auth_token = d.pop("new_auth_token")

        grace_period_seconds = d.pop("grace_period_seconds", UNSET)

        rotate_managed_realtime_auth_request = cls(
            new_auth_token=new_auth_token,
            grace_period_seconds=grace_period_seconds,
        )

        rotate_managed_realtime_auth_request.additional_properties = d
        return rotate_managed_realtime_auth_request

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
