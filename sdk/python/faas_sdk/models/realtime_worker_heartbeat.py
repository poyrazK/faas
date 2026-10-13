from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeWorkerHeartbeat")


@_attrs_define
class RealtimeWorkerHeartbeat:
    """Realtime Worker Heartbeat."""

    id: UUID
    worker: str
    ready: bool
    status_code: str
    last_seen_at: datetime.datetime
    lost: bool
    """Persisted loss decision; sampled and may lag computed liveness."""
    live: bool
    """Fresh heartbeat and currently eligible credential. Not proof of connectivity or ability to process work."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        worker = self.worker

        ready = self.ready

        status_code = self.status_code

        last_seen_at = self.last_seen_at.isoformat()

        lost = self.lost

        live = self.live

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "worker": worker,
                "ready": ready,
                "status_code": status_code,
                "last_seen_at": last_seen_at,
                "lost": lost,
                "live": live,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        worker = d.pop("worker")

        ready = d.pop("ready")

        status_code = d.pop("status_code")

        last_seen_at = datetime.datetime.fromisoformat(d.pop("last_seen_at"))

        lost = d.pop("lost")

        live = d.pop("live")

        realtime_worker_heartbeat = cls(
            id=id,
            worker=worker,
            ready=ready,
            status_code=status_code,
            last_seen_at=last_seen_at,
            lost=lost,
            live=live,
        )

        realtime_worker_heartbeat.additional_properties = d
        return realtime_worker_heartbeat

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
