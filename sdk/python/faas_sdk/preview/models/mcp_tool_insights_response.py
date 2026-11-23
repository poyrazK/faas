from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_tool_insights_response_telemetry import (
    MCPToolInsightsResponseTelemetry,
    check_mcp_tool_insights_response_telemetry,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_tool_insight import MCPToolInsight


T = TypeVar("T", bound="MCPToolInsightsResponse")


@_attrs_define
class MCPToolInsightsResponse:
    """Per-tool MCP usage and estimated compute for one app over the trailing window."""

    app: str
    window: str
    """Trailing window every figure covers."""
    observed_at: datetime.datetime
    telemetry: MCPToolInsightsResponseTelemetry
    memory_mb: int
    """Plan RAM; compute is estimated at memory_mb + 8."""
    tools: list[MCPToolInsight]
    last_event: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        window = self.window

        observed_at = self.observed_at.isoformat()

        telemetry: str = self.telemetry

        memory_mb = self.memory_mb

        tools = []
        for tools_item_data in self.tools:
            tools_item = tools_item_data.to_dict()
            tools.append(tools_item)

        last_event: str | Unset = UNSET
        if not isinstance(self.last_event, Unset):
            last_event = self.last_event.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "window": window,
                "observed_at": observed_at,
                "telemetry": telemetry,
                "memory_mb": memory_mb,
                "tools": tools,
            }
        )
        if last_event is not UNSET:
            field_dict["last_event"] = last_event

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_tool_insight import MCPToolInsight

        d = dict(src_dict)
        app = d.pop("app")

        window = d.pop("window")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        telemetry = check_mcp_tool_insights_response_telemetry(d.pop("telemetry"))

        memory_mb = d.pop("memory_mb")

        tools = []
        _tools = d.pop("tools")
        for tools_item_data in _tools:
            tools_item = MCPToolInsight.from_dict(tools_item_data)

            tools.append(tools_item)

        _last_event = d.pop("last_event", UNSET)
        last_event: datetime.datetime | Unset
        if isinstance(_last_event, Unset):
            last_event = UNSET
        else:
            last_event = datetime.datetime.fromisoformat(_last_event)

        mcp_tool_insights_response = cls(
            app=app,
            window=window,
            observed_at=observed_at,
            telemetry=telemetry,
            memory_mb=memory_mb,
            tools=tools,
            last_event=last_event,
        )

        mcp_tool_insights_response.additional_properties = d
        return mcp_tool_insights_response

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
