from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mcp_seller_customer_totals import MCPSellerCustomerTotals
    from ..models.mcp_seller_near_limit import MCPSellerNearLimit
    from ..models.mcp_seller_stats_month import MCPSellerStatsMonth
    from ..models.mcp_seller_stats_tool import MCPSellerStatsTool


T = TypeVar("T", bound="MCPSellerStatsResponse")


@_attrs_define
class MCPSellerStatsResponse:
    """A sold MCP server's customers, tools and revenue over UTC calendar months."""

    app: str
    generated_at: datetime.datetime
    months: list[MCPSellerStatsMonth]
    """Oldest first; the last is the current month to date."""
    tools: list[MCPSellerStatsTool]
    near_limit: list[MCPSellerNearLimit]
    customers: MCPSellerCustomerTotals
    """The app's open and closed customers now."""
    estimate_complete: bool
    """False when the current month's estimate left customers out."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        generated_at = self.generated_at.isoformat()

        months = []
        for months_item_data in self.months:
            months_item = months_item_data.to_dict()
            months.append(months_item)

        tools = []
        for tools_item_data in self.tools:
            tools_item = tools_item_data.to_dict()
            tools.append(tools_item)

        near_limit = []
        for near_limit_item_data in self.near_limit:
            near_limit_item = near_limit_item_data.to_dict()
            near_limit.append(near_limit_item)

        customers = self.customers.to_dict()

        estimate_complete = self.estimate_complete

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "generated_at": generated_at,
                "months": months,
                "tools": tools,
                "near_limit": near_limit,
                "customers": customers,
                "estimate_complete": estimate_complete,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_seller_customer_totals import MCPSellerCustomerTotals
        from ..models.mcp_seller_near_limit import MCPSellerNearLimit
        from ..models.mcp_seller_stats_month import MCPSellerStatsMonth
        from ..models.mcp_seller_stats_tool import MCPSellerStatsTool

        d = dict(src_dict)
        app = d.pop("app")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        months = []
        _months = d.pop("months")
        for months_item_data in _months:
            months_item = MCPSellerStatsMonth.from_dict(months_item_data)

            months.append(months_item)

        tools = []
        _tools = d.pop("tools")
        for tools_item_data in _tools:
            tools_item = MCPSellerStatsTool.from_dict(tools_item_data)

            tools.append(tools_item)

        near_limit = []
        _near_limit = d.pop("near_limit")
        for near_limit_item_data in _near_limit:
            near_limit_item = MCPSellerNearLimit.from_dict(near_limit_item_data)

            near_limit.append(near_limit_item)

        customers = MCPSellerCustomerTotals.from_dict(d.pop("customers"))

        estimate_complete = d.pop("estimate_complete")

        mcp_seller_stats_response = cls(
            app=app,
            generated_at=generated_at,
            months=months,
            tools=tools,
            near_limit=near_limit,
            customers=customers,
            estimate_complete=estimate_complete,
        )

        mcp_seller_stats_response.additional_properties = d
        return mcp_seller_stats_response

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
