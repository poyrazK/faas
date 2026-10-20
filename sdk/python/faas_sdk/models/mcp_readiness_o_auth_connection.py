from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessOAuthConnection")


@_attrs_define
class MCPReadinessOAuthConnection:
    """App-local managed OAuth connection in the default environment scope, pinned to its current revision."""

    role: str
    revision: UUID
    scope: str | Unset = UNSET
    """Connection environment. Omission preserves the default environment."""

    def to_dict(self) -> dict[str, Any]:
        role = self.role

        revision = str(self.revision)

        scope = self.scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "role": role,
                "revision": revision,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        role = d.pop("role")

        revision = UUID(d.pop("revision"))

        scope = d.pop("scope", UNSET)

        mcp_readiness_o_auth_connection = cls(
            role=role,
            revision=revision,
            scope=scope,
        )

        return mcp_readiness_o_auth_connection
