from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.invoice_refresh_response_provider import (
    InvoiceRefreshResponseProvider,
    check_invoice_refresh_response_provider,
)
from ..models.invoice_refresh_response_source_gap import (
    InvoiceRefreshResponseSourceGap,
    check_invoice_refresh_response_source_gap,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="InvoiceRefreshResponse")


@_attrs_define
class InvoiceRefreshResponse:
    """Persisted line coverage after refreshing an existing invoice from its billing provider."""

    invoice_id: UUID
    provider: InvoiceRefreshResponseProvider
    detailed: bool
    """Whether the refreshed line snapshot is complete, classified, and exactly reconciled."""
    line_items: int
    updated_at: datetime.datetime
    source_gap: InvoiceRefreshResponseSourceGap | Unset = UNSET
    """Reason for aggregate fallback; omitted when detailed is true."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invoice_id = str(self.invoice_id)

        provider: str = self.provider

        detailed = self.detailed

        line_items = self.line_items

        updated_at = self.updated_at.isoformat()

        source_gap: str | Unset = UNSET
        if not isinstance(self.source_gap, Unset):
            source_gap = self.source_gap

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invoice_id": invoice_id,
                "provider": provider,
                "detailed": detailed,
                "line_items": line_items,
                "updated_at": updated_at,
            }
        )
        if source_gap is not UNSET:
            field_dict["source_gap"] = source_gap

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invoice_id = UUID(d.pop("invoice_id"))

        provider = check_invoice_refresh_response_provider(d.pop("provider"))

        detailed = d.pop("detailed")

        line_items = d.pop("line_items")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _source_gap = d.pop("source_gap", UNSET)
        source_gap: InvoiceRefreshResponseSourceGap | Unset
        if isinstance(_source_gap, Unset):
            source_gap = UNSET
        else:
            source_gap = check_invoice_refresh_response_source_gap(_source_gap)

        invoice_refresh_response = cls(
            invoice_id=invoice_id,
            provider=provider,
            detailed=detailed,
            line_items=line_items,
            updated_at=updated_at,
            source_gap=source_gap,
        )

        invoice_refresh_response.additional_properties = d
        return invoice_refresh_response

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
