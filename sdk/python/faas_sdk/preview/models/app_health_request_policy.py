from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AppHealthRequestPolicy")


@_attrs_define
class AppHealthRequestPolicy:
    """Diagnostic severity defaults, independent of customer SLO configuration. Error thresholds require both minimum
    request and server-error counts.

    """

    minimum_requests: int
    minimum_server_errors: int
    warning_error_rate_pct: float
    unhealthy_error_rate_pct: float

    def to_dict(self) -> dict[str, Any]:
        minimum_requests = self.minimum_requests

        minimum_server_errors = self.minimum_server_errors

        warning_error_rate_pct = self.warning_error_rate_pct

        unhealthy_error_rate_pct = self.unhealthy_error_rate_pct

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "minimum_requests": minimum_requests,
                "minimum_server_errors": minimum_server_errors,
                "warning_error_rate_pct": warning_error_rate_pct,
                "unhealthy_error_rate_pct": unhealthy_error_rate_pct,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        minimum_requests = d.pop("minimum_requests")

        minimum_server_errors = d.pop("minimum_server_errors")

        warning_error_rate_pct = d.pop("warning_error_rate_pct")

        unhealthy_error_rate_pct = d.pop("unhealthy_error_rate_pct")

        app_health_request_policy = cls(
            minimum_requests=minimum_requests,
            minimum_server_errors=minimum_server_errors,
            warning_error_rate_pct=warning_error_rate_pct,
            unhealthy_error_rate_pct=unhealthy_error_rate_pct,
        )

        return app_health_request_policy
