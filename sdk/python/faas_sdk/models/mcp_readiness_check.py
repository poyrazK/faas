from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.mcp_readiness_check_status import MCPReadinessCheckStatus, check_mcp_readiness_check_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessCheck")


@_attrs_define
class MCPReadinessCheck:
    """Result of one protocol or catalog check in readiness evidence."""

    name: str
    status: MCPReadinessCheckStatus
    detail: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        status: str = self.status

        detail = self.detail

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "status": status,
            }
        )
        if detail is not UNSET:
            field_dict["detail"] = detail

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        status = check_mcp_readiness_check_status(d.pop("status"))

        detail = d.pop("detail", UNSET)

        mcp_readiness_check = cls(
            name=name,
            status=status,
            detail=detail,
        )

        return mcp_readiness_check
