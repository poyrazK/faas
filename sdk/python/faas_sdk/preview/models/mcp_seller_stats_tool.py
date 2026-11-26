from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_seller_stats_amount import MCPSellerStatsAmount


T = TypeVar("T", bound="MCPSellerStatsTool")


@_attrs_define
class MCPSellerStatsTool:
    """One MCP tool's units and customers over the window."""

    tool: str
    units: int
    customers: int
    revenue: list[MCPSellerStatsAmount] | Unset = UNSET
    """This month's estimated revenue for the tool."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tool = self.tool

        units = self.units

        customers = self.customers

        revenue: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.revenue, Unset):
            revenue = []
            for revenue_item_data in self.revenue:
                revenue_item = revenue_item_data.to_dict()
                revenue.append(revenue_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tool": tool,
                "units": units,
                "customers": customers,
            }
        )
        if revenue is not UNSET:
            field_dict["revenue"] = revenue

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_seller_stats_amount import MCPSellerStatsAmount

        d = dict(src_dict)
        tool = d.pop("tool")

        units = d.pop("units")

        customers = d.pop("customers")

        _revenue = d.pop("revenue", UNSET)
        revenue: list[MCPSellerStatsAmount] | Unset = UNSET
        if _revenue is not UNSET:
            revenue = []
            for revenue_item_data in _revenue:
                revenue_item = MCPSellerStatsAmount.from_dict(revenue_item_data)

                revenue.append(revenue_item)

        mcp_seller_stats_tool = cls(
            tool=tool,
            units=units,
            customers=customers,
            revenue=revenue,
        )

        mcp_seller_stats_tool.additional_properties = d
        return mcp_seller_stats_tool

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
