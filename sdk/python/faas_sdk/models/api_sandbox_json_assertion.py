from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APISandboxJSONAssertion")


@_attrs_define
class APISandboxJSONAssertion:
    """Expected scalar at a JSON Pointer in a sandbox response."""

    pointer: str
    """ JSON Pointer into the response. An empty string selects the response root. """
    equals: bool | float | None | str
    """ Expected JSON scalar value, including null. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pointer = self.pointer

        equals: bool | float | None | str
        equals = self.equals

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pointer": pointer,
                "equals": equals,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pointer = d.pop("pointer")

        def _parse_equals(data: object) -> bool | float | None | str:
            if data is None:
                return data
            return cast(bool | float | None | str, data)

        equals = _parse_equals(d.pop("equals"))

        api_sandbox_json_assertion = cls(
            pointer=pointer,
            equals=equals,
        )

        api_sandbox_json_assertion.additional_properties = d
        return api_sandbox_json_assertion

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
