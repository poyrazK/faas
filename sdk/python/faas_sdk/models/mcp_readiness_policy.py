from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_role import MCPReadinessRole


T = TypeVar("T", bound="MCPReadinessPolicy")


@_attrs_define
class MCPReadinessPolicy:
    """Saved environment readiness requirements with revision and audit context."""

    revision: str
    enabled: bool
    endpoint: str
    max_age_seconds: int
    roles: list[MCPReadinessRole]
    updated_at: datetime.datetime
    restored_from: str | Unset = UNSET
    """Historical source revision for a restore; audit metadata is always newly recorded."""
    scope: str | Unset = UNSET
    """Environment to which this saved readiness policy applies. Omitted means default."""
    reason: str | Unset = UNSET
    updated_by: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        enabled = self.enabled

        endpoint = self.endpoint

        max_age_seconds = self.max_age_seconds

        roles = []
        for roles_item_data in self.roles:
            roles_item = roles_item_data.to_dict()
            roles.append(roles_item)

        updated_at = self.updated_at.isoformat()

        restored_from = self.restored_from

        scope = self.scope

        reason = self.reason

        updated_by = self.updated_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "enabled": enabled,
                "endpoint": endpoint,
                "max_age_seconds": max_age_seconds,
                "roles": roles,
                "updated_at": updated_at,
            }
        )
        if restored_from is not UNSET:
            field_dict["restored_from"] = restored_from
        if scope is not UNSET:
            field_dict["scope"] = scope
        if reason is not UNSET:
            field_dict["reason"] = reason
        if updated_by is not UNSET:
            field_dict["updated_by"] = updated_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_role import MCPReadinessRole

        d = dict(src_dict)
        revision = d.pop("revision")

        enabled = d.pop("enabled")

        endpoint = d.pop("endpoint")

        max_age_seconds = d.pop("max_age_seconds")

        roles = []
        _roles = d.pop("roles")
        for roles_item_data in _roles:
            roles_item = MCPReadinessRole.from_dict(roles_item_data)

            roles.append(roles_item)

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        restored_from = d.pop("restored_from", UNSET)

        scope = d.pop("scope", UNSET)

        reason = d.pop("reason", UNSET)

        updated_by = d.pop("updated_by", UNSET)

        mcp_readiness_policy = cls(
            revision=revision,
            enabled=enabled,
            endpoint=endpoint,
            max_age_seconds=max_age_seconds,
            roles=roles,
            updated_at=updated_at,
            restored_from=restored_from,
            scope=scope,
            reason=reason,
            updated_by=updated_by,
        )

        return mcp_readiness_policy
