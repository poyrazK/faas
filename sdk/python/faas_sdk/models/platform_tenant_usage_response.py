from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.platform_tenant_usage_bucket_response import PlatformTenantUsageBucketResponse


T = TypeVar("T", bound="PlatformTenantUsageResponse")


@_attrs_define
class PlatformTenantUsageResponse:
    """Cross-app raw usage totals with app and consumer-or-surface attribution."""

    tenant_id: UUID
    period_start: datetime.datetime
    period_end: datetime.datetime
    request_count: int
    error_count: int
    billable_units: int
    buckets: list[PlatformTenantUsageBucketResponse]
    as_of: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        request_count = self.request_count

        error_count = self.error_count

        billable_units = self.billable_units

        buckets = []
        for buckets_item_data in self.buckets:
            buckets_item = buckets_item_data.to_dict()
            buckets.append(buckets_item)

        as_of = self.as_of.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "period_start": period_start,
                "period_end": period_end,
                "request_count": request_count,
                "error_count": error_count,
                "billable_units": billable_units,
                "buckets": buckets,
                "as_of": as_of,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_usage_bucket_response import PlatformTenantUsageBucketResponse

        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        request_count = d.pop("request_count")

        error_count = d.pop("error_count")

        billable_units = d.pop("billable_units")

        buckets = []
        _buckets = d.pop("buckets")
        for buckets_item_data in _buckets:
            buckets_item = PlatformTenantUsageBucketResponse.from_dict(buckets_item_data)

            buckets.append(buckets_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        platform_tenant_usage_response = cls(
            tenant_id=tenant_id,
            period_start=period_start,
            period_end=period_end,
            request_count=request_count,
            error_count=error_count,
            billable_units=billable_units,
            buckets=buckets,
            as_of=as_of,
        )

        platform_tenant_usage_response.additional_properties = d
        return platform_tenant_usage_response

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
