from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.billing_run_collection_total import BillingRunCollectionTotal


T = TypeVar("T", bound="BillingRunCollectionsResponse")


@_attrs_define
class BillingRunCollectionsResponse:
    """Recorded Stripe payment totals for a completed billing run, grouped by currency and Stripe mode."""

    totals: list[BillingRunCollectionTotal]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        totals = []
        for totals_item_data in self.totals:
            totals_item = totals_item_data.to_dict()
            totals.append(totals_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "totals": totals,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.billing_run_collection_total import BillingRunCollectionTotal

        d = dict(src_dict)
        totals = []
        _totals = d.pop("totals")
        for totals_item_data in _totals:
            totals_item = BillingRunCollectionTotal.from_dict(totals_item_data)

            totals.append(totals_item)

        billing_run_collections_response = cls(
            totals=totals,
        )

        billing_run_collections_response.additional_properties = d
        return billing_run_collections_response

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
