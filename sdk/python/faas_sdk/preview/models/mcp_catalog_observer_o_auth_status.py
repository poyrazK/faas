from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_observer_o_auth_status_state import (
    MCPCatalogObserverOAuthStatusState,
    check_mcp_catalog_observer_o_auth_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPCatalogObserverOAuthStatus")


@_attrs_define
class MCPCatalogObserverOAuthStatus:
    """Public OAuth connection health; sealed credentials and source fingerprint are omitted from responses."""

    state: MCPCatalogObserverOAuthStatusState
    retirement_id: UUID | Unset = UNSET
    """Internal issuance reservation identity omitted from public responses"""
    reauthorization_needed: bool | Unset = UNSET
    """Reconnect is required or credentials failed within fifteen minutes of expiry"""
    expires_at: datetime.datetime | Unset = UNSET
    last_renewal_at: datetime.datetime | Unset = UNSET
    ciphertext: str | Unset = UNSET
    """Internal sealed storage omitted from public responses"""
    source_sha256: str | Unset = UNSET
    """Internal source fingerprint omitted from public responses"""
    granted_scopes: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        retirement_id: str | Unset = UNSET
        if not isinstance(self.retirement_id, Unset):
            retirement_id = str(self.retirement_id)

        reauthorization_needed = self.reauthorization_needed

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        last_renewal_at: str | Unset = UNSET
        if not isinstance(self.last_renewal_at, Unset):
            last_renewal_at = self.last_renewal_at.isoformat()

        ciphertext = self.ciphertext

        source_sha256 = self.source_sha256

        granted_scopes: list[str] | Unset = UNSET
        if not isinstance(self.granted_scopes, Unset):
            granted_scopes = self.granted_scopes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if retirement_id is not UNSET:
            field_dict["retirement_id"] = retirement_id
        if reauthorization_needed is not UNSET:
            field_dict["reauthorization_needed"] = reauthorization_needed
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if last_renewal_at is not UNSET:
            field_dict["last_renewal_at"] = last_renewal_at
        if ciphertext is not UNSET:
            field_dict["ciphertext"] = ciphertext
        if source_sha256 is not UNSET:
            field_dict["source_sha256"] = source_sha256
        if granted_scopes is not UNSET:
            field_dict["granted_scopes"] = granted_scopes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_mcp_catalog_observer_o_auth_status_state(d.pop("state"))

        _retirement_id = d.pop("retirement_id", UNSET)
        retirement_id: UUID | Unset
        if isinstance(_retirement_id, Unset):
            retirement_id = UNSET
        else:
            retirement_id = UUID(_retirement_id)

        reauthorization_needed = d.pop("reauthorization_needed", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        _last_renewal_at = d.pop("last_renewal_at", UNSET)
        last_renewal_at: datetime.datetime | Unset
        if isinstance(_last_renewal_at, Unset):
            last_renewal_at = UNSET
        else:
            last_renewal_at = datetime.datetime.fromisoformat(_last_renewal_at)

        ciphertext = d.pop("ciphertext", UNSET)

        source_sha256 = d.pop("source_sha256", UNSET)

        granted_scopes = cast(list[str], d.pop("granted_scopes", UNSET))

        mcp_catalog_observer_o_auth_status = cls(
            state=state,
            retirement_id=retirement_id,
            reauthorization_needed=reauthorization_needed,
            expires_at=expires_at,
            last_renewal_at=last_renewal_at,
            ciphertext=ciphertext,
            source_sha256=source_sha256,
            granted_scopes=granted_scopes,
        )

        mcp_catalog_observer_o_auth_status.additional_properties = d
        return mcp_catalog_observer_o_auth_status

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
