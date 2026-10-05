from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.app_health_response_phase import AppHealthResponsePhase, check_app_health_response_phase
from ..models.app_health_response_scope import AppHealthResponseScope, check_app_health_response_scope
from ..models.app_health_response_status import AppHealthResponseStatus, check_app_health_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_health_capacity import AppHealthCapacity
    from ..models.app_health_check import AppHealthCheck
    from ..models.app_health_requests import AppHealthRequests


T = TypeVar("T", bound="AppHealthResponse")


@_attrs_define
class AppHealthResponse:
    """Read-only observed health of default-scope HTTP serving workloads."""

    app_id: str
    status: AppHealthResponseStatus
    phase: AppHealthResponsePhase
    summary: str
    scope: AppHealthResponseScope
    evaluated_at: datetime.datetime
    valid_for_seconds: int
    """Maximum age before clients must reconfirm this assessment."""
    serving_deployment_ids: list[str]
    capacity: AppHealthCapacity
    """Measured serving replica counts, with explicit evidence availability."""
    checks: list[AppHealthCheck]
    metrics_as_of: datetime.datetime | Unset = UNSET
    """Actual telemetry sample time; absent when unconfirmed."""
    latest_deployment_id: str | Unset = UNSET
    requests: AppHealthRequests | Unset = UNSET
    """Scoped request evidence; counts are confirmed only when known is true. Covers current serving releases,
    excluding previous releases and other scopes."""

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        status: str = self.status

        phase: str = self.phase

        summary = self.summary

        scope: str = self.scope

        evaluated_at = self.evaluated_at.isoformat()

        valid_for_seconds = self.valid_for_seconds

        serving_deployment_ids = self.serving_deployment_ids

        capacity = self.capacity.to_dict()

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        metrics_as_of: str | Unset = UNSET
        if not isinstance(self.metrics_as_of, Unset):
            metrics_as_of = self.metrics_as_of.isoformat()

        latest_deployment_id = self.latest_deployment_id

        requests: dict[str, Any] | Unset = UNSET
        if not isinstance(self.requests, Unset):
            requests = self.requests.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "status": status,
                "phase": phase,
                "summary": summary,
                "scope": scope,
                "evaluated_at": evaluated_at,
                "valid_for_seconds": valid_for_seconds,
                "serving_deployment_ids": serving_deployment_ids,
                "capacity": capacity,
                "checks": checks,
            }
        )
        if metrics_as_of is not UNSET:
            field_dict["metrics_as_of"] = metrics_as_of
        if latest_deployment_id is not UNSET:
            field_dict["latest_deployment_id"] = latest_deployment_id
        if requests is not UNSET:
            field_dict["requests"] = requests

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_health_capacity import AppHealthCapacity
        from ..models.app_health_check import AppHealthCheck
        from ..models.app_health_requests import AppHealthRequests

        d = dict(src_dict)
        app_id = d.pop("app_id")

        status = check_app_health_response_status(d.pop("status"))

        phase = check_app_health_response_phase(d.pop("phase"))

        summary = d.pop("summary")

        scope = check_app_health_response_scope(d.pop("scope"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        valid_for_seconds = d.pop("valid_for_seconds")

        serving_deployment_ids = cast(list[str], d.pop("serving_deployment_ids"))

        capacity = AppHealthCapacity.from_dict(d.pop("capacity"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = AppHealthCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        _metrics_as_of = d.pop("metrics_as_of", UNSET)
        metrics_as_of: datetime.datetime | Unset
        if isinstance(_metrics_as_of, Unset):
            metrics_as_of = UNSET
        else:
            metrics_as_of = datetime.datetime.fromisoformat(_metrics_as_of)

        latest_deployment_id = d.pop("latest_deployment_id", UNSET)

        _requests = d.pop("requests", UNSET)
        requests: AppHealthRequests | Unset
        if isinstance(_requests, Unset):
            requests = UNSET
        else:
            requests = AppHealthRequests.from_dict(_requests)

        app_health_response = cls(
            app_id=app_id,
            status=status,
            phase=phase,
            summary=summary,
            scope=scope,
            evaluated_at=evaluated_at,
            valid_for_seconds=valid_for_seconds,
            serving_deployment_ids=serving_deployment_ids,
            capacity=capacity,
            checks=checks,
            metrics_as_of=metrics_as_of,
            latest_deployment_id=latest_deployment_id,
            requests=requests,
        )

        return app_health_response
