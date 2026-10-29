from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_tenant_activity_filters import PlatformTenantActivityFilters
    from ..models.platform_tenant_activity_item import PlatformTenantActivityItem


T = TypeVar("T", bound="PlatformTenantActivityResponse")


@_attrs_define
class PlatformTenantActivityResponse:
    """A retention-bounded page of observed debugger evidence. Page totals weight each collapsed telemetry row by
    request.count and must not be treated as complete usage.

    """

    tenant_id: UUID
    since: str
    """Actual lookback duration after applying the account's debugger retention ceiling."""
    window_start: datetime.datetime
    window_end: datetime.datetime
    plan_retention_days: int
    retention_clamped: bool
    page_telemetry_rows: int
    page_represented_requests: int
    page_error_requests: int
    page_complete: bool
    """True when this page contains all matching retained rows in the pinned window; it does not imply complete
    telemetry capture."""
    filters: PlatformTenantActivityFilters
    """Normalized app and HTTP status filters applied to every page."""
    requests: list[PlatformTenantActivityItem]
    next_cursor: str | Unset = UNSET
    """Present when another page of retained rows exists."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        since = self.since

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        plan_retention_days = self.plan_retention_days

        retention_clamped = self.retention_clamped

        page_telemetry_rows = self.page_telemetry_rows

        page_represented_requests = self.page_represented_requests

        page_error_requests = self.page_error_requests

        page_complete = self.page_complete

        filters = self.filters.to_dict()

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "since": since,
                "window_start": window_start,
                "window_end": window_end,
                "plan_retention_days": plan_retention_days,
                "retention_clamped": retention_clamped,
                "page_telemetry_rows": page_telemetry_rows,
                "page_represented_requests": page_represented_requests,
                "page_error_requests": page_error_requests,
                "page_complete": page_complete,
                "filters": filters,
                "requests": requests,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_activity_filters import PlatformTenantActivityFilters
        from ..models.platform_tenant_activity_item import PlatformTenantActivityItem

        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        since = d.pop("since")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        plan_retention_days = d.pop("plan_retention_days")

        retention_clamped = d.pop("retention_clamped")

        page_telemetry_rows = d.pop("page_telemetry_rows")

        page_represented_requests = d.pop("page_represented_requests")

        page_error_requests = d.pop("page_error_requests")

        page_complete = d.pop("page_complete")

        filters = PlatformTenantActivityFilters.from_dict(d.pop("filters"))

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = PlatformTenantActivityItem.from_dict(requests_item_data)

            requests.append(requests_item)

        next_cursor = d.pop("next_cursor", UNSET)

        platform_tenant_activity_response = cls(
            tenant_id=tenant_id,
            since=since,
            window_start=window_start,
            window_end=window_end,
            plan_retention_days=plan_retention_days,
            retention_clamped=retention_clamped,
            page_telemetry_rows=page_telemetry_rows,
            page_represented_requests=page_represented_requests,
            page_error_requests=page_error_requests,
            page_complete=page_complete,
            filters=filters,
            requests=requests,
            next_cursor=next_cursor,
        )

        platform_tenant_activity_response.additional_properties = d
        return platform_tenant_activity_response

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
