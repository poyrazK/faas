from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_o_auth_response_provider_revocation import (
    MCPCatalogOAuthResponseProviderRevocation,
    check_mcp_catalog_o_auth_response_provider_revocation,
)
from ..models.mcp_catalog_o_auth_response_state import (
    MCPCatalogOAuthResponseState,
    check_mcp_catalog_o_auth_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_o_auth_check import MCPCatalogOAuthCheck


T = TypeVar("T", bound="MCPCatalogOAuthResponse")


@_attrs_define
class MCPCatalogOAuthResponse:
    """Guided OAuth outcome; token and verifier values are never returned."""

    revision: UUID
    state: MCPCatalogOAuthResponseState
    authorization_url: str | Unset = UNSET
    callback_state: str | Unset = UNSET
    """One-time state to validate the local browser callback"""
    provider_revocation: MCPCatalogOAuthResponseProviderRevocation | Unset = UNSET
    checks: list[MCPCatalogOAuthCheck] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revision = str(self.revision)

        state: str = self.state

        authorization_url = self.authorization_url

        callback_state = self.callback_state

        provider_revocation: str | Unset = UNSET
        if not isinstance(self.provider_revocation, Unset):
            provider_revocation = self.provider_revocation

        checks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.checks, Unset):
            checks = []
            for checks_item_data in self.checks:
                checks_item = checks_item_data.to_dict()
                checks.append(checks_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revision": revision,
                "state": state,
            }
        )
        if authorization_url is not UNSET:
            field_dict["authorization_url"] = authorization_url
        if callback_state is not UNSET:
            field_dict["callback_state"] = callback_state
        if provider_revocation is not UNSET:
            field_dict["provider_revocation"] = provider_revocation
        if checks is not UNSET:
            field_dict["checks"] = checks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_o_auth_check import MCPCatalogOAuthCheck

        d = dict(src_dict)
        revision = UUID(d.pop("revision"))

        state = check_mcp_catalog_o_auth_response_state(d.pop("state"))

        authorization_url = d.pop("authorization_url", UNSET)

        callback_state = d.pop("callback_state", UNSET)

        _provider_revocation = d.pop("provider_revocation", UNSET)
        provider_revocation: MCPCatalogOAuthResponseProviderRevocation | Unset
        if isinstance(_provider_revocation, Unset):
            provider_revocation = UNSET
        else:
            provider_revocation = check_mcp_catalog_o_auth_response_provider_revocation(_provider_revocation)

        _checks = d.pop("checks", UNSET)
        checks: list[MCPCatalogOAuthCheck] | Unset = UNSET
        if _checks is not UNSET:
            checks = []
            for checks_item_data in _checks:
                checks_item = MCPCatalogOAuthCheck.from_dict(checks_item_data)

                checks.append(checks_item)

        mcp_catalog_o_auth_response = cls(
            revision=revision,
            state=state,
            authorization_url=authorization_url,
            callback_state=callback_state,
            provider_revocation=provider_revocation,
            checks=checks,
        )

        mcp_catalog_o_auth_response.additional_properties = d
        return mcp_catalog_o_auth_response

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
