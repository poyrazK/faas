from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.hosting_route_check_result_status import (
    HostingRouteCheckResultStatus,
    check_hosting_route_check_result_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="HostingRouteCheckResult")


@_attrs_define
class HostingRouteCheckResult:
    """Bounded, non-secret evidence from one authenticated candidate GET. Request and response bodies are omitted."""

    method: str
    path: str
    status: HostingRouteCheckResultStatus
    verified_at: datetime.datetime
    """Zero time (0001-01-01T00:00:00Z) when the check did not run."""
    status_code: int | Unset = UNSET
    latency_ms: int | Unset = UNSET
    request_id: str | Unset = UNSET
    error_code: str | Unset = UNSET
    error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        status: str = self.status

        verified_at = self.verified_at.isoformat()

        status_code = self.status_code

        latency_ms = self.latency_ms

        request_id = self.request_id

        error_code = self.error_code

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "status": status,
                "verified_at": verified_at,
            }
        )
        if status_code is not UNSET:
            field_dict["status_code"] = status_code
        if latency_ms is not UNSET:
            field_dict["latency_ms"] = latency_ms
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        status = check_hosting_route_check_result_status(d.pop("status"))

        verified_at = datetime.datetime.fromisoformat(d.pop("verified_at"))

        status_code = d.pop("status_code", UNSET)

        latency_ms = d.pop("latency_ms", UNSET)

        request_id = d.pop("request_id", UNSET)

        error_code = d.pop("error_code", UNSET)

        error = d.pop("error", UNSET)

        hosting_route_check_result = cls(
            method=method,
            path=path,
            status=status,
            verified_at=verified_at,
            status_code=status_code,
            latency_ms=latency_ms,
            request_id=request_id,
            error_code=error_code,
            error=error,
        )

        hosting_route_check_result.additional_properties = d
        return hosting_route_check_result

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
