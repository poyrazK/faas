from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MCPReadinessProbeRequest")


@_attrs_define
class MCPReadinessProbeRequest:
    """Role selection and options for a platform-executed deployment readiness probe."""

    role: str

    def to_dict(self) -> dict[str, Any]:
        role = self.role

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "role": role,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        role = d.pop("role")

        mcp_readiness_probe_request = cls(
            role=role,
        )

        return mcp_readiness_probe_request
