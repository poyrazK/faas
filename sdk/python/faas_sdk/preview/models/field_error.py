from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="FieldError")


@_attrs_define
class FieldError:
    """Per-field entry of `Problem.errors`. The shape mirrors
    Cloudflare's API Shield 422 / Stripe's `card_errors` family so
    an SDK can iterate `errors[]` to drive form-field UI without
    parsing prose. `field` uses JSON Pointer notation for nested
    keys (`address.zip`). `expected` and `got` are short stable
    strings; consumers should not depend on the prose.

    """

    field: str
    expected: str
    got: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        field = self.field

        expected = self.expected

        got = self.got

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "field": field,
                "expected": expected,
            }
        )
        if got is not UNSET:
            field_dict["got"] = got

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        field = d.pop("field")

        expected = d.pop("expected")

        got = d.pop("got", UNSET)

        field_error = cls(
            field=field,
            expected=expected,
            got=got,
        )

        field_error.additional_properties = d
        return field_error

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
