from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_connection_health_issue_kind import (
    MCPConnectionHealthIssueKind,
    check_mcp_connection_health_issue_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPConnectionHealthIssue")


@_attrs_define
class MCPConnectionHealthIssue:
    """Actionable connection health metadata without sealed grants or provider responses."""

    id: str
    app_id: UUID
    app_slug: str
    role: str
    kind: MCPConnectionHealthIssueKind
    state: str
    recovery_action: str
    recovery_path: str
    scope: str | Unset = UNSET
    """Environment containing the MCP connection with this health issue. Empty means default."""
    app_deleted: bool | Unset = UNSET
    retirement_id: UUID | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = str(self.app_id)

        app_slug = self.app_slug

        role = self.role

        kind: str = self.kind

        state = self.state

        recovery_action = self.recovery_action

        recovery_path = self.recovery_path

        scope = self.scope

        app_deleted = self.app_deleted

        retirement_id: str | Unset = UNSET
        if not isinstance(self.retirement_id, Unset):
            retirement_id = str(self.retirement_id)

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "app_slug": app_slug,
                "role": role,
                "kind": kind,
                "state": state,
                "recovery_action": recovery_action,
                "recovery_path": recovery_path,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if app_deleted is not UNSET:
            field_dict["app_deleted"] = app_deleted
        if retirement_id is not UNSET:
            field_dict["retirement_id"] = retirement_id
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        app_id = UUID(d.pop("app_id"))

        app_slug = d.pop("app_slug")

        role = d.pop("role")

        kind = check_mcp_connection_health_issue_kind(d.pop("kind"))

        state = d.pop("state")

        recovery_action = d.pop("recovery_action")

        recovery_path = d.pop("recovery_path")

        scope = d.pop("scope", UNSET)

        app_deleted = d.pop("app_deleted", UNSET)

        _retirement_id = d.pop("retirement_id", UNSET)
        retirement_id: UUID | Unset
        if isinstance(_retirement_id, Unset):
            retirement_id = UNSET
        else:
            retirement_id = UUID(_retirement_id)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        mcp_connection_health_issue = cls(
            id=id,
            app_id=app_id,
            app_slug=app_slug,
            role=role,
            kind=kind,
            state=state,
            recovery_action=recovery_action,
            recovery_path=recovery_path,
            scope=scope,
            app_deleted=app_deleted,
            retirement_id=retirement_id,
            expires_at=expires_at,
        )

        mcp_connection_health_issue.additional_properties = d
        return mcp_connection_health_issue

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
