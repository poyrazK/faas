from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_monitor_snapshot_state import (
    MCPCatalogMonitorSnapshotState,
    check_mcp_catalog_monitor_snapshot_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_monitor_config import MCPCatalogMonitorConfig
    from ..models.mcp_catalog_monitor_snapshot_roles import MCPCatalogMonitorSnapshotRoles
    from ..models.mcp_catalog_o_auth_lifecycle import MCPCatalogOAuthLifecycle
    from ..models.mcp_catalog_observer_status import MCPCatalogObserverStatus
    from ..models.mcp_catalog_receipt import MCPCatalogReceipt


T = TypeVar("T", bound="MCPCatalogMonitorSnapshot")


@_attrs_define
class MCPCatalogMonitorSnapshot:
    """Configuration, latest observation and bounded transition history for a catalog monitor."""

    config: MCPCatalogMonitorConfig
    """Server-issued revision and retained settings for a configured MCP catalog role."""
    history: list[MCPCatalogReceipt]
    state: MCPCatalogMonitorSnapshotState
    oauth_lifecycle: MCPCatalogOAuthLifecycle | Unset = UNSET
    """Durable local connection state, provider revocation progress and bounded metadata audit history."""
    observer_status: MCPCatalogObserverStatus | Unset = UNSET
    """Managed observer scheduling and recovery metadata without credential values or raw source errors."""
    roles: MCPCatalogMonitorSnapshotRoles | Unset = UNSET
    """Additional role views when included; role-scoped reads omit this field. Use all_roles for aggregate state."""
    checked_at: datetime.datetime | Unset = UNSET
    latest: MCPCatalogReceipt | Unset = UNSET
    """Retained catalog transition with server-computed drift, hashes and capture time."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        config = self.config.to_dict()

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        state: str = self.state

        oauth_lifecycle: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth_lifecycle, Unset):
            oauth_lifecycle = self.oauth_lifecycle.to_dict()

        observer_status: dict[str, Any] | Unset = UNSET
        if not isinstance(self.observer_status, Unset):
            observer_status = self.observer_status.to_dict()

        roles: dict[str, Any] | Unset = UNSET
        if not isinstance(self.roles, Unset):
            roles = self.roles.to_dict()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        latest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest, Unset):
            latest = self.latest.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "config": config,
                "history": history,
                "state": state,
            }
        )
        if oauth_lifecycle is not UNSET:
            field_dict["oauth_lifecycle"] = oauth_lifecycle
        if observer_status is not UNSET:
            field_dict["observer_status"] = observer_status
        if roles is not UNSET:
            field_dict["roles"] = roles
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if latest is not UNSET:
            field_dict["latest"] = latest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_monitor_config import MCPCatalogMonitorConfig
        from ..models.mcp_catalog_monitor_snapshot_roles import MCPCatalogMonitorSnapshotRoles
        from ..models.mcp_catalog_o_auth_lifecycle import MCPCatalogOAuthLifecycle
        from ..models.mcp_catalog_observer_status import MCPCatalogObserverStatus
        from ..models.mcp_catalog_receipt import MCPCatalogReceipt

        d = dict(src_dict)
        config = MCPCatalogMonitorConfig.from_dict(d.pop("config"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = MCPCatalogReceipt.from_dict(history_item_data)

            history.append(history_item)

        state = check_mcp_catalog_monitor_snapshot_state(d.pop("state"))

        _oauth_lifecycle = d.pop("oauth_lifecycle", UNSET)
        oauth_lifecycle: MCPCatalogOAuthLifecycle | Unset
        if isinstance(_oauth_lifecycle, Unset):
            oauth_lifecycle = UNSET
        else:
            oauth_lifecycle = MCPCatalogOAuthLifecycle.from_dict(_oauth_lifecycle)

        _observer_status = d.pop("observer_status", UNSET)
        observer_status: MCPCatalogObserverStatus | Unset
        if isinstance(_observer_status, Unset):
            observer_status = UNSET
        else:
            observer_status = MCPCatalogObserverStatus.from_dict(_observer_status)

        _roles = d.pop("roles", UNSET)
        roles: MCPCatalogMonitorSnapshotRoles | Unset
        if isinstance(_roles, Unset):
            roles = UNSET
        else:
            roles = MCPCatalogMonitorSnapshotRoles.from_dict(_roles)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _latest = d.pop("latest", UNSET)
        latest: MCPCatalogReceipt | Unset
        if isinstance(_latest, Unset):
            latest = UNSET
        else:
            latest = MCPCatalogReceipt.from_dict(_latest)

        mcp_catalog_monitor_snapshot = cls(
            config=config,
            history=history,
            state=state,
            oauth_lifecycle=oauth_lifecycle,
            observer_status=observer_status,
            roles=roles,
            checked_at=checked_at,
            latest=latest,
        )

        mcp_catalog_monitor_snapshot.additional_properties = d
        return mcp_catalog_monitor_snapshot

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
