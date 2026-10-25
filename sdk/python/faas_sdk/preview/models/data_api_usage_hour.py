from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DataAPIUsageHour")


@_attrs_define
class DataAPIUsageHour:
    """One UTC hour's usage delta in a runtime report."""

    user_requests: int
    anonymous_requests: int
    server_requests: int
    rejected_requests: int
    error_requests: int
    capped_requests: int
    rows_returned: int
    response_bytes: int
    hour: datetime.datetime
    """UTC hour the delta belongs to; aligned to the hour."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        user_requests = self.user_requests

        anonymous_requests = self.anonymous_requests

        server_requests = self.server_requests

        rejected_requests = self.rejected_requests

        error_requests = self.error_requests

        capped_requests = self.capped_requests

        rows_returned = self.rows_returned

        response_bytes = self.response_bytes

        hour = self.hour.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "user_requests": user_requests,
                "anonymous_requests": anonymous_requests,
                "server_requests": server_requests,
                "rejected_requests": rejected_requests,
                "error_requests": error_requests,
                "capped_requests": capped_requests,
                "rows_returned": rows_returned,
                "response_bytes": response_bytes,
                "hour": hour,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        user_requests = d.pop("user_requests")

        anonymous_requests = d.pop("anonymous_requests")

        server_requests = d.pop("server_requests")

        rejected_requests = d.pop("rejected_requests")

        error_requests = d.pop("error_requests")

        capped_requests = d.pop("capped_requests")

        rows_returned = d.pop("rows_returned")

        response_bytes = d.pop("response_bytes")

        hour = datetime.datetime.fromisoformat(d.pop("hour"))

        data_api_usage_hour = cls(
            user_requests=user_requests,
            anonymous_requests=anonymous_requests,
            server_requests=server_requests,
            rejected_requests=rejected_requests,
            error_requests=error_requests,
            capped_requests=capped_requests,
            rows_returned=rows_returned,
            response_bytes=response_bytes,
            hour=hour,
        )

        data_api_usage_hour.additional_properties = d
        return data_api_usage_hour

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
