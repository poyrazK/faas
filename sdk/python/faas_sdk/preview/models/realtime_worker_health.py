from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_worker_health_timeout_seconds import (
    RealtimeWorkerHealthTimeoutSeconds,
    check_realtime_worker_health_timeout_seconds,
)

if TYPE_CHECKING:
    from ..models.realtime_worker_heartbeat import RealtimeWorkerHeartbeat


T = TypeVar("T", bound="RealtimeWorkerHealth")


@_attrs_define
class RealtimeWorkerHealth:
    """Realtime Worker Health."""

    workers: list[RealtimeWorkerHeartbeat]
    live_workers: int
    ready_workers: int
    timeout_seconds: RealtimeWorkerHealthTimeoutSeconds
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workers = []
        for workers_item_data in self.workers:
            workers_item = workers_item_data.to_dict()
            workers.append(workers_item)

        live_workers = self.live_workers

        ready_workers = self.ready_workers

        timeout_seconds: int = self.timeout_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workers": workers,
                "live_workers": live_workers,
                "ready_workers": ready_workers,
                "timeout_seconds": timeout_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_worker_heartbeat import RealtimeWorkerHeartbeat

        d = dict(src_dict)
        workers = []
        _workers = d.pop("workers")
        for workers_item_data in _workers:
            workers_item = RealtimeWorkerHeartbeat.from_dict(workers_item_data)

            workers.append(workers_item)

        live_workers = d.pop("live_workers")

        ready_workers = d.pop("ready_workers")

        timeout_seconds = check_realtime_worker_health_timeout_seconds(d.pop("timeout_seconds"))

        realtime_worker_health = cls(
            workers=workers,
            live_workers=live_workers,
            ready_workers=ready_workers,
            timeout_seconds=timeout_seconds,
        )

        realtime_worker_health.additional_properties = d
        return realtime_worker_health

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
