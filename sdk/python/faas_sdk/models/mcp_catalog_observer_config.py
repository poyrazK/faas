from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_observer_o_auth_config import MCPCatalogObserverOAuthConfig


T = TypeVar("T", bound="MCPCatalogObserverConfig")


@_attrs_define
class MCPCatalogObserverConfig:
    """Opt-in platform observer intent for one MCP role using its canonical app origin and an optional sealed secret
    reference.

    """

    enabled: bool
    paused: bool
    interval_seconds: int = 30
    timeout_seconds: int = 10
    endpoint: str = "/mcp"
    secret_ref: str | Unset = UNSET
    """App secret in the connection environment named secret:NAME; its current value is loaded for each check"""
    oauth: MCPCatalogObserverOAuthConfig | Unset = UNSET
    """Explicit OAuth refresh grant for one app and role; credentials are app secret references in the connection
    environment."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        paused = self.paused

        interval_seconds = self.interval_seconds

        timeout_seconds = self.timeout_seconds

        endpoint = self.endpoint

        secret_ref = self.secret_ref

        oauth: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth, Unset):
            oauth = self.oauth.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "paused": paused,
                "interval_seconds": interval_seconds,
                "timeout_seconds": timeout_seconds,
                "endpoint": endpoint,
            }
        )
        if secret_ref is not UNSET:
            field_dict["secret_ref"] = secret_ref
        if oauth is not UNSET:
            field_dict["oauth"] = oauth

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_observer_o_auth_config import MCPCatalogObserverOAuthConfig

        d = dict(src_dict)
        enabled = d.pop("enabled")

        paused = d.pop("paused")

        interval_seconds = d.pop("interval_seconds")

        timeout_seconds = d.pop("timeout_seconds")

        endpoint = d.pop("endpoint")

        secret_ref = d.pop("secret_ref", UNSET)

        _oauth = d.pop("oauth", UNSET)
        oauth: MCPCatalogObserverOAuthConfig | Unset
        if isinstance(_oauth, Unset):
            oauth = UNSET
        else:
            oauth = MCPCatalogObserverOAuthConfig.from_dict(_oauth)

        mcp_catalog_observer_config = cls(
            enabled=enabled,
            paused=paused,
            interval_seconds=interval_seconds,
            timeout_seconds=timeout_seconds,
            endpoint=endpoint,
            secret_ref=secret_ref,
            oauth=oauth,
        )

        mcp_catalog_observer_config.additional_properties = d
        return mcp_catalog_observer_config

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
