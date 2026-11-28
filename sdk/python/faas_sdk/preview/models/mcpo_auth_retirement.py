from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcpo_auth_retirement_state import MCPOAuthRetirementState, check_mcpo_auth_retirement_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_o_auth_revocation import MCPCatalogOAuthRevocation
    from ..models.mcpo_auth_retirement_event import MCPOAuthRetirementEvent


T = TypeVar("T", bound="MCPOAuthRetirement")


@_attrs_define
class MCPOAuthRetirement:
    """Account-owned grant retirement metadata surviving app deletion; sealed data is omitted from public responses."""

    id: UUID
    app_id: UUID
    app_slug: str
    role: str
    reason: str
    """Retirement cause or unknown issuance outcome"""
    state: MCPOAuthRetirementState
    created_at: datetime.datetime
    retain_until: datetime.datetime
    """Seven-day maximum sealed credential retention deadline"""
    revocation: MCPCatalogOAuthRevocation
    """Durable RFC 7009 progress; acknowledgement also covers tokens already invalid at the provider. Private
    fields are omitted from all public projections."""
    allow_active_grant_revocation: bool
    """Explicit operator authorization to retire a grant despite a matching active connection"""
    history: list[MCPOAuthRetirementEvent]
    scope: str | Unset = UNSET
    """Environment of the retired MCP OAuth connection. Empty means default."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        app_slug = self.app_slug

        role = self.role

        reason = self.reason

        state: str = self.state

        created_at = self.created_at.isoformat()

        retain_until = self.retain_until.isoformat()

        revocation = self.revocation.to_dict()

        allow_active_grant_revocation = self.allow_active_grant_revocation

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        scope = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "app_slug": app_slug,
                "role": role,
                "reason": reason,
                "state": state,
                "created_at": created_at,
                "retain_until": retain_until,
                "revocation": revocation,
                "allow_active_grant_revocation": allow_active_grant_revocation,
                "history": history,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_o_auth_revocation import MCPCatalogOAuthRevocation
        from ..models.mcpo_auth_retirement_event import MCPOAuthRetirementEvent

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        app_slug = d.pop("app_slug")

        role = d.pop("role")

        reason = d.pop("reason")

        state = check_mcpo_auth_retirement_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        revocation = MCPCatalogOAuthRevocation.from_dict(d.pop("revocation"))

        allow_active_grant_revocation = d.pop("allow_active_grant_revocation")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = MCPOAuthRetirementEvent.from_dict(history_item_data)

            history.append(history_item)

        scope = d.pop("scope", UNSET)

        mcpo_auth_retirement = cls(
            id=id,
            app_id=app_id,
            app_slug=app_slug,
            role=role,
            reason=reason,
            state=state,
            created_at=created_at,
            retain_until=retain_until,
            revocation=revocation,
            allow_active_grant_revocation=allow_active_grant_revocation,
            history=history,
            scope=scope,
        )

        mcpo_auth_retirement.additional_properties = d
        return mcpo_auth_retirement

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
