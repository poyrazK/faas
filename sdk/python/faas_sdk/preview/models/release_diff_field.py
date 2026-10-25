from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_diff_field_field import ReleaseDiffFieldField, check_release_diff_field_field

T = TypeVar("T", bound="ReleaseDiffField")


@_attrs_define
class ReleaseDiffField:
    """A runtime setting that differs; env overrides compare by key."""

    field: ReleaseDiffFieldField
    from_: str
    to: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        field: str = self.field

        from_ = self.from_

        to = self.to

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "field": field,
                "from": from_,
                "to": to,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        field = check_release_diff_field_field(d.pop("field"))

        from_ = d.pop("from")

        to = d.pop("to")

        release_diff_field = cls(
            field=field,
            from_=from_,
            to=to,
        )

        release_diff_field.additional_properties = d
        return release_diff_field

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
