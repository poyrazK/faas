from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_role import MCPReadinessRole


T = TypeVar("T", bound="SetMCPReadinessPolicyRequest")


@_attrs_define
class SetMCPReadinessPolicyRequest:
    """Replacement readiness configuration with expected-revision concurrency control."""

    expected_revision: str
    enabled: bool
    endpoint: str
    max_age_seconds: int
    roles: list[MCPReadinessRole]
    reason: str
    scope: str | Unset = UNSET
    """Environment whose readiness policy is replaced by this request. Omit for default."""

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        enabled = self.enabled

        endpoint = self.endpoint

        max_age_seconds = self.max_age_seconds

        roles = []
        for roles_item_data in self.roles:
            roles_item = roles_item_data.to_dict()
            roles.append(roles_item)

        reason = self.reason

        scope = self.scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "enabled": enabled,
                "endpoint": endpoint,
                "max_age_seconds": max_age_seconds,
                "roles": roles,
                "reason": reason,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_role import MCPReadinessRole

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        enabled = d.pop("enabled")

        endpoint = d.pop("endpoint")

        max_age_seconds = d.pop("max_age_seconds")

        roles = []
        _roles = d.pop("roles")
        for roles_item_data in _roles:
            roles_item = MCPReadinessRole.from_dict(roles_item_data)

            roles.append(roles_item)

        reason = d.pop("reason")

        scope = d.pop("scope", UNSET)

        set_mcp_readiness_policy_request = cls(
            expected_revision=expected_revision,
            enabled=enabled,
            endpoint=endpoint,
            max_age_seconds=max_age_seconds,
            roles=roles,
            reason=reason,
            scope=scope,
        )

        return set_mcp_readiness_policy_request
