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

T = TypeVar("T", bound="AgentApprovalPendingInfo")


@_attrs_define
class AgentApprovalPendingInfo:
    """The approval request behind an approval_required problem: a person opens review_url to decide, then the agent
    repeats the same request once.

    """

    id: UUID
    """The id of the pending approval request."""
    category: AgentApprovalPendingInfoCategory
    """The kind of action that needs approval."""
    review_url: str
    """The page where a signed-in member who may decide approves or denies the request."""
    expires_at: datetime.datetime
    """When the request and any approval of it expire."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        category: str = self.category

        review_url = self.review_url

        expires_at = self.expires_at.isoformat()

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        category = check_agent_approval_pending_info_category(d.pop("category"))

        review_url = d.pop("review_url")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        agent_approval_pending_info = cls(
            id=id,
            category=category,
            review_url=review_url,
            expires_at=expires_at,
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
