from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_tenant_reconciliation_receipt_summary import PlatformTenantReconciliationReceiptSummary


T = TypeVar("T", bound="PlatformTenantReconciliationReceiptListResponse")


@_attrs_define
class PlatformTenantReconciliationReceiptListResponse:
    """One page of reconciliation receipt summaries and an optional cursor for older receipts."""

    receipts: list[PlatformTenantReconciliationReceiptSummary]
    next_page_token: str | Unset = UNSET
    """Opaque cursor for the next page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipts = []
        for receipts_item_data in self.receipts:
            receipts_item = receipts_item_data.to_dict()
            receipts.append(receipts_item)

        next_page_token = self.next_page_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipts": receipts,
            }
        )
        if next_page_token is not UNSET:
            field_dict["next_page_token"] = next_page_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_reconciliation_receipt_summary import PlatformTenantReconciliationReceiptSummary

        d = dict(src_dict)
        receipts = []
        _receipts = d.pop("receipts")
        for receipts_item_data in _receipts:
            receipts_item = PlatformTenantReconciliationReceiptSummary.from_dict(receipts_item_data)

            receipts.append(receipts_item)

        next_page_token = d.pop("next_page_token", UNSET)

        platform_tenant_reconciliation_receipt_list_response = cls(
            receipts=receipts,
            next_page_token=next_page_token,
        )

        platform_tenant_reconciliation_receipt_list_response.additional_properties = d
        return platform_tenant_reconciliation_receipt_list_response

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
