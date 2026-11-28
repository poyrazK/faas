from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectStoragePricing")


@_attrs_define
class ObjectStoragePricing:
    """Operator-supplied customer rate card. Rates are integer millicents and are not provider-specific."""

    currency: str
    storage_millicents_per_gib_month: int
    requests_millicents_per_million: int
    egress_millicents_per_gib: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        currency = self.currency

        storage_millicents_per_gib_month = self.storage_millicents_per_gib_month

        requests_millicents_per_million = self.requests_millicents_per_million

        egress_millicents_per_gib = self.egress_millicents_per_gib

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "currency": currency,
                "storage_millicents_per_gib_month": storage_millicents_per_gib_month,
                "requests_millicents_per_million": requests_millicents_per_million,
                "egress_millicents_per_gib": egress_millicents_per_gib,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        currency = d.pop("currency")

        storage_millicents_per_gib_month = d.pop("storage_millicents_per_gib_month")

        requests_millicents_per_million = d.pop("requests_millicents_per_million")

        egress_millicents_per_gib = d.pop("egress_millicents_per_gib")

        object_storage_pricing = cls(
            currency=currency,
            storage_millicents_per_gib_month=storage_millicents_per_gib_month,
            requests_millicents_per_million=requests_millicents_per_million,
            egress_millicents_per_gib=egress_millicents_per_gib,
        )

        object_storage_pricing.additional_properties = d
        return object_storage_pricing

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
