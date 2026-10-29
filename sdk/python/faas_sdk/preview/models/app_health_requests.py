from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.app_health_requests_coverage import AppHealthRequestsCoverage, check_app_health_requests_coverage

if TYPE_CHECKING:
    from ..models.app_health_request_policy import AppHealthRequestPolicy


T = TypeVar("T", bound="AppHealthRequests")


@_attrs_define
class AppHealthRequests:
    """Scoped request evidence; counts are confirmed only when known is true. Covers current serving releases, excluding
    previous releases and other scopes.

    """

    known: bool
    coverage: AppHealthRequestsCoverage
    window_seconds: int
    deployment_ids: list[str]
    request_count: int
    server_errors: int
    error_rate_pct: float
    policy: AppHealthRequestPolicy
    """Diagnostic severity defaults, independent of customer SLO configuration. Error thresholds require both
    minimum request and server-error counts."""

    def to_dict(self) -> dict[str, Any]:
        known = self.known

        coverage: str = self.coverage

        window_seconds = self.window_seconds

        deployment_ids = self.deployment_ids

        request_count = self.request_count

        server_errors = self.server_errors

        error_rate_pct = self.error_rate_pct

        policy = self.policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "known": known,
                "coverage": coverage,
                "window_seconds": window_seconds,
                "deployment_ids": deployment_ids,
                "request_count": request_count,
                "server_errors": server_errors,
                "error_rate_pct": error_rate_pct,
                "policy": policy,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_health_request_policy import AppHealthRequestPolicy

        d = dict(src_dict)
        known = d.pop("known")

        coverage = check_app_health_requests_coverage(d.pop("coverage"))

        window_seconds = d.pop("window_seconds")

        deployment_ids = cast(list[str], d.pop("deployment_ids"))

        request_count = d.pop("request_count")

        server_errors = d.pop("server_errors")

        error_rate_pct = d.pop("error_rate_pct")

        policy = AppHealthRequestPolicy.from_dict(d.pop("policy"))

        app_health_requests = cls(
            known=known,
            coverage=coverage,
            window_seconds=window_seconds,
            deployment_ids=deployment_ids,
            request_count=request_count,
            server_errors=server_errors,
            error_rate_pct=error_rate_pct,
            policy=policy,
        )

        return app_health_requests
