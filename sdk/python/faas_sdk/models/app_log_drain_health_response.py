from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_log_drain_health_response_status import (
    AppLogDrainHealthResponseStatus,
    check_app_log_drain_health_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppLogDrainHealthResponse")


@_attrs_define
class AppLogDrainHealthResponse:
    """Durable, customer-safe delivery health for one runtime log destination."""

    log_drain_id: str
    status: AppLogDrainHealthResponseStatus
    active: bool
    queue_depth: int
    queue_capacity: int
    delivered_total: int
    failed_total: int
    dropped_total: int
    retries_total: int
    stream_reconnects_total: int
    gaps_total: int
    updated_at: datetime.datetime
    last_success_at: datetime.datetime | Unset = UNSET
    last_failure_at: datetime.datetime | Unset = UNSET
    last_error: str | Unset = UNSET
    """Sanitized delivery summary; never raw transport output."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        log_drain_id = self.log_drain_id

        status: str = self.status

        active = self.active

        queue_depth = self.queue_depth

        queue_capacity = self.queue_capacity

        delivered_total = self.delivered_total

        failed_total = self.failed_total

        dropped_total = self.dropped_total

        retries_total = self.retries_total

        stream_reconnects_total = self.stream_reconnects_total

        gaps_total = self.gaps_total

        updated_at = self.updated_at.isoformat()

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        last_failure_at: str | Unset = UNSET
        if not isinstance(self.last_failure_at, Unset):
            last_failure_at = self.last_failure_at.isoformat()

        last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "log_drain_id": log_drain_id,
                "status": status,
                "active": active,
                "queue_depth": queue_depth,
                "queue_capacity": queue_capacity,
                "delivered_total": delivered_total,
                "failed_total": failed_total,
                "dropped_total": dropped_total,
                "retries_total": retries_total,
                "stream_reconnects_total": stream_reconnects_total,
                "gaps_total": gaps_total,
                "updated_at": updated_at,
            }
        )
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if last_failure_at is not UNSET:
            field_dict["last_failure_at"] = last_failure_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        log_drain_id = d.pop("log_drain_id")

        status = check_app_log_drain_health_response_status(d.pop("status"))

        active = d.pop("active")

        queue_depth = d.pop("queue_depth")

        queue_capacity = d.pop("queue_capacity")

        delivered_total = d.pop("delivered_total")

        failed_total = d.pop("failed_total")

        dropped_total = d.pop("dropped_total")

        retries_total = d.pop("retries_total")

        stream_reconnects_total = d.pop("stream_reconnects_total")

        gaps_total = d.pop("gaps_total")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        _last_failure_at = d.pop("last_failure_at", UNSET)
        last_failure_at: datetime.datetime | Unset
        if isinstance(_last_failure_at, Unset):
            last_failure_at = UNSET
        else:
            last_failure_at = datetime.datetime.fromisoformat(_last_failure_at)

        last_error = d.pop("last_error", UNSET)

        app_log_drain_health_response = cls(
            log_drain_id=log_drain_id,
            status=status,
            active=active,
            queue_depth=queue_depth,
            queue_capacity=queue_capacity,
            delivered_total=delivered_total,
            failed_total=failed_total,
            dropped_total=dropped_total,
            retries_total=retries_total,
            stream_reconnects_total=stream_reconnects_total,
            gaps_total=gaps_total,
            updated_at=updated_at,
            last_success_at=last_success_at,
            last_failure_at=last_failure_at,
            last_error=last_error,
        )

        app_log_drain_health_response.additional_properties = d
        return app_log_drain_health_response

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
