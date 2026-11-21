from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_item_job import ObsRuntimeAppTaskItemJob, check_obs_runtime_app_task_item_job
from ..models.obs_runtime_app_task_item_state import ObsRuntimeAppTaskItemState, check_obs_runtime_app_task_item_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_app_task_class import ObsRuntimeAppTaskClass
    from ..models.obs_runtime_app_task_dispatch import ObsRuntimeAppTaskDispatch
    from ..models.obs_runtime_app_task_observation import ObsRuntimeAppTaskObservation
    from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection


T = TypeVar("T", bound="ObsRuntimeAppTaskItem")


@_attrs_define
class ObsRuntimeAppTaskItem:
    """One sanitized scheduler replica. Collection status, observer expectation, local dispatcher and 27 ordered kind/class
    observations are validated independently. No task, app, command or output identifiers are exposed.

    """

    job: ObsRuntimeAppTaskItemJob
    state: ObsRuntimeAppTaskItemState
    collection: ObsRuntimeHandoffCollection
    """Freshness and shape of the collection-status gauges. Available metadata can report unsuccessful or expired
    collection; that never establishes an empty backlog."""
    observation: ObsRuntimeAppTaskObservation
    """Whether durable task observation is expected, independently of collection success, dispatch configuration
    and runner activity. Disabled observation does not imply drained tasks."""
    dispatch: ObsRuntimeAppTaskDispatch
    """Local dispatcher activity independent of durable task collection. Disabled means opted out; stopped means
    dispatch enabled but runner inactive. Running does not prove progress. Slot occupancy includes restore,
    execution and teardown, not fleet tasks or VMs. Busy handlers need not claim work. No new stall threshold is
    inferred."""
    classes: list[ObsRuntimeAppTaskClass]
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job: str = self.job

        state: str = self.state

        collection = self.collection.to_dict()

        observation = self.observation.to_dict()

        dispatch = self.dispatch.to_dict()

        classes = []
        for classes_item_data in self.classes:
            classes_item = classes_item_data.to_dict()
            classes.append(classes_item)

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
                "job": job,
                "state": state,
                "collection": collection,
                "observation": observation,
                "dispatch": dispatch,
                "classes": classes,
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
        from ..models.obs_runtime_app_task_class import ObsRuntimeAppTaskClass
        from ..models.obs_runtime_app_task_dispatch import ObsRuntimeAppTaskDispatch
        from ..models.obs_runtime_app_task_observation import ObsRuntimeAppTaskObservation
        from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection

        d = dict(src_dict)
        job = check_obs_runtime_app_task_item_job(d.pop("job"))

        state = check_obs_runtime_app_task_item_state(d.pop("state"))

        collection = ObsRuntimeHandoffCollection.from_dict(d.pop("collection"))

        observation = ObsRuntimeAppTaskObservation.from_dict(d.pop("observation"))

        dispatch = ObsRuntimeAppTaskDispatch.from_dict(d.pop("dispatch"))

        classes = []
        _classes = d.pop("classes")
        for classes_item_data in _classes:
            classes_item = ObsRuntimeAppTaskClass.from_dict(classes_item_data)

            classes.append(classes_item)

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        obs_runtime_app_task_item = cls(
            job=job,
            state=state,
            collection=collection,
            observation=observation,
            dispatch=dispatch,
            classes=classes,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            scrape_observed_at=scrape_observed_at,
        )

        obs_runtime_app_task_item.additional_properties = d
        return obs_runtime_app_task_item

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
