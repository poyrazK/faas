from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.invoice_history_backfill_response_provider import (
    InvoiceHistoryBackfillResponseProvider,
    check_invoice_history_backfill_response_provider,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="InvoiceHistoryBackfillResponse")


@_attrs_define
class InvoiceHistoryBackfillResponse:
    """Counts and cursor for one provider invoice-history page."""

    provider: InvoiceHistoryBackfillResponseProvider
    scanned: int
    imported: int
    skipped: int
    has_more: bool
    next_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        scanned = self.scanned

        imported = self.imported

        skipped = self.skipped

        has_more = self.has_more

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "scanned": scanned,
                "imported": imported,
                "skipped": skipped,
                "has_more": has_more,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider = check_invoice_history_backfill_response_provider(d.pop("provider"))

        scanned = d.pop("scanned")

        imported = d.pop("imported")

        skipped = d.pop("skipped")

        has_more = d.pop("has_more")

        next_cursor = d.pop("next_cursor", UNSET)

        invoice_history_backfill_response = cls(
            provider=provider,
            scanned=scanned,
            imported=imported,
            skipped=skipped,
            has_more=has_more,
            next_cursor=next_cursor,
        )

        invoice_history_backfill_response.additional_properties = d
        return invoice_history_backfill_response

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
