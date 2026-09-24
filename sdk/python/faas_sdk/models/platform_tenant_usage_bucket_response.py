from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PlatformTenantUsageBucketResponse")


@_attrs_define
class PlatformTenantUsageBucketResponse:
    """One UTC day of app-consumer usage from the durable raw usage ledger."""

    app_id: UUID
    consumer_id: UUID
    window_start: datetime.datetime
    request_count: int
    error_count: int
    billable_units: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        consumer_id = str(self.consumer_id)

        window_start = self.window_start.isoformat()

        request_count = self.request_count

        error_count = self.error_count

        billable_units = self.billable_units

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "consumer_id": consumer_id,
                "window_start": window_start,
                "request_count": request_count,
                "error_count": error_count,
                "billable_units": billable_units,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        consumer_id = UUID(d.pop("consumer_id"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        request_count = d.pop("request_count")

        error_count = d.pop("error_count")

        billable_units = d.pop("billable_units")

        platform_tenant_usage_bucket_response = cls(
            app_id=app_id,
            consumer_id=consumer_id,
            window_start=window_start,
            request_count=request_count,
            error_count=error_count,
            billable_units=billable_units,
        )

        platform_tenant_usage_bucket_response.additional_properties = d
        return platform_tenant_usage_bucket_response

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
