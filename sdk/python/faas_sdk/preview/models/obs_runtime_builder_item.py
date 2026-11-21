from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_builder_item_state import ObsRuntimeBuilderItemState, check_obs_runtime_builder_item_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_builder_queue import ObsRuntimeBuilderQueue
    from ..models.obs_runtime_builder_slots import ObsRuntimeBuilderSlots
    from ..models.obs_runtime_builder_worker import ObsRuntimeBuilderWorker


T = TypeVar("T", bound="ObsRuntimeBuilderItem")


@_attrs_define
class ObsRuntimeBuilderItem:
    """Sanitized observations of one selected builder target, with independently validated worker, slot and notification
    queue components.

    """

    state: ObsRuntimeBuilderItemState
    worker: ObsRuntimeBuilderWorker
    """Waiting/working/draining/overdue describe usable sampled poller evidence, not successful builds. Observed_at
    is the oldest original sample clock among this component's required gauges. Invalid or incomplete components
    omit values."""
    slots: ObsRuntimeBuilderSlots
    """Process-local reserved build slots; zero usage does not establish free host capacity. Observed_at is the
    oldest original sample clock of the two gauges."""
    queues: list[ObsRuntimeBuilderQueue]
    """Exactly the build and cancel queues, in that order."""
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        worker = self.worker.to_dict()

        slots = self.slots.to_dict()

        queues = []
        for queues_item_data in self.queues:
            queues_item = queues_item_data.to_dict()
            queues.append(queues_item)

        node = self.node

        target_id = self.target_id

        unexpected = self.unexpected

        scrape_observed_at: str | Unset = UNSET
        if not isinstance(self.scrape_observed_at, Unset):
            scrape_observed_at = self.scrape_observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "worker": worker,
                "slots": slots,
                "queues": queues,
            }
        )
        if node is not UNSET:
            field_dict["node"] = node
        if target_id is not UNSET:
            field_dict["target_id"] = target_id
        if unexpected is not UNSET:
            field_dict["unexpected"] = unexpected
        if scrape_observed_at is not UNSET:
            field_dict["scrape_observed_at"] = scrape_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_builder_queue import ObsRuntimeBuilderQueue
        from ..models.obs_runtime_builder_slots import ObsRuntimeBuilderSlots
        from ..models.obs_runtime_builder_worker import ObsRuntimeBuilderWorker

        d = dict(src_dict)
        state = check_obs_runtime_builder_item_state(d.pop("state"))

        worker = ObsRuntimeBuilderWorker.from_dict(d.pop("worker"))

        slots = ObsRuntimeBuilderSlots.from_dict(d.pop("slots"))

        queues = []
        _queues = d.pop("queues")
        for queues_item_data in _queues:
            queues_item = ObsRuntimeBuilderQueue.from_dict(queues_item_data)

            queues.append(queues_item)

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        obs_runtime_builder_item = cls(
            state=state,
            worker=worker,
            slots=slots,
            queues=queues,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            scrape_observed_at=scrape_observed_at,
        )

        obs_runtime_builder_item.additional_properties = d
        return obs_runtime_builder_item

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
