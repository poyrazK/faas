from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_deploy_token_request_scopes_item import (
    CreateDeployTokenRequestScopesItem,
    check_create_deploy_token_request_scopes_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateDeployTokenRequest")


@_attrs_define
class CreateDeployTokenRequest:
    """Create an app-bound credential. Standard tokens receive deploy:write; agent=true permits a narrow MCP permission set
    while preserving the native app boundary.

    """

    agent: bool | Unset = UNSET
    """Mint an app-bound headless MCP credential. Omit for a standard deploy token."""
    scopes: list[CreateDeployTokenRequestScopesItem] | Unset = UNSET
    """Requires agent=true. Agent and apps:read are included; omitted scopes also include deploy:write."""
    label: str | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    """Optional future expiry; defaults to 90 days and is capped at 365 days."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        agent = self.agent

        scopes: list[str] | Unset = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = []
            for scopes_item_data in self.scopes:
                scopes_item: str = scopes_item_data
                scopes.append(scopes_item)

        label = self.label

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if agent is not UNSET:
            field_dict["agent"] = agent
        if scopes is not UNSET:
            field_dict["scopes"] = scopes
        if label is not UNSET:
            field_dict["label"] = label
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        agent = d.pop("agent", UNSET)

        _scopes = d.pop("scopes", UNSET)
        scopes: list[CreateDeployTokenRequestScopesItem] | Unset = UNSET
        if _scopes is not UNSET:
            scopes = []
            for scopes_item_data in _scopes:
                scopes_item = check_create_deploy_token_request_scopes_item(scopes_item_data)

                scopes.append(scopes_item)

        label = d.pop("label", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        create_deploy_token_request = cls(
            agent=agent,
            scopes=scopes,
            label=label,
            expires_at=expires_at,
        )

        create_deploy_token_request.additional_properties = d
        return create_deploy_token_request

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
