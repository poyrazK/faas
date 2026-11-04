from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.inbound_webhook_endpoint_response_provider import (
    InboundWebhookEndpointResponseProvider,
    check_inbound_webhook_endpoint_response_provider,
)
from ..models.inbound_webhook_endpoint_response_signing_secret_masked import (
    InboundWebhookEndpointResponseSigningSecretMasked,
    check_inbound_webhook_endpoint_response_signing_secret_masked,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="InboundWebhookEndpointResponse")


@_attrs_define
class InboundWebhookEndpointResponse:
    """Safe endpoint metadata. endpoint_url is disclosed only by the create response."""

    id: UUID
    app_id: UUID
    account_id: UUID
    name: str
    provider: InboundWebhookEndpointResponseProvider
    delivery_path: str
    enabled: bool
    signing_secret_masked: InboundWebhookEndpointResponseSigningSecretMasked
    created_at: datetime.datetime
    updated_at: datetime.datetime
    endpoint_url: str | Unset = UNSET
    """One-time-disclosed public provider URL, present only on create."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        account_id = str(self.account_id)

        name = self.name

        provider: str = self.provider

        delivery_path = self.delivery_path

        enabled = self.enabled

        signing_secret_masked: str = self.signing_secret_masked

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        endpoint_url = self.endpoint_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "account_id": account_id,
                "name": name,
                "provider": provider,
                "delivery_path": delivery_path,
                "enabled": enabled,
                "signing_secret_masked": signing_secret_masked,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if endpoint_url is not UNSET:
            field_dict["endpoint_url"] = endpoint_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        account_id = UUID(d.pop("account_id"))

        name = d.pop("name")

        provider = check_inbound_webhook_endpoint_response_provider(d.pop("provider"))

        delivery_path = d.pop("delivery_path")

        enabled = d.pop("enabled")

        signing_secret_masked = check_inbound_webhook_endpoint_response_signing_secret_masked(
            d.pop("signing_secret_masked")
        )

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        endpoint_url = d.pop("endpoint_url", UNSET)

        inbound_webhook_endpoint_response = cls(
            id=id,
            app_id=app_id,
            account_id=account_id,
            name=name,
            provider=provider,
            delivery_path=delivery_path,
            enabled=enabled,
            signing_secret_masked=signing_secret_masked,
            created_at=created_at,
            updated_at=updated_at,
            endpoint_url=endpoint_url,
        )

        inbound_webhook_endpoint_response.additional_properties = d
        return inbound_webhook_endpoint_response

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
