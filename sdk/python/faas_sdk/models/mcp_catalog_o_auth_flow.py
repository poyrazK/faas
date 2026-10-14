from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="MCPCatalogOAuthFlow")


@_attrs_define
class MCPCatalogOAuthFlow:
    """Internal sealed one-time authorization state omitted from public monitor projections."""

    state_sha256: str
    ciphertext: str
    expires_at: datetime.datetime
    consumed: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state_sha256 = self.state_sha256

        ciphertext = self.ciphertext

        expires_at = self.expires_at.isoformat()

        consumed = self.consumed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state_sha256": state_sha256,
                "ciphertext": ciphertext,
                "expires_at": expires_at,
                "consumed": consumed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state_sha256 = d.pop("state_sha256")

        ciphertext = d.pop("ciphertext")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        consumed = d.pop("consumed")

        mcp_catalog_o_auth_flow = cls(
            state_sha256=state_sha256,
            ciphertext=ciphertext,
            expires_at=expires_at,
            consumed=consumed,
        )

        mcp_catalog_o_auth_flow.additional_properties = d
        return mcp_catalog_o_auth_flow

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
