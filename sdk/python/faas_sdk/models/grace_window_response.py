from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="GraceWindowResponse")


@_attrs_define
class GraceWindowResponse:
    """Body of GET /v1/account/keys/grace_window_days. `days` is the customer's override (null = no override);
    `plan_default` is the platform default the rotation handler uses when the row is null.

    """

    days: int | None
    plan_default: int
    """Platform default grace window in days (api.DefaultAPIKeyGraceWindowDays = 7)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        days: int | None
        days = self.days

        plan_default = self.plan_default

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "days": days,
                "plan_default": plan_default,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_days(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        days = _parse_days(d.pop("days"))

        plan_default = d.pop("plan_default")

        grace_window_response = cls(
            days=days,
            plan_default=plan_default,
        )

        grace_window_response.additional_properties = d
        return grace_window_response

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
