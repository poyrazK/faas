from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_acknowledgement_source import (
    MCPReadinessAcknowledgementSource,
    check_mcp_readiness_acknowledgement_source,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessAcknowledgement")


@_attrs_define
class MCPReadinessAcknowledgement:
    """Retained acknowledgement metadata independent of readiness state."""

    request_id: UUID
    created_by: UUID
    created_at: datetime.datetime
    reason: str
    source: MCPReadinessAcknowledgementSource | Unset = UNSET
    """Provider provenance for this MCPReadinessAcknowledgement; absence denotes a local account action."""
    provider_event_id: str | Unset = UNSET
    """PagerDuty event associated with this MCPReadinessAcknowledgement."""
    provider_incident_id: str | Unset = UNSET
    """Provider incident associated with this MCPReadinessAcknowledgement."""
    provider_occurred_at: datetime.datetime | Unset = UNSET
    """Signed provider event time retained in this MCPReadinessAcknowledgement."""
    expires_at: datetime.datetime | Unset = UNSET
    """Omitted for acknowledgement until release or incident resolution."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        reason = self.reason

        source: str | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source

        provider_event_id = self.provider_event_id

        provider_incident_id = self.provider_incident_id

        provider_occurred_at: str | Unset = UNSET
        if not isinstance(self.provider_occurred_at, Unset):
            provider_occurred_at = self.provider_occurred_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "created_by": created_by,
                "created_at": created_at,
                "reason": reason,
            }
        )
        if source is not UNSET:
            field_dict["source"] = source
        if provider_event_id is not UNSET:
            field_dict["provider_event_id"] = provider_event_id
        if provider_incident_id is not UNSET:
            field_dict["provider_incident_id"] = provider_incident_id
        if provider_occurred_at is not UNSET:
            field_dict["provider_occurred_at"] = provider_occurred_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        reason = d.pop("reason")

        _source = d.pop("source", UNSET)
        source: MCPReadinessAcknowledgementSource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = check_mcp_readiness_acknowledgement_source(_source)

        provider_event_id = d.pop("provider_event_id", UNSET)

        provider_incident_id = d.pop("provider_incident_id", UNSET)

        _provider_occurred_at = d.pop("provider_occurred_at", UNSET)
        provider_occurred_at: datetime.datetime | Unset
        if isinstance(_provider_occurred_at, Unset):
            provider_occurred_at = UNSET
        else:
            provider_occurred_at = datetime.datetime.fromisoformat(_provider_occurred_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        mcp_readiness_acknowledgement = cls(
            request_id=request_id,
            created_by=created_by,
            created_at=created_at,
            reason=reason,
            source=source,
            provider_event_id=provider_event_id,
            provider_incident_id=provider_incident_id,
            provider_occurred_at=provider_occurred_at,
            expires_at=expires_at,
        )

        mcp_readiness_acknowledgement.additional_properties = d
        return mcp_readiness_acknowledgement

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
