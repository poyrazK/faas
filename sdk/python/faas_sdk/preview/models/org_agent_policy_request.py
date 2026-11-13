from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

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
    approval_channels: list[str] | Unset = UNSET
    """Names of your own notification channels that get approval requests of agents acting in the organization;
    omit to keep them, send an empty list to remove them."""
    max_agent_budget_cents: int | Unset = UNSET
    """Ceiling, in cents a month, on the budget of every agent acting in the organization; omit to keep the current
    ceiling."""
    clear_max_agent_budget: bool | Unset = UNSET
    """Remove the ceiling on agents' budgets."""

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

        approval_channels: list[str] | Unset = UNSET
        if not isinstance(self.approval_channels, Unset):
            approval_channels = self.approval_channels

        max_agent_budget_cents = self.max_agent_budget_cents

        clear_max_agent_budget = self.clear_max_agent_budget

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "agents_allowed": agents_allowed,
                "allowed_scopes": allowed_scopes,
            }
        )
        if approval_categories is not UNSET:
            field_dict["approval_categories"] = approval_categories
        if approval_channels is not UNSET:
            field_dict["approval_channels"] = approval_channels
        if max_agent_budget_cents is not UNSET:
            field_dict["max_agent_budget_cents"] = max_agent_budget_cents
        if clear_max_agent_budget is not UNSET:
            field_dict["clear_max_agent_budget"] = clear_max_agent_budget

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

        approval_channels = cast(list[str], d.pop("approval_channels", UNSET))

        max_agent_budget_cents = d.pop("max_agent_budget_cents", UNSET)

        clear_max_agent_budget = d.pop("clear_max_agent_budget", UNSET)

        org_agent_policy_request = cls(
            agents_allowed=agents_allowed,
            allowed_scopes=allowed_scopes,
            approval_categories=approval_categories,
            approval_channels=approval_channels,
            max_agent_budget_cents=max_agent_budget_cents,
            clear_max_agent_budget=clear_max_agent_budget,
        )

        return org_agent_policy_request
