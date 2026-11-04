from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.org_agent_policy_request_allowed_scopes_item import (
    OrgAgentPolicyRequestAllowedScopesItem,
    check_org_agent_policy_request_allowed_scopes_item,
)

T = TypeVar("T", bound="OrgAgentPolicyRequest")


@_attrs_define
class OrgAgentPolicyRequest:
    """Whether agents may act in the organization, and the capabilities they may get."""

    agents_allowed: bool
    """Whether hosted MCP connections and agent keys may act in the organization."""
    allowed_scopes: list[OrgAgentPolicyRequestAllowedScopesItem]
    """Capabilities agents may get beyond inspection; empty for read-only."""

    def to_dict(self) -> dict[str, Any]:
        agents_allowed = self.agents_allowed

        allowed_scopes = []
        for allowed_scopes_item_data in self.allowed_scopes:
            allowed_scopes_item: str = allowed_scopes_item_data
            allowed_scopes.append(allowed_scopes_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "agents_allowed": agents_allowed,
                "allowed_scopes": allowed_scopes,
            }
        )

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

        org_agent_policy_request = cls(
            agents_allowed=agents_allowed,
            allowed_scopes=allowed_scopes,
        )

        return org_agent_policy_request
