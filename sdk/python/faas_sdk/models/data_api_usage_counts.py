from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataAPIUsageCounts")


@_attrs_define
class DataAPIUsageCounts:
    """Data API usage counts. Admitted requests by access kind, gateway and engine rejections (4xx), errors (5xx), requests
    refused by owner caps, rows the engine reported and response bytes. Owner telemetry from the app's runtime, not
    billing records.

    """

    user_requests: int
    anonymous_requests: int
    server_requests: int
    rejected_requests: int
    error_requests: int
    capped_requests: int
    rows_returned: int
    response_bytes: int

    def to_dict(self) -> dict[str, Any]:
        user_requests = self.user_requests

        anonymous_requests = self.anonymous_requests

        server_requests = self.server_requests

        rejected_requests = self.rejected_requests

        error_requests = self.error_requests

        capped_requests = self.capped_requests

        rows_returned = self.rows_returned

        response_bytes = self.response_bytes

        field_dict: dict[str, Any] = {}

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

        data_api_usage_counts = cls(
            user_requests=user_requests,
            anonymous_requests=anonymous_requests,
            server_requests=server_requests,
            rejected_requests=rejected_requests,
            error_requests=error_requests,
            capped_requests=capped_requests,
            rows_returned=rows_returned,
            response_bytes=response_bytes,
        )

        return data_api_usage_counts
