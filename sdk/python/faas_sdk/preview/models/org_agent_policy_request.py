from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.org_agent_policy_request_allowed_scopes_item import (
    OrgAgentPolicyRequestAllowedScopesItem,
    check_org_agent_policy_request_allowed_scopes_item,
)
from ..models.org_agent_policy_request_approval_categories_item import (
    OrgAgentPolicyRequestApprovalCategoriesItem,
    check_org_agent_policy_request_approval_categories_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OrgAgentPolicyRequest")


@_attrs_define
class OrgAgentPolicyRequest:
    """Whether agents may act in the organization, and the capabilities they may get."""

    agents_allowed: bool
    """Whether hosted MCP connections and agent keys may act in the organization."""
    allowed_scopes: list[OrgAgentPolicyRequestAllowedScopesItem]
    """Capabilities agents may get beyond inspection; empty for read-only."""
    approval_categories: list[OrgAgentPolicyRequestApprovalCategoriesItem] | Unset = UNSET
    """Kinds of action every agent acting in the organization must have a person approve; omit to keep the current
    ones."""

    def to_dict(self) -> dict[str, Any]:
        agents_allowed = self.agents_allowed

        allowed_scopes = []
        for allowed_scopes_item_data in self.allowed_scopes:
            allowed_scopes_item: str = allowed_scopes_item_data
            allowed_scopes.append(allowed_scopes_item)

        approval_categories: list[str] | Unset = UNSET
        if not isinstance(self.approval_categories, Unset):
            approval_categories = []
            for approval_categories_item_data in self.approval_categories:
                approval_categories_item: str = approval_categories_item_data
                approval_categories.append(approval_categories_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "agents_allowed": agents_allowed,
                "allowed_scopes": allowed_scopes,
            }
        )
        if approval_categories is not UNSET:
            field_dict["approval_categories"] = approval_categories

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        agents_allowed = d.pop("agents_allowed")

        allowed_scopes = []
        _allowed_scopes = d.pop("allowed_scopes")
        for allowed_scopes_item_data in _allowed_scopes:
            allowed_scopes_item = check_org_agent_policy_request_allowed_scopes_item(allowed_scopes_item_data)

            allowed_scopes.append(allowed_scopes_item)

        _approval_categories = d.pop("approval_categories", UNSET)
        approval_categories: list[OrgAgentPolicyRequestApprovalCategoriesItem] | Unset = UNSET
        if _approval_categories is not UNSET:
            approval_categories = []
            for approval_categories_item_data in _approval_categories:
                approval_categories_item = check_org_agent_policy_request_approval_categories_item(
                    approval_categories_item_data
                )

                approval_categories.append(approval_categories_item)

        org_agent_policy_request = cls(
            agents_allowed=agents_allowed,
            allowed_scopes=allowed_scopes,
            approval_categories=approval_categories,
        )

        return org_agent_policy_request
