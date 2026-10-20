from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.billing_catalog_entry import BillingCatalogEntry


T = TypeVar("T", bound="BillingCatalogResponse")


@_attrs_define
class BillingCatalogResponse:
    """Wire shape for GET / POST / DELETE
    /v1/admin/billing-paddle-catalog compatibility endpoints
    (PR-P3). Provider is the active billing provider's name
    (polar / paddle); providers without a catalog surface
    return 501. SyncedAt is the timestamp of the most recent
    successful catalog preflight; empty when no hydration has
    completed.

    """

    provider: str
    """Active provider name (polar / paddle)."""
    synced_at: str
    """RFC 3339 last-sync timestamp; empty string when never synced."""
    entries: list[BillingCatalogEntry]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider = self.provider

        synced_at = self.synced_at

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "synced_at": synced_at,
                "entries": entries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.billing_catalog_entry import BillingCatalogEntry

        d = dict(src_dict)
        provider = d.pop("provider")

        synced_at = d.pop("synced_at")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = BillingCatalogEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        billing_catalog_response = cls(
            provider=provider,
            synced_at=synced_at,
            entries=entries,
        )

        billing_catalog_response.additional_properties = d
        return billing_catalog_response

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
