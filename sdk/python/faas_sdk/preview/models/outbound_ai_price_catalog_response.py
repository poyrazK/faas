from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_price_entry import OutboundAIPriceEntry


T = TypeVar("T", bound="OutboundAIPriceCatalogResponse")


@_attrs_define
class OutboundAIPriceCatalogResponse:
    """One provider's built-in AI price catalog (ADR-994), generated from LiteLLM's MIT-licensed price data. model_prefix
    holds the catalog model name.

    """

    ai_provider: str
    source: str
    """URL of the price data the catalog was generated from."""
    generated: datetime.date
    entries: list[OutboundAIPriceEntry]
    currency: str | Unset = UNSET
    """ISO 4217 currency of every catalog price: the billing currency, USD. Absent from responses of releases
    before ADR 20261019123822."""
    usd_per_eur: str | Unset = UNSET
    """Deprecated: always 1, since the catalog is in US dollars."""
    fx_source: str | Unset = UNSET
    """Deprecated: names no exchange rate."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ai_provider = self.ai_provider

        source = self.source

        generated = self.generated.isoformat()

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        currency = self.currency

        usd_per_eur = self.usd_per_eur

        fx_source = self.fx_source

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ai_provider": ai_provider,
                "source": source,
                "generated": generated,
                "entries": entries,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency
        if usd_per_eur is not UNSET:
            field_dict["usd_per_eur"] = usd_per_eur
        if fx_source is not UNSET:
            field_dict["fx_source"] = fx_source

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_price_entry import OutboundAIPriceEntry

        d = dict(src_dict)
        ai_provider = d.pop("ai_provider")

        source = d.pop("source")

        generated = datetime.date.fromisoformat(d.pop("generated"))

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = OutboundAIPriceEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        currency = d.pop("currency", UNSET)

        usd_per_eur = d.pop("usd_per_eur", UNSET)

        fx_source = d.pop("fx_source", UNSET)

        outbound_ai_price_catalog_response = cls(
            ai_provider=ai_provider,
            source=source,
            generated=generated,
            entries=entries,
            currency=currency,
            usd_per_eur=usd_per_eur,
            fx_source=fx_source,
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
