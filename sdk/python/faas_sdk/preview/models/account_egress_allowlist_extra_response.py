from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AccountEgressAllowlistExtraResponse")


@_attrs_define
class AccountEgressAllowlistExtraResponse:
    """Body of GET /v1/account/egress_allowlist_extra and PATCH /v1/account/egress_allowlist_extra. The trio (extra,
    plan_cap, max_extra) lets the dashboard render the override + plan cap + global ceiling in a single round-trip.

    """

    extra: int
    """Effective additive budget currently in force. 0 = no override; the plan cap is authoritative."""
    plan_cap: int
    """Plan cap on apps.egress_allowlist CIDR count (Hobby 8, Pro 16, Scale 64; Free 0)."""
    max_extra: int
    """Global ceiling on the per-account override (api.MaxAccountEgressAllowlistExtra = 1024). Flat across plans;
    the validator rejects out-of-range values with `account_egress_allowlist_extra_out_of_range`."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        extra = self.extra

        plan_cap = self.plan_cap

        max_extra = self.max_extra

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "extra": extra,
                "plan_cap": plan_cap,
                "max_extra": max_extra,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        extra = d.pop("extra")

        plan_cap = d.pop("plan_cap")

        max_extra = d.pop("max_extra")

        account_egress_allowlist_extra_response = cls(
            extra=extra,
            plan_cap=plan_cap,
            max_extra=max_extra,
        )

        account_egress_allowlist_extra_response.additional_properties = d
        return account_egress_allowlist_extra_response

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
