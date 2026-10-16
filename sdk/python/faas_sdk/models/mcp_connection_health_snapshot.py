from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_connection_health_issue import MCPConnectionHealthIssue
    from ..models.mcp_connection_health_snapshot_counts import MCPConnectionHealthSnapshotCounts


T = TypeVar("T", bound="MCPConnectionHealthSnapshot")


@_attrs_define
class MCPConnectionHealthSnapshot:
    """Account-wide or app-filtered connection issues with complete counts independent of issue pagination. Incomplete
    inventory cannot prove alert recovery.

    """

    observed_at: datetime.datetime
    complete: bool
    counts: MCPConnectionHealthSnapshotCounts
    total_issues: int
    items: list[MCPConnectionHealthIssue]
    next_offset: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        complete = self.complete

        counts = self.counts.to_dict()

        total_issues = self.total_issues

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_offset = self.next_offset

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "complete": complete,
                "counts": counts,
                "total_issues": total_issues,
                "items": items,
            }
        )
        if next_offset is not UNSET:
            field_dict["next_offset"] = next_offset

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_connection_health_issue import MCPConnectionHealthIssue
        from ..models.mcp_connection_health_snapshot_counts import MCPConnectionHealthSnapshotCounts

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        complete = d.pop("complete")

        counts = MCPConnectionHealthSnapshotCounts.from_dict(d.pop("counts"))

        total_issues = d.pop("total_issues")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = MCPConnectionHealthIssue.from_dict(items_item_data)

            items.append(items_item)

        next_offset = d.pop("next_offset", UNSET)

        mcp_connection_health_snapshot = cls(
            observed_at=observed_at,
            complete=complete,
            counts=counts,
            total_issues=total_issues,
            items=items,
            next_offset=next_offset,
        )

        mcp_connection_health_snapshot.additional_properties = d
        return mcp_connection_health_snapshot

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
