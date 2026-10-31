from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_task_handler_compatibility import MCPTaskHandlerCompatibility


T = TypeVar("T", bound="MCPTaskCompatibilityReportRequest")


@_attrs_define
class MCPTaskCompatibilityReportRequest:
    """Bounded aggregate handler inventory published by the task observer."""

    handlers: list[MCPTaskHandlerCompatibility]
    truncated: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        handlers = []
        for handlers_item_data in self.handlers:
            handlers_item = handlers_item_data.to_dict()
            handlers.append(handlers_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "handlers": handlers,
            }
        )
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_task_handler_compatibility import MCPTaskHandlerCompatibility

        d = dict(src_dict)
        handlers = []
        _handlers = d.pop("handlers")
        for handlers_item_data in _handlers:
            handlers_item = MCPTaskHandlerCompatibility.from_dict(handlers_item_data)

            handlers.append(handlers_item)

        truncated = d.pop("truncated", UNSET)

        mcp_task_compatibility_report_request = cls(
            handlers=handlers,
            truncated=truncated,
        )

        return mcp_task_compatibility_report_request
