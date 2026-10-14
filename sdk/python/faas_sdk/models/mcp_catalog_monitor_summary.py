from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_monitor_summary_state import (
    MCPCatalogMonitorSummaryState,
    check_mcp_catalog_monitor_summary_state,
)

if TYPE_CHECKING:
    from ..models.mcp_catalog_monitor_signal import MCPCatalogMonitorSignal
    from ..models.mcp_catalog_monitor_summary_roles import MCPCatalogMonitorSummaryRoles


T = TypeVar("T", bound="MCPCatalogMonitorSummary")


@_attrs_define
class MCPCatalogMonitorSummary:
    """App health and individual catalog views for every configured role."""

    state: MCPCatalogMonitorSummaryState
    roles: MCPCatalogMonitorSummaryRoles
    signal: MCPCatalogMonitorSignal
    """Freshness-aware catalog health and evidence links used in monitoring notifications."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        roles = self.roles.to_dict()

        signal = self.signal.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "roles": roles,
                "signal": signal,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_monitor_signal import MCPCatalogMonitorSignal
        from ..models.mcp_catalog_monitor_summary_roles import MCPCatalogMonitorSummaryRoles

        d = dict(src_dict)
        state = check_mcp_catalog_monitor_summary_state(d.pop("state"))

        roles = MCPCatalogMonitorSummaryRoles.from_dict(d.pop("roles"))

        signal = MCPCatalogMonitorSignal.from_dict(d.pop("signal"))

        mcp_catalog_monitor_summary = cls(
            state=state,
            roles=roles,
            signal=signal,
        )

        mcp_catalog_monitor_summary.additional_properties = d
        return mcp_catalog_monitor_summary

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
