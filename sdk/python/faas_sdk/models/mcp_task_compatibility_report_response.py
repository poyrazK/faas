from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_task_handler_compatibility import MCPTaskHandlerCompatibility


T = TypeVar("T", bound="MCPTaskCompatibilityReportResponse")


@_attrs_define
class MCPTaskCompatibilityReportResponse:
    """Latest observer inventory with platform timestamps and freshness state."""

    published: bool
    handlers: list[MCPTaskHandlerCompatibility]
    truncated: bool
    freshness_seconds: int
    stale: bool
    observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        published = self.published

        handlers = []
        for handlers_item_data in self.handlers:
            handlers_item = handlers_item_data.to_dict()
            handlers.append(handlers_item)

        truncated = self.truncated

        freshness_seconds = self.freshness_seconds

        stale = self.stale

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "published": published,
                "handlers": handlers,
                "truncated": truncated,
                "freshness_seconds": freshness_seconds,
                "stale": stale,
            }
        )
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_task_handler_compatibility import MCPTaskHandlerCompatibility

        d = dict(src_dict)
        published = d.pop("published")

        handlers = []
        _handlers = d.pop("handlers")
        for handlers_item_data in _handlers:
            handlers_item = MCPTaskHandlerCompatibility.from_dict(handlers_item_data)

            handlers.append(handlers_item)

        truncated = d.pop("truncated")

        freshness_seconds = d.pop("freshness_seconds")

        stale = d.pop("stale")

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        mcp_task_compatibility_report_response = cls(
            published=published,
            handlers=handlers,
            truncated=truncated,
            freshness_seconds=freshness_seconds,
            stale=stale,
            observed_at=observed_at,
        )

        mcp_task_compatibility_report_response.additional_properties = d
        return mcp_task_compatibility_report_response

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
