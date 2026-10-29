from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_capture import ProfileCapture


T = TypeVar("T", bound="ProfileCaptureList")


@_attrs_define
class ProfileCaptureList:
    """An app's profile captures, newest first."""

    captures: list[ProfileCapture]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        captures = []
        for captures_item_data in self.captures:
            captures_item = captures_item_data.to_dict()
            captures.append(captures_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "captures": captures,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_capture import ProfileCapture

        d = dict(src_dict)
        captures = []
        _captures = d.pop("captures")
        for captures_item_data in _captures:
            captures_item = ProfileCapture.from_dict(captures_item_data)

            captures.append(captures_item)

        profile_capture_list = cls(
            captures=captures,
        )

        profile_capture_list.additional_properties = d
        return profile_capture_list

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
