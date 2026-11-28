from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.set_app_protection_request_state import (
    SetAppProtectionRequestState,
    check_set_app_protection_request_state,
)

T = TypeVar("T", bound="SetAppProtectionRequest")


@_attrs_define
class SetAppProtectionRequest:
    """Turns production protection on or off. on creates the baseline; off deletes the baseline objects you have not
    changed.

    """

    state: SetAppProtectionRequestState
    """on enrolls the app; off removes the unchanged protection objects."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_set_app_protection_request_state(d.pop("state"))

        set_app_protection_request = cls(
            state=state,
        )

        set_app_protection_request.additional_properties = d
        return set_app_protection_request

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
