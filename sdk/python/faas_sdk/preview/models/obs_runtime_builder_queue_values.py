from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeBuilderQueueValues")


@_attrs_define
class ObsRuntimeBuilderQueueValues:
    """One notification queue's enabled flag, bounded counts and oldest waiting hint age; zero waiting omits the enqueue
    clock.

    """

    enabled: bool
    waiting: int
    active: int
    capacity: int
    workers: int
    oldest_wait_seconds: float
    oldest_enqueued_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        waiting = self.waiting

        active = self.active

        capacity = self.capacity

        workers = self.workers

        oldest_wait_seconds = self.oldest_wait_seconds

        oldest_enqueued_at: str | Unset = UNSET
        if not isinstance(self.oldest_enqueued_at, Unset):
            oldest_enqueued_at = self.oldest_enqueued_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "waiting": waiting,
                "active": active,
                "capacity": capacity,
                "workers": workers,
                "oldest_wait_seconds": oldest_wait_seconds,
            }
        )
        if oldest_enqueued_at is not UNSET:
            field_dict["oldest_enqueued_at"] = oldest_enqueued_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        waiting = d.pop("waiting")

        active = d.pop("active")

        capacity = d.pop("capacity")

        workers = d.pop("workers")

        oldest_wait_seconds = d.pop("oldest_wait_seconds")

        _oldest_enqueued_at = d.pop("oldest_enqueued_at", UNSET)
        oldest_enqueued_at: datetime.datetime | Unset
        if isinstance(_oldest_enqueued_at, Unset):
            oldest_enqueued_at = UNSET
        else:
            oldest_enqueued_at = datetime.datetime.fromisoformat(_oldest_enqueued_at)

        obs_runtime_builder_queue_values = cls(
            enabled=enabled,
            waiting=waiting,
            active=active,
            capacity=capacity,
            workers=workers,
            oldest_wait_seconds=oldest_wait_seconds,
            oldest_enqueued_at=oldest_enqueued_at,
        )

        obs_runtime_builder_queue_values.additional_properties = d
        return obs_runtime_builder_queue_values

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
