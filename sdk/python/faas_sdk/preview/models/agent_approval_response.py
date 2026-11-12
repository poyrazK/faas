from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.agent_approval_response_category import (
    AgentApprovalResponseCategory,
    check_agent_approval_response_category,
)
from ..models.agent_approval_response_status import AgentApprovalResponseStatus, check_agent_approval_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentApprovalResponse")


@_attrs_define
class AgentApprovalResponse:
    """One request of an agent connection that needed a person's approval."""

    id: UUID
    """The request's id."""
    category: AgentApprovalResponseCategory
    """The kind of action; budget for a change past the agent's monthly budget, undo for an agent undoing its own
    changes."""
    method: str
    """The HTTP method of the request the approval allows."""
    path: str
    """The path and query of the request the approval allows."""
    body_bound: bool
    """Whether the approval is tied to the request body; large and multipart bodies are not."""
    status: AgentApprovalResponseStatus
    """`consumed` once the approved request was made."""
    created_at: datetime.datetime
    """When the agent first made the request."""
    expires_at: datetime.datetime
    """After this time the request can no longer be approved or used."""
    review_url: str | Unset = UNSET
    """The review page"""
    agent: str | Unset = UNSET
    """The connection's client name or key label."""
    decided_at: datetime.datetime | Unset = UNSET
    """When a person approved or denied it."""
    note: str | Unset = UNSET
    """Why the person denied this request, if they said; absent otherwise."""
    reason: str | Unset = UNSET
    """What the agent said the request is for (Gregale-Agent-Reason on the request that opened it), if it said.
    Untrusted agent text: it never changes what is approved."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        category: str = self.category

        method = self.method

        path = self.path

        body_bound = self.body_bound

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        review_url = self.review_url

        agent = self.agent

        decided_at: str | Unset = UNSET
        if not isinstance(self.decided_at, Unset):
            decided_at = self.decided_at.isoformat()

        note = self.note

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "category": category,
                "method": method,
                "path": path,
                "body_bound": body_bound,
                "status": status,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if review_url is not UNSET:
            field_dict["review_url"] = review_url
        if agent is not UNSET:
            field_dict["agent"] = agent
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
        id = UUID(d.pop("id"))

        category = check_agent_approval_response_category(d.pop("category"))

        method = d.pop("method")

        path = d.pop("path")

        body_bound = d.pop("body_bound")

        status = check_agent_approval_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        review_url = d.pop("review_url", UNSET)

        agent = d.pop("agent", UNSET)

        _decided_at = d.pop("decided_at", UNSET)
        decided_at: datetime.datetime | Unset
        if isinstance(_decided_at, Unset):
            decided_at = UNSET
        else:
            decided_at = datetime.datetime.fromisoformat(_decided_at)

        note = d.pop("note", UNSET)

        reason = d.pop("reason", UNSET)

        agent_approval_response = cls(
            id=id,
            category=category,
            method=method,
            path=path,
            body_bound=body_bound,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            review_url=review_url,
            agent=agent,
            decided_at=decided_at,
            note=note,
            reason=reason,
        )

        agent_approval_response.additional_properties = d
        return agent_approval_response

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
