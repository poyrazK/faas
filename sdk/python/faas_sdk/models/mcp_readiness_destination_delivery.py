from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_destination_delivery_destination import (
    MCPReadinessDestinationDeliveryDestination,
    check_mcp_readiness_destination_delivery_destination,
)
from ..models.mcp_readiness_destination_delivery_provider import (
    MCPReadinessDestinationDeliveryProvider,
    check_mcp_readiness_destination_delivery_provider,
)
from ..models.mcp_readiness_destination_delivery_provider_status import (
    MCPReadinessDestinationDeliveryProviderStatus,
    check_mcp_readiness_destination_delivery_provider_status,
)
from ..models.mcp_readiness_destination_delivery_status import (
    MCPReadinessDestinationDeliveryStatus,
    check_mcp_readiness_destination_delivery_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessDestinationDelivery")


@_attrs_define
class MCPReadinessDestinationDelivery:
    """Safe persisted progress for one event receiver without ownership tokens or credentials."""

    destination: MCPReadinessDestinationDeliveryDestination
    status: MCPReadinessDestinationDeliveryStatus
    attempt_count: int
    provider: MCPReadinessDestinationDeliveryProvider | Unset = UNSET
    """Transport for this receiver delivery; omission denotes a legacy webhook."""
    provider_status: MCPReadinessDestinationDeliveryProviderStatus | Unset = UNSET
    """PagerDuty transport progress; accepted means a validated 202 acknowledgement rather than a confirmed page."""
    last_status_code: int | Unset = UNSET
    last_delivery_id: UUID | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    lease_until: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        destination: str = self.destination

        status: str = self.status

        attempt_count = self.attempt_count

        provider: str | Unset = UNSET
        if not isinstance(self.provider, Unset):
            provider = self.provider

        provider_status: str | Unset = UNSET
        if not isinstance(self.provider_status, Unset):
            provider_status = self.provider_status

        last_status_code = self.last_status_code

        last_delivery_id: str | Unset = UNSET
        if not isinstance(self.last_delivery_id, Unset):
            last_delivery_id = str(self.last_delivery_id)

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "destination": destination,
                "status": status,
                "attempt_count": attempt_count,
            }
        )
        if provider is not UNSET:
            field_dict["provider"] = provider
        if provider_status is not UNSET:
            field_dict["provider_status"] = provider_status
        if last_status_code is not UNSET:
            field_dict["last_status_code"] = last_status_code
        if last_delivery_id is not UNSET:
            field_dict["last_delivery_id"] = last_delivery_id
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        destination = check_mcp_readiness_destination_delivery_destination(d.pop("destination"))

        status = check_mcp_readiness_destination_delivery_status(d.pop("status"))

        attempt_count = d.pop("attempt_count")

        _provider = d.pop("provider", UNSET)
        provider: MCPReadinessDestinationDeliveryProvider | Unset
        if isinstance(_provider, Unset):
            provider = UNSET
        else:
            provider = check_mcp_readiness_destination_delivery_provider(_provider)

        _provider_status = d.pop("provider_status", UNSET)
        provider_status: MCPReadinessDestinationDeliveryProviderStatus | Unset
        if isinstance(_provider_status, Unset):
            provider_status = UNSET
        else:
            provider_status = check_mcp_readiness_destination_delivery_provider_status(_provider_status)

        last_status_code = d.pop("last_status_code", UNSET)

        _last_delivery_id = d.pop("last_delivery_id", UNSET)
        last_delivery_id: UUID | Unset
        if isinstance(_last_delivery_id, Unset):
            last_delivery_id = UNSET
        else:
            last_delivery_id = UUID(_last_delivery_id)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _lease_until = d.pop("lease_until", UNSET)
        lease_until: datetime.datetime | Unset
        if isinstance(_lease_until, Unset):
            lease_until = UNSET
        else:
            lease_until = datetime.datetime.fromisoformat(_lease_until)

        mcp_readiness_destination_delivery = cls(
            destination=destination,
            status=status,
            attempt_count=attempt_count,
            provider=provider,
            provider_status=provider_status,
            last_status_code=last_status_code,
            last_delivery_id=last_delivery_id,
            next_attempt_at=next_attempt_at,
            lease_until=lease_until,
        )

        mcp_readiness_destination_delivery.additional_properties = d
        return mcp_readiness_destination_delivery

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
