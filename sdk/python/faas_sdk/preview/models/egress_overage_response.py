from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.egress_overage_response_billing import EgressOverageResponseBilling, check_egress_overage_response_billing
from ..types import UNSET, Unset

T = TypeVar("T", bound="EgressOverageResponse")


@_attrs_define
class EgressOverageResponse:
    """The billing subject's election to pay for egress above its plan's monthly
    allowance instead of stopping at it.

    """

    enabled: bool
    """Whether the election is on."""
    electable: bool
    """Whether the plan allows the election; Free does not."""
    included_gib: int
    """The plan's monthly egress allowance in GiB."""
    billing: EgressOverageResponseBilling
    """live when egress above the allowance is billed while the election is on; off while egress is not billed,
    when the allowance still holds."""
    enabled_at: datetime.datetime | Unset = UNSET
    """When the election started; kept when it is turned off and on again."""
    disabled_at: datetime.datetime | Unset = UNSET
    """When the election was last turned off."""
    price_millicents_per_gib: int | Unset = UNSET
    """Price of a GiB above the allowance while billing is live, in millicents."""
    currency: str | Unset = UNSET
    """Currency of the price."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        electable = self.electable

        included_gib = self.included_gib

        billing: str = self.billing

        enabled_at: str | Unset = UNSET
        if not isinstance(self.enabled_at, Unset):
            enabled_at = self.enabled_at.isoformat()

        disabled_at: str | Unset = UNSET
        if not isinstance(self.disabled_at, Unset):
            disabled_at = self.disabled_at.isoformat()

        price_millicents_per_gib = self.price_millicents_per_gib

        currency = self.currency

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "electable": electable,
                "included_gib": included_gib,
                "billing": billing,
            }
        )
        if enabled_at is not UNSET:
            field_dict["enabled_at"] = enabled_at
        if disabled_at is not UNSET:
            field_dict["disabled_at"] = disabled_at
        if price_millicents_per_gib is not UNSET:
            field_dict["price_millicents_per_gib"] = price_millicents_per_gib
        if currency is not UNSET:
            field_dict["currency"] = currency

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        electable = d.pop("electable")

        included_gib = d.pop("included_gib")

        billing = check_egress_overage_response_billing(d.pop("billing"))

        _enabled_at = d.pop("enabled_at", UNSET)
        enabled_at: datetime.datetime | Unset
        if isinstance(_enabled_at, Unset):
            enabled_at = UNSET
        else:
            enabled_at = datetime.datetime.fromisoformat(_enabled_at)

        _disabled_at = d.pop("disabled_at", UNSET)
        disabled_at: datetime.datetime | Unset
        if isinstance(_disabled_at, Unset):
            disabled_at = UNSET
        else:
            disabled_at = datetime.datetime.fromisoformat(_disabled_at)

        price_millicents_per_gib = d.pop("price_millicents_per_gib", UNSET)

        currency = d.pop("currency", UNSET)

        egress_overage_response = cls(
            enabled=enabled,
            electable=electable,
            included_gib=included_gib,
            billing=billing,
            enabled_at=enabled_at,
            disabled_at=disabled_at,
            price_millicents_per_gib=price_millicents_per_gib,
            currency=currency,
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
