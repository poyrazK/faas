from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workload_uses_buckets_item import WorkloadUsesBucketsItem
    from ..models.workload_uses_queues_item import WorkloadUsesQueuesItem
    from ..models.workload_uses_realtime_item import WorkloadUsesRealtimeItem


T = TypeVar("T", bound="WorkloadUses")


@_attrs_define
class WorkloadUses:
    """A service's cross-app grants from gregale.yaml: what its workload token may call on other apps of the same account."""

    queues: list[WorkloadUsesQueuesItem] | Unset = UNSET
    realtime: list[WorkloadUsesRealtimeItem] | Unset = UNSET
    delayed_tasks: list[str] | Unset = UNSET
    """ Apps the service may schedule delayed tasks on. """
    buckets: list[WorkloadUsesBucketsItem] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queues: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.queues, Unset):
            queues = []
            for queues_item_data in self.queues:
                queues_item = queues_item_data.to_dict()
                queues.append(queues_item)

        realtime: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.realtime, Unset):
            realtime = []
            for realtime_item_data in self.realtime:
                realtime_item = realtime_item_data.to_dict()
                realtime.append(realtime_item)

        delayed_tasks: list[str] | Unset = UNSET
        if not isinstance(self.delayed_tasks, Unset):
            delayed_tasks = self.delayed_tasks

        buckets: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.buckets, Unset):
            buckets = []
            for buckets_item_data in self.buckets:
                buckets_item = buckets_item_data.to_dict()
                buckets.append(buckets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if queues is not UNSET:
            field_dict["queues"] = queues
        if realtime is not UNSET:
            field_dict["realtime"] = realtime
        if delayed_tasks is not UNSET:
            field_dict["delayed_tasks"] = delayed_tasks
        if buckets is not UNSET:
            field_dict["buckets"] = buckets

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workload_uses_buckets_item import WorkloadUsesBucketsItem
        from ..models.workload_uses_queues_item import WorkloadUsesQueuesItem
        from ..models.workload_uses_realtime_item import WorkloadUsesRealtimeItem

        d = dict(src_dict)
        _queues = d.pop("queues", UNSET)
        queues: list[WorkloadUsesQueuesItem] | Unset = UNSET
        if _queues is not UNSET:
            queues = []
            for queues_item_data in _queues:
                queues_item = WorkloadUsesQueuesItem.from_dict(queues_item_data)

                queues.append(queues_item)

        _realtime = d.pop("realtime", UNSET)
        realtime: list[WorkloadUsesRealtimeItem] | Unset = UNSET
        if _realtime is not UNSET:
            realtime = []
            for realtime_item_data in _realtime:
                realtime_item = WorkloadUsesRealtimeItem.from_dict(realtime_item_data)

                realtime.append(realtime_item)

        delayed_tasks = cast(list[str], d.pop("delayed_tasks", UNSET))

        _buckets = d.pop("buckets", UNSET)
        buckets: list[WorkloadUsesBucketsItem] | Unset = UNSET
        if _buckets is not UNSET:
            buckets = []
            for buckets_item_data in _buckets:
                buckets_item = WorkloadUsesBucketsItem.from_dict(buckets_item_data)

                buckets.append(buckets_item)

        workload_uses = cls(
            queues=queues,
            realtime=realtime,
            delayed_tasks=delayed_tasks,
            buckets=buckets,
        )

        workload_uses.additional_properties = d
        return workload_uses

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
