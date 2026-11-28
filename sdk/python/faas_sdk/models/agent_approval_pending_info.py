from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.agent_approval_pending_info_category import (
    AgentApprovalPendingInfoCategory,
    check_agent_approval_pending_info_category,
)
from ..models.agent_approval_pending_info_status import (
    AgentApprovalPendingInfoStatus,
    check_agent_approval_pending_info_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentApprovalPendingInfo")


@_attrs_define
class AgentApprovalPendingInfo:
    """The approval request behind an approval_required, approval_denied or approval_expired problem. On approval_required
    a person opens review_url to decide, then the agent repeats the same request once; on the other two the agent should
    stop repeating it.

    """

    id: UUID
    """The id of the approval request."""
    category: AgentApprovalPendingInfoCategory
    """The kind of action that needs approval; budget when the change would take the agent past its monthly budget,
    undo when the agent applies an undo of its own changes."""
    review_url: str
    """The page where a signed-in member who may decide approves or denies the request."""
    expires_at: datetime.datetime
    """When the request and any approval of it expire."""
    status: AgentApprovalPendingInfoStatus | Unset = UNSET
    """Set on approval_denied and approval_expired: what became of the request."""
    note: str | Unset = UNSET
    """The person's reason for denying the request, when they gave one."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        category: str = self.category

        review_url = self.review_url

        expires_at = self.expires_at.isoformat()

        status: str | Unset = UNSET
        if not isinstance(self.status, Unset):
            status = self.status

        note = self.note

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "category": category,
                "review_url": review_url,
                "expires_at": expires_at,
            }
        )
        if status is not UNSET:
            field_dict["status"] = status
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        category = check_agent_approval_pending_info_category(d.pop("category"))

        review_url = d.pop("review_url")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _status = d.pop("status", UNSET)
        status: AgentApprovalPendingInfoStatus | Unset
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = check_agent_approval_pending_info_status(_status)

        note = d.pop("note", UNSET)

        agent_approval_pending_info = cls(
            id=id,
            category=category,
            review_url=review_url,
            expires_at=expires_at,
            status=status,
            note=note,
        )

        agent_approval_pending_info.additional_properties = d
        return agent_approval_pending_info

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
