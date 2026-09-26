from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_integration_offer import OutboundIntegrationOffer


T = TypeVar("T", bound="OutboundAppBinding")


@_attrs_define
class OutboundAppBinding:
    """An app's customer-owned attachment to a managed integration."""

    integration: OutboundIntegrationOffer
    """Account-owned managed integration metadata; never contains a provider key or gateway token."""
    app_id: UUID
    allowed_methods: list[str]
    """App-specific methods, bounded by the integration ceiling."""
    allowed_path_prefixes: list[str]
    """App-specific path prefixes, bounded by the integration ceiling."""
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        integration = self.integration.to_dict()

        app_id = str(self.app_id)

        allowed_methods = self.allowed_methods

        allowed_path_prefixes = self.allowed_path_prefixes

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "integration": integration,
                "app_id": app_id,
                "allowed_methods": allowed_methods,
                "allowed_path_prefixes": allowed_path_prefixes,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_integration_offer import OutboundIntegrationOffer

        d = dict(src_dict)
        integration = OutboundIntegrationOffer.from_dict(d.pop("integration"))

        app_id = UUID(d.pop("app_id"))

        allowed_methods = cast(list[str], d.pop("allowed_methods"))

        allowed_path_prefixes = cast(list[str], d.pop("allowed_path_prefixes"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        outbound_app_binding = cls(
            integration=integration,
            app_id=app_id,
            allowed_methods=allowed_methods,
            allowed_path_prefixes=allowed_path_prefixes,
            created_at=created_at,
        )

        outbound_app_binding.additional_properties = d
        return outbound_app_binding

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
