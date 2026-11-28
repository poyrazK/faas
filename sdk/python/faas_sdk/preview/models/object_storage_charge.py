from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectStorageCharge")


@_attrs_define
class ObjectStorageCharge:
    """Current UTC-month customer charge estimate. This is not an invoice until a billing provider posts a line item."""

    currency: str
    storage_millicents: int
    requests_millicents: int
    egress_millicents: int
    total_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        currency = self.currency

        storage_millicents = self.storage_millicents

        requests_millicents = self.requests_millicents

        egress_millicents = self.egress_millicents

        total_millicents = self.total_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "currency": currency,
                "storage_millicents": storage_millicents,
                "requests_millicents": requests_millicents,
                "egress_millicents": egress_millicents,
                "total_millicents": total_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        currency = d.pop("currency")

        storage_millicents = d.pop("storage_millicents")

        requests_millicents = d.pop("requests_millicents")

        egress_millicents = d.pop("egress_millicents")

        total_millicents = d.pop("total_millicents")

        object_storage_charge = cls(
            currency=currency,
            storage_millicents=storage_millicents,
            requests_millicents=requests_millicents,
            egress_millicents=egress_millicents,
            total_millicents=total_millicents,
        )

        object_storage_charge.additional_properties = d
        return object_storage_charge

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
