from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_monitor_config_oauth_connections import MCPReadinessMonitorConfigOauthConnections
    from ..models.mcp_readiness_o_auth_connection import MCPReadinessOAuthConnection


T = TypeVar("T", bound="MCPReadinessMonitorConfig")


@_attrs_define
class MCPReadinessMonitorConfig:
    """Schedule, failure threshold and connection settings for continuous readiness probes."""

    enabled: bool
    interval_seconds: int
    failure_threshold: int
    oauth_connections: MCPReadinessMonitorConfigOauthConnections | Unset = UNSET
    """Environment scope to pinned connection. Mutually exclusive with oauth_connection and secret_ref. Missing
    environments fail closed."""
    oauth_connection: MCPReadinessOAuthConnection | Unset = UNSET
    """App-local managed OAuth connection in the default environment scope, pinned to its current revision."""
    secret_ref: str | Unset = UNSET
    """Scoped app-secret reference; no plaintext credentials."""

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        interval_seconds = self.interval_seconds

        failure_threshold = self.failure_threshold

        oauth_connections: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth_connections, Unset):
            oauth_connections = self.oauth_connections.to_dict()

        oauth_connection: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth_connection, Unset):
            oauth_connection = self.oauth_connection.to_dict()

        secret_ref = self.secret_ref

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
                "interval_seconds": interval_seconds,
                "failure_threshold": failure_threshold,
            }
        )
        if oauth_connections is not UNSET:
            field_dict["oauth_connections"] = oauth_connections
        if oauth_connection is not UNSET:
            field_dict["oauth_connection"] = oauth_connection
        if secret_ref is not UNSET:
            field_dict["secret_ref"] = secret_ref

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_monitor_config_oauth_connections import MCPReadinessMonitorConfigOauthConnections
        from ..models.mcp_readiness_o_auth_connection import MCPReadinessOAuthConnection

        d = dict(src_dict)
        enabled = d.pop("enabled")

        interval_seconds = d.pop("interval_seconds")

        failure_threshold = d.pop("failure_threshold")

        _oauth_connections = d.pop("oauth_connections", UNSET)
        oauth_connections: MCPReadinessMonitorConfigOauthConnections | Unset
        if isinstance(_oauth_connections, Unset):
            oauth_connections = UNSET
        else:
            oauth_connections = MCPReadinessMonitorConfigOauthConnections.from_dict(_oauth_connections)

        _oauth_connection = d.pop("oauth_connection", UNSET)
        oauth_connection: MCPReadinessOAuthConnection | Unset
        if isinstance(_oauth_connection, Unset):
            oauth_connection = UNSET
        else:
            oauth_connection = MCPReadinessOAuthConnection.from_dict(_oauth_connection)

        secret_ref = d.pop("secret_ref", UNSET)

        mcp_readiness_monitor_config = cls(
            enabled=enabled,
            interval_seconds=interval_seconds,
            failure_threshold=failure_threshold,
            oauth_connections=oauth_connections,
            oauth_connection=oauth_connection,
            secret_ref=secret_ref,
        )

        return mcp_readiness_monitor_config
