from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_price_entry import OutboundAIPriceEntry


T = TypeVar("T", bound="OutboundAIPriceCatalogResponse")


@_attrs_define
class OutboundAIPriceCatalogResponse:
    """One provider's built-in AI price catalog (ADR-992), generated from LiteLLM's MIT-licensed price data. model_prefix
    holds the catalog model name.

    """

    ai_provider: str
    source: str
    """URL of the price data the catalog was generated from."""
    generated: datetime.date
    usd_per_eur: str
    """USD per EUR used for the conversion, as a decimal string."""
    fx_source: str
    """Source and date of the exchange rate."""
    entries: list[OutboundAIPriceEntry]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ai_provider = self.ai_provider

        source = self.source

        generated = self.generated.isoformat()

        usd_per_eur = self.usd_per_eur

        fx_source = self.fx_source

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ai_provider": ai_provider,
                "source": source,
                "generated": generated,
                "usd_per_eur": usd_per_eur,
                "fx_source": fx_source,
                "entries": entries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_price_entry import OutboundAIPriceEntry

        d = dict(src_dict)
        ai_provider = d.pop("ai_provider")

        source = d.pop("source")

        generated = datetime.date.fromisoformat(d.pop("generated"))

        usd_per_eur = d.pop("usd_per_eur")

        fx_source = d.pop("fx_source")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = OutboundAIPriceEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        outbound_ai_price_catalog_response = cls(
            ai_provider=ai_provider,
            source=source,
            generated=generated,
            usd_per_eur=usd_per_eur,
            fx_source=fx_source,
            entries=entries,
        )

        outbound_ai_price_catalog_response.additional_properties = d
        return outbound_ai_price_catalog_response

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
