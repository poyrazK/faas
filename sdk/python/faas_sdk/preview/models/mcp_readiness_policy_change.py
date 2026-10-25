from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MCPReadinessPolicyChange")


@_attrs_define
class MCPReadinessPolicyChange:
    """One field change between two readiness policy configurations."""

    path: str
    """JSON Pointer configuration field or role; role order and audit fields are ignored"""
    before: Any
    after: Any

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        before = self.before

        after = self.after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
                "before": before,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        before = d.pop("before")

        after = d.pop("after")

        mcp_readiness_policy_change = cls(
            path=path,
            before=before,
            after=after,
        )

        return mcp_readiness_policy_change
