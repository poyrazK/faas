from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RotateKeyRequest")


@_attrs_define
class RotateKeyRequest:
    """Optional body of POST /v1/keys/{id}/rotate. `grace_window_days` overrides, for this rotation only, how many days the
    old key keeps working; `0` revokes it at once (use it for a leaked key). Omitted uses the account's grace window or
    the plan default (7 days).

    """

    grace_window_days: int | Unset = UNSET
    """Days the old key keeps working after this rotation. `0` revokes it at once."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grace_window_days = self.grace_window_days

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if grace_window_days is not UNSET:
            field_dict["grace_window_days"] = grace_window_days

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        grace_window_days = d.pop("grace_window_days", UNSET)

        rotate_key_request = cls(
            grace_window_days=grace_window_days,
        )

        rotate_key_request.additional_properties = d
        return rotate_key_request

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
