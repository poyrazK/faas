from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_o_auth_lifecycle_credential_state import (
    MCPCatalogOAuthLifecycleCredentialState,
    check_mcp_catalog_o_auth_lifecycle_credential_state,
)
from ..models.mcp_catalog_o_auth_lifecycle_local_state import (
    MCPCatalogOAuthLifecycleLocalState,
    check_mcp_catalog_o_auth_lifecycle_local_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_o_auth_event import MCPCatalogOAuthEvent
    from ..models.mcp_catalog_o_auth_revocation import MCPCatalogOAuthRevocation


T = TypeVar("T", bound="MCPCatalogOAuthLifecycle")


@_attrs_define
class MCPCatalogOAuthLifecycle:
    """Durable local connection state, provider revocation progress and bounded metadata audit history."""

    local_state: MCPCatalogOAuthLifecycleLocalState
    history: list[MCPCatalogOAuthEvent]
    credential_state: MCPCatalogOAuthLifecycleCredentialState | Unset = UNSET
    revocation: MCPCatalogOAuthRevocation | Unset = UNSET
    """Durable RFC 7009 progress; acknowledgement also covers tokens already invalid at the provider. Private
    fields are omitted from all public projections."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        local_state: str = self.local_state

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        credential_state: str | Unset = UNSET
        if not isinstance(self.credential_state, Unset):
            credential_state = self.credential_state

        revocation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.revocation, Unset):
            revocation = self.revocation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "local_state": local_state,
                "history": history,
            }
        )
        if credential_state is not UNSET:
            field_dict["credential_state"] = credential_state
        if revocation is not UNSET:
            field_dict["revocation"] = revocation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_o_auth_event import MCPCatalogOAuthEvent
        from ..models.mcp_catalog_o_auth_revocation import MCPCatalogOAuthRevocation

        d = dict(src_dict)
        local_state = check_mcp_catalog_o_auth_lifecycle_local_state(d.pop("local_state"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = MCPCatalogOAuthEvent.from_dict(history_item_data)

            history.append(history_item)

        _credential_state = d.pop("credential_state", UNSET)
        credential_state: MCPCatalogOAuthLifecycleCredentialState | Unset
        if isinstance(_credential_state, Unset):
            credential_state = UNSET
        else:
            credential_state = check_mcp_catalog_o_auth_lifecycle_credential_state(_credential_state)

        _revocation = d.pop("revocation", UNSET)
        revocation: MCPCatalogOAuthRevocation | Unset
        if isinstance(_revocation, Unset):
            revocation = UNSET
        else:
            revocation = MCPCatalogOAuthRevocation.from_dict(_revocation)

        mcp_catalog_o_auth_lifecycle = cls(
            local_state=local_state,
            history=history,
            credential_state=credential_state,
            revocation=revocation,
        )

        mcp_catalog_o_auth_lifecycle.additional_properties = d
        return mcp_catalog_o_auth_lifecycle

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
