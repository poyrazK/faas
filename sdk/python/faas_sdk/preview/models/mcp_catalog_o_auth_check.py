from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_o_auth_check_reason import MCPCatalogOAuthCheckReason, check_mcp_catalog_o_auth_check_reason
from ..models.mcp_catalog_o_auth_check_state import MCPCatalogOAuthCheckState, check_mcp_catalog_o_auth_check_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPCatalogOAuthCheck")


@_attrs_define
class MCPCatalogOAuthCheck:
    """Public metadata compatibility result without source errors, provider response bodies or credentials."""

    name: str
    state: MCPCatalogOAuthCheckState
    reason: MCPCatalogOAuthCheckReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        state: str = self.state

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        state = check_mcp_catalog_o_auth_check_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: MCPCatalogOAuthCheckReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_mcp_catalog_o_auth_check_reason(_reason)

        mcp_catalog_o_auth_check = cls(
            name=name,
            state=state,
            reason=reason,
        )

        mcp_catalog_o_auth_check.additional_properties = d
        return mcp_catalog_o_auth_check

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
