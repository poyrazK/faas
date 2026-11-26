from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mcp_seller_stats_amount import MCPSellerStatsAmount
    from ..models.mcp_seller_stats_month_signed_up_by_source import MCPSellerStatsMonthSignedUpBySource


T = TypeVar("T", bound="MCPSellerStatsMonth")


@_attrs_define
class MCPSellerStatsMonth:
    """One UTC calendar month of a sold MCP server's customers, usage and revenue."""

    month: str
    """YYYY-MM (UTC)."""
    current: bool
    signed_up: int
    signed_up_by_source: MCPSellerStatsMonthSignedUpBySource
    """Sign-ups by source: oauth, signup or owner."""
    active: int
    """Customers with billable units in the month."""
    paying: int
    """Customers whose month amounts to more than zero."""
    closed: int
    requests: int
    units: int
    revenue: list[MCPSellerStatsAmount]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        month = self.month

        current = self.current

        signed_up = self.signed_up

        signed_up_by_source = self.signed_up_by_source.to_dict()

        active = self.active

        paying = self.paying

        closed = self.closed

        requests = self.requests

        units = self.units

        revenue = []
        for revenue_item_data in self.revenue:
            revenue_item = revenue_item_data.to_dict()
            revenue.append(revenue_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "month": month,
                "current": current,
                "signed_up": signed_up,
                "signed_up_by_source": signed_up_by_source,
                "active": active,
                "paying": paying,
                "closed": closed,
                "requests": requests,
                "units": units,
                "revenue": revenue,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_seller_stats_amount import MCPSellerStatsAmount
        from ..models.mcp_seller_stats_month_signed_up_by_source import MCPSellerStatsMonthSignedUpBySource

        d = dict(src_dict)
        month = d.pop("month")

        current = d.pop("current")

        signed_up = d.pop("signed_up")

        signed_up_by_source = MCPSellerStatsMonthSignedUpBySource.from_dict(d.pop("signed_up_by_source"))

        active = d.pop("active")

        paying = d.pop("paying")

        closed = d.pop("closed")

        requests = d.pop("requests")

        units = d.pop("units")

        revenue = []
        _revenue = d.pop("revenue")
        for revenue_item_data in _revenue:
            revenue_item = MCPSellerStatsAmount.from_dict(revenue_item_data)

            revenue.append(revenue_item)

        mcp_seller_stats_month = cls(
            month=month,
            current=current,
            signed_up=signed_up,
            signed_up_by_source=signed_up_by_source,
            active=active,
            paying=paying,
            closed=closed,
            requests=requests,
            units=units,
            revenue=revenue,
        )

        mcp_seller_stats_month.additional_properties = d
        return mcp_seller_stats_month

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
