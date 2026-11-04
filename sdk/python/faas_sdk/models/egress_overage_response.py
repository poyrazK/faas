from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EgressOverageResponse")


@_attrs_define
class EgressOverageResponse:
    """A billing subject's egress overage choice. With it on, egress past the
    plan's monthly allowance is billed at millicents_per_gib and counts
    toward the spend cap; with it off, apps stop at the allowance until
    the month ends. billed is false while Gregale does not bill egress for
    the plan.

    """

    enabled: bool
    billed: bool
    included_gib: int
    millicents_per_gib: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        billed = self.billed

        included_gib = self.included_gib

        millicents_per_gib = self.millicents_per_gib

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "billed": billed,
                "included_gib": included_gib,
                "millicents_per_gib": millicents_per_gib,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        billed = d.pop("billed")

        included_gib = d.pop("included_gib")

        millicents_per_gib = d.pop("millicents_per_gib")

        egress_overage_response = cls(
            enabled=enabled,
            billed=billed,
            included_gib=included_gib,
            millicents_per_gib=millicents_per_gib,
        )

        egress_overage_response.additional_properties = d
        return egress_overage_response

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
