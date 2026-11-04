from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.mcp_readiness_role_catalog_format import (
    MCPReadinessRoleCatalogFormat,
    check_mcp_readiness_role_catalog_format,
)
from ..models.mcp_readiness_role_protocol_version import (
    MCPReadinessRoleProtocolVersion,
    check_mcp_readiness_role_protocol_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_monitor_config import MCPReadinessMonitorConfig


T = TypeVar("T", bound="MCPReadinessRole")


@_attrs_define
class MCPReadinessRole:
    """Role-specific protocol, catalog and monitoring requirements in a readiness policy."""

    name: str
    protocol_version: MCPReadinessRoleProtocolVersion
    catalog_format: MCPReadinessRoleCatalogFormat
    catalog_sha256: str
    monitor: MCPReadinessMonitorConfig | Unset = UNSET
    """Schedule, failure threshold and connection settings for continuous readiness probes."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        protocol_version: str = self.protocol_version

        catalog_format: int = self.catalog_format

        catalog_sha256 = self.catalog_sha256

        monitor: dict[str, Any] | Unset = UNSET
        if not isinstance(self.monitor, Unset):
            monitor = self.monitor.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "protocol_version": protocol_version,
                "catalog_format": catalog_format,
                "catalog_sha256": catalog_sha256,
            }
        )
        if monitor is not UNSET:
            field_dict["monitor"] = monitor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_monitor_config import MCPReadinessMonitorConfig

        d = dict(src_dict)
        name = d.pop("name")

        protocol_version = check_mcp_readiness_role_protocol_version(d.pop("protocol_version"))

        catalog_format = check_mcp_readiness_role_catalog_format(d.pop("catalog_format"))

        catalog_sha256 = d.pop("catalog_sha256")

        _monitor = d.pop("monitor", UNSET)
        monitor: MCPReadinessMonitorConfig | Unset
        if isinstance(_monitor, Unset):
            monitor = UNSET
        else:
            monitor = MCPReadinessMonitorConfig.from_dict(_monitor)

        mcp_readiness_role = cls(
            name=name,
            protocol_version=protocol_version,
            catalog_format=catalog_format,
            catalog_sha256=catalog_sha256,
            monitor=monitor,
        )

        return mcp_readiness_role
