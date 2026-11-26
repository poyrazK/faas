from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_attention_receipt_action import (
    MCPReadinessAttentionReceiptAction,
    check_mcp_readiness_attention_receipt_action,
)
from ..models.mcp_readiness_attention_receipt_source import (
    MCPReadinessAttentionReceiptSource,
    check_mcp_readiness_attention_receipt_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy


T = TypeVar("T", bound="MCPReadinessAttentionReceipt")


@_attrs_define
class MCPReadinessAttentionReceipt:
    """Immutable attention decision retained transactionally with the incident state change."""

    request_id: UUID
    incident_id: UUID
    action: MCPReadinessAttentionReceiptAction
    reason: str
    created_by: UUID
    created_at: datetime.datetime
    version: int
    source: MCPReadinessAttentionReceiptSource | Unset = UNSET
    """Provider provenance for this MCPReadinessAttentionReceipt; absence denotes a local account action."""
    provider_event_id: str | Unset = UNSET
    """PagerDuty event associated with this MCPReadinessAttentionReceipt."""
    provider_incident_id: str | Unset = UNSET
    """Provider incident associated with this MCPReadinessAttentionReceipt."""
    provider_occurred_at: datetime.datetime | Unset = UNSET
    """Signed provider event time retained in this MCPReadinessAttentionReceipt."""
    expires_at: datetime.datetime | Unset = UNSET
    reminder_policy: MCPReadinessReminderPolicy | Unset = UNSET
    """Per-incident lifetime creation budget. Both zero disables; otherwise interval is 60–86400 seconds and budget
    is 1–10. Reconfiguration never resets the count."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        incident_id = str(self.incident_id)

        action: str = self.action

        reason = self.reason

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        version = self.version

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

        reminder_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reminder_policy, Unset):
            reminder_policy = self.reminder_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "incident_id": incident_id,
                "action": action,
                "reason": reason,
                "created_by": created_by,
                "created_at": created_at,
                "version": version,
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
        if reminder_policy is not UNSET:
            field_dict["reminder_policy"] = reminder_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        incident_id = UUID(d.pop("incident_id"))

        action = check_mcp_readiness_attention_receipt_action(d.pop("action"))

        reason = d.pop("reason")

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        version = d.pop("version")

        _source = d.pop("source", UNSET)
        source: MCPReadinessAttentionReceiptSource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = check_mcp_readiness_attention_receipt_source(_source)

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

        _reminder_policy = d.pop("reminder_policy", UNSET)
        reminder_policy: MCPReadinessReminderPolicy | Unset
        if isinstance(_reminder_policy, Unset):
            reminder_policy = UNSET
        else:
            reminder_policy = MCPReadinessReminderPolicy.from_dict(_reminder_policy)

        mcp_readiness_attention_receipt = cls(
            request_id=request_id,
            incident_id=incident_id,
            action=action,
            reason=reason,
            created_by=created_by,
            created_at=created_at,
            version=version,
            source=source,
            provider_event_id=provider_event_id,
            provider_incident_id=provider_incident_id,
            provider_occurred_at=provider_occurred_at,
            expires_at=expires_at,
            reminder_policy=reminder_policy,
        )

        mcp_readiness_attention_receipt.additional_properties = d
        return mcp_readiness_attention_receipt

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
