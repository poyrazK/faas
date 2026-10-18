from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_monitor_config_baseline import MCPCatalogMonitorConfigBaseline
    from ..models.mcp_catalog_observer_config import MCPCatalogObserverConfig


T = TypeVar("T", bound="MCPCatalogMonitorConfig")


@_attrs_define
class MCPCatalogMonitorConfig:
    """Server-issued revision and retained settings for a configured MCP catalog role."""

    enabled: bool
    revision: UUID
    baseline: MCPCatalogMonitorConfigBaseline
    baseline_sha256: str
    freshness_seconds: int
    scope: str | Unset = UNSET
    """Environment watched by this configured MCP catalog monitor. Defaults to default."""
    observer: MCPCatalogObserverConfig | Unset = UNSET
    """Opt-in platform observer intent for one MCP role using its canonical app origin and an optional sealed
    secret reference."""
    token_env: str | Unset = UNSET
    """Token environment reference selected by observers; no secret value is retained"""
    role: str | Unset = "default"
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        revision = str(self.revision)

        baseline = self.baseline.to_dict()

        baseline_sha256 = self.baseline_sha256

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
                "revision": revision,
                "baseline": baseline,
                "baseline_sha256": baseline_sha256,
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
        from ..models.mcp_catalog_monitor_config_baseline import MCPCatalogMonitorConfigBaseline
        from ..models.mcp_catalog_observer_config import MCPCatalogObserverConfig

        d = dict(src_dict)
        enabled = d.pop("enabled")

        revision = UUID(d.pop("revision"))

        baseline = MCPCatalogMonitorConfigBaseline.from_dict(d.pop("baseline"))

        baseline_sha256 = d.pop("baseline_sha256")

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

        mcp_catalog_monitor_config = cls(
            enabled=enabled,
            revision=revision,
            baseline=baseline,
            baseline_sha256=baseline_sha256,
            freshness_seconds=freshness_seconds,
            scope=scope,
            observer=observer,
            token_env=token_env,
            role=role,
        )

        mcp_catalog_monitor_config.additional_properties = d
        return mcp_catalog_monitor_config

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
