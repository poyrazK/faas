from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_integration_offer_credential_source import (
    OutboundIntegrationOfferCredentialSource,
    check_outbound_integration_offer_credential_source,
)
from ..models.outbound_integration_offer_owner_kind import (
    OutboundIntegrationOfferOwnerKind,
    check_outbound_integration_offer_owner_kind,
)

T = TypeVar("T", bound="OutboundIntegrationOffer")


@_attrs_define
class OutboundIntegrationOffer:
    """Account-owned managed integration metadata; never contains a provider key or gateway token."""

    id: UUID
    name: str
    origin: str
    allowed_methods: list[str]
    allowed_path_prefixes: list[str]
    enabled: bool
    credential_source: OutboundIntegrationOfferCredentialSource
    """Who supplies the provider Authorization value."""
    credential_configured: bool
    """Whether the selected source currently has a credential; never reveals its value."""
    owner_kind: OutboundIntegrationOfferOwnerKind
    """Whether the integration is operator-provisioned or customer-created."""
    daily_request_limit: int | None
    """Effective per-integration UTC-day admitted-request limit; null means no configured limit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        origin = self.origin

        allowed_methods = self.allowed_methods

        allowed_path_prefixes = self.allowed_path_prefixes

        enabled = self.enabled

        credential_source: str = self.credential_source

        credential_configured = self.credential_configured

        owner_kind: str = self.owner_kind

        daily_request_limit: int | None
        daily_request_limit = self.daily_request_limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "origin": origin,
                "allowed_methods": allowed_methods,
                "allowed_path_prefixes": allowed_path_prefixes,
                "enabled": enabled,
                "credential_source": credential_source,
                "credential_configured": credential_configured,
                "owner_kind": owner_kind,
                "daily_request_limit": daily_request_limit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        origin = d.pop("origin")

        allowed_methods = cast(list[str], d.pop("allowed_methods"))

        allowed_path_prefixes = cast(list[str], d.pop("allowed_path_prefixes"))

        enabled = d.pop("enabled")

        credential_source = check_outbound_integration_offer_credential_source(d.pop("credential_source"))

        credential_configured = d.pop("credential_configured")

        owner_kind = check_outbound_integration_offer_owner_kind(d.pop("owner_kind"))

        def _parse_daily_request_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_request_limit = _parse_daily_request_limit(d.pop("daily_request_limit"))

        outbound_integration_offer = cls(
            id=id,
            name=name,
            origin=origin,
            allowed_methods=allowed_methods,
            allowed_path_prefixes=allowed_path_prefixes,
            enabled=enabled,
            credential_source=credential_source,
            credential_configured=credential_configured,
            owner_kind=owner_kind,
            daily_request_limit=daily_request_limit,
        )

        outbound_integration_offer.additional_properties = d
        return outbound_integration_offer

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
