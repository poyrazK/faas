from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SetGraceWindowRequest")


@_attrs_define
class SetGraceWindowRequest:
    """Body of PATCH /v1/account/keys/grace_window_days. `days` is the per-account override for the rotation grace window.
    `days=0` is atomic rotation; `days=null` (or omitted) clears the override and falls back to the plan default
    (api.DefaultAPIKeyGraceWindowDays = 7).

    """

    days: int | None | Unset = UNSET
    """Per-account override in days. 0 = atomic, null = use plan default."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        days: int | None | Unset
        if isinstance(self.days, Unset):
            days = UNSET
        else:
            days = self.days

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if days is not UNSET:
            field_dict["days"] = days

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_days(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        days = _parse_days(d.pop("days", UNSET))

        set_grace_window_request = cls(
            days=days,
        )

        set_grace_window_request.additional_properties = d
        return set_grace_window_request

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
