from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.statement_invoice_export_response import StatementInvoiceExportResponse


T = TypeVar("T", bound="StatementInvoiceListResponse")


@_attrs_define
class StatementInvoiceListResponse:
    """One page of an account's exported invoices, newest first."""

    invoices: list[StatementInvoiceExportResponse]
    next_cursor: str | Unset = UNSET
    """Pass as cursor for the next invoice page; absent on the last page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invoices = []
        for invoices_item_data in self.invoices:
            invoices_item = invoices_item_data.to_dict()
            invoices.append(invoices_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invoices": invoices,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.statement_invoice_export_response import StatementInvoiceExportResponse

        d = dict(src_dict)
        invoices = []
        _invoices = d.pop("invoices")
        for invoices_item_data in _invoices:
            invoices_item = StatementInvoiceExportResponse.from_dict(invoices_item_data)

            invoices.append(invoices_item)

        next_cursor = d.pop("next_cursor", UNSET)

        statement_invoice_list_response = cls(
            invoices=invoices,
            next_cursor=next_cursor,
        )

        statement_invoice_list_response.additional_properties = d
        return statement_invoice_list_response

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
