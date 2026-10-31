from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_o_auth_event_kind import MCPCatalogOAuthEventKind, check_mcp_catalog_o_auth_event_kind

T = TypeVar("T", bound="MCPCatalogOAuthEvent")


@_attrs_define
class MCPCatalogOAuthEvent:
    """OAuth lifecycle metadata without token values or provider response bodies."""

    id: UUID
    at: datetime.datetime
    kind: MCPCatalogOAuthEventKind
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        at = self.at.isoformat()

        kind: str = self.kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "at": at,
                "kind": kind,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = check_mcp_catalog_o_auth_event_kind(d.pop("kind"))

        mcp_catalog_o_auth_event = cls(
            id=id,
            at=at,
            kind=kind,
        )

        mcp_catalog_o_auth_event.additional_properties = d
        return mcp_catalog_o_auth_event

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
