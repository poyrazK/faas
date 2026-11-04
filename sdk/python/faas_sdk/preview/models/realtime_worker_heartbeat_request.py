from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeWorkerHeartbeatRequest")


@_attrs_define
class RealtimeWorkerHeartbeatRequest:
    """Realtime Worker Heartbeat Request."""

    ready: bool
    worker: str | Unset = UNSET
    """Required on account route; optional for scoped workers and must match credential name if supplied. Sharing a
    credential aggregates processes."""
    status_code: str | Unset = UNSET
    """Short diagnostic code only; never include secrets."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ready = self.ready

        worker = self.worker

        status_code = self.status_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ready": ready,
            }
        )
        if worker is not UNSET:
            field_dict["worker"] = worker
        if status_code is not UNSET:
            field_dict["status_code"] = status_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ready = d.pop("ready")

        worker = d.pop("worker", UNSET)

        status_code = d.pop("status_code", UNSET)

        realtime_worker_heartbeat_request = cls(
            ready=ready,
            worker=worker,
            status_code=status_code,
        )

        realtime_worker_heartbeat_request.additional_properties = d
        return realtime_worker_heartbeat_request

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
