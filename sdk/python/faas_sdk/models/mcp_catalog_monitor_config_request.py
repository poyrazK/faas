from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_monitor_config_request_baseline import MCPCatalogMonitorConfigRequestBaseline
    from ..models.mcp_catalog_observer_config import MCPCatalogObserverConfig


T = TypeVar("T", bound="MCPCatalogMonitorConfigRequest")


@_attrs_define
class MCPCatalogMonitorConfigRequest:
    """Reviewed baseline and observer settings for one independently monitored MCP role."""

    enabled: bool
    baseline: MCPCatalogMonitorConfigRequestBaseline
    """Reviewed format-2 MCP catalog contract"""
    freshness_seconds: int = 90
    scope: str | Unset = UNSET
    """Environment selected when configuring catalog monitoring. Omit for default."""
    observer: MCPCatalogObserverConfig | Unset = UNSET
    """Opt-in platform observer intent for one MCP role using its canonical app origin and an optional sealed
    secret reference."""
    token_env: str | Unset = UNSET
    """Environment variable name only; credentials are never stored"""
    role: str | Unset = "default"
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        baseline = self.baseline.to_dict()

        freshness_seconds = self.freshness_seconds

        scope = self.scope

        observer: dict[str, Any] | Unset = UNSET
        if not isinstance(self.observer, Unset):
            observer = self.observer.to_dict()

        token_env = self.token_env

        role = self.role

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "baseline": baseline,
                "freshness_seconds": freshness_seconds,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if observer is not UNSET:
            field_dict["observer"] = observer
        if token_env is not UNSET:
            field_dict["token_env"] = token_env
        if role is not UNSET:
            field_dict["role"] = role

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_monitor_config_request_baseline import MCPCatalogMonitorConfigRequestBaseline
        from ..models.mcp_catalog_observer_config import MCPCatalogObserverConfig

        d = dict(src_dict)
        enabled = d.pop("enabled")

        baseline = MCPCatalogMonitorConfigRequestBaseline.from_dict(d.pop("baseline"))

        freshness_seconds = d.pop("freshness_seconds")

        scope = d.pop("scope", UNSET)

        _observer = d.pop("observer", UNSET)
        observer: MCPCatalogObserverConfig | Unset
        if isinstance(_observer, Unset):
            observer = UNSET
        else:
            observer = MCPCatalogObserverConfig.from_dict(_observer)

        token_env = d.pop("token_env", UNSET)

        role = d.pop("role", UNSET)

        mcp_catalog_monitor_config_request = cls(
            enabled=enabled,
            baseline=baseline,
            freshness_seconds=freshness_seconds,
            scope=scope,
            observer=observer,
            token_env=token_env,
            role=role,
        )

        mcp_catalog_monitor_config_request.additional_properties = d
        return mcp_catalog_monitor_config_request

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
