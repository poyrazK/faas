from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.agent_approval_webhook_payload_category import (
    AgentApprovalWebhookPayloadCategory,
    check_agent_approval_webhook_payload_category,
)
from ..models.agent_approval_webhook_payload_status import (
    AgentApprovalWebhookPayloadStatus,
    check_agent_approval_webhook_payload_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentApprovalWebhookPayload")


@_attrs_define
class AgentApprovalWebhookPayload:
    """Delivered with agent.approval.requested, agent.approval.approved and agent.approval.denied: an agent key's request
    waits for a person, or a person decided it. It never carries the request body.

    """

    approval_id: UUID
    """The approval request this event is about."""
    key_id: UUID
    """The agent key that made the request."""
    category: AgentApprovalWebhookPayloadCategory
    """Which kind of risky action the agent asked to take; budget when it would go past its monthly budget, undo
    when it undoes its own changes."""
    method: str
    """HTTP method of the agent's request."""
    path: str
    """Path and query of the agent's request."""
    status: AgentApprovalWebhookPayloadStatus
    """The request's status when the event was recorded."""
    expires_at: datetime.datetime
    """When the request stops being approvable or usable."""
    org_id: UUID | Unset = UNSET
    """The organization the key acts in"""
    review_url: str | Unset = UNSET
    """On agent.approval.requested: the page where a member who may decide opens it."""
    decided_at: datetime.datetime | Unset = UNSET
    """When the person approved or denied the request."""
    note: str | Unset = UNSET
    """On agent.approval.denied: the reason the person gave the agent, if any."""
    reason: str | Unset = UNSET
    """The agent's own account of what the request is for, if it gave one. Untrusted agent text."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        approval_id = str(self.approval_id)

        key_id = str(self.key_id)

        category: str = self.category

        method = self.method

        path = self.path

        status: str = self.status

        expires_at = self.expires_at.isoformat()

        org_id: str | Unset = UNSET
        if not isinstance(self.org_id, Unset):
            org_id = str(self.org_id)

        review_url = self.review_url

        decided_at: str | Unset = UNSET
        if not isinstance(self.decided_at, Unset):
            decided_at = self.decided_at.isoformat()

        note = self.note

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "approval_id": approval_id,
                "key_id": key_id,
                "category": category,
                "method": method,
                "path": path,
                "status": status,
                "expires_at": expires_at,
            }
        )
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if review_url is not UNSET:
            field_dict["review_url"] = review_url
        if decided_at is not UNSET:
            field_dict["decided_at"] = decided_at
        if note is not UNSET:
            field_dict["note"] = note
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        approval_id = UUID(d.pop("approval_id"))

        key_id = UUID(d.pop("key_id"))

        category = check_agent_approval_webhook_payload_category(d.pop("category"))

        method = d.pop("method")

        path = d.pop("path")

        status = check_agent_approval_webhook_payload_status(d.pop("status"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _org_id = d.pop("org_id", UNSET)
        org_id: UUID | Unset
        if isinstance(_org_id, Unset):
            org_id = UNSET
        else:
            org_id = UUID(_org_id)

        review_url = d.pop("review_url", UNSET)

        _decided_at = d.pop("decided_at", UNSET)
        decided_at: datetime.datetime | Unset
        if isinstance(_decided_at, Unset):
            decided_at = UNSET
        else:
            decided_at = datetime.datetime.fromisoformat(_decided_at)

        note = d.pop("note", UNSET)

        reason = d.pop("reason", UNSET)

        agent_approval_webhook_payload = cls(
            approval_id=approval_id,
            key_id=key_id,
            category=category,
            method=method,
            path=path,
            status=status,
            expires_at=expires_at,
            org_id=org_id,
            review_url=review_url,
            decided_at=decided_at,
            note=note,
            reason=reason,
        )

        agent_approval_webhook_payload.additional_properties = d
        return agent_approval_webhook_payload

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
